package operations

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"example.com/cabinet/backend/db"
	"example.com/cabinet/backend/internal/modules/accounts"
	auditreports "example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/support"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool       *pgxpool.Pool
	sourceURL  string
	restoreURL string
}

func New(pool *pgxpool.Pool, sourceURL, restoreURL string) *Service {
	return &Service{pool: pool, sourceURL: sourceURL, restoreURL: restoreURL}
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func (s *Service) audit(ctx context.Context, actor, operation uuid.UUID, action, code string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return errors.New("AUDIT_UNAVAILABLE")
	}
	defer tx.Rollback(ctx)
	reason := "operation_id=" + operation.String() + ";code=" + code
	if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: time.Now().UTC(), Action: action, AccountID: actor, OperatorAccountID: &actor, Reason: &reason}) != nil || tx.Commit(ctx) != nil {
		return errors.New("AUDIT_UNAVAILABLE")
	}
	return nil
}

func (s *Service) authorize(ctx context.Context, actor uuid.UUID) (pgx.Tx, error) {
	owner := accounts.New(s.pool, nil, nil, accounts.Config{})
	if owner.RequireOperator(ctx, actor) != nil {
		return nil, errors.New("OPERATOR_REQUIRED")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, errors.New("OPERATOR_REQUIRED")
	}
	if _, err = owner.LockOperatorPair(ctx, tx, actor, actor); err != nil {
		tx.Rollback(ctx)
		return nil, errors.New("OPERATOR_REQUIRED")
	}
	return tx, nil
}

func pgMajor(ctx context.Context, tx pgx.Tx) (int, error) {
	var major int
	if tx.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer / 10000`).Scan(&major) != nil || major < 1 {
		return 0, errors.New("SCHEMA_MISMATCH")
	}
	return major, nil
}

func (s *Service) Create(ctx context.Context, actor uuid.UUID, dir string) (err error) {
	if err = ValidateConnectionURL(s.sourceURL); err != nil {
		return err
	}
	op := uuid.New()
	defer func() {
		if err != nil {
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.audit(c, actor, op, "backup.create.failed", err.Error())
		}
	}()
	if accounts.New(s.pool, nil, nil, accounts.Config{}).RequireOperator(ctx, actor) != nil {
		return errors.New("OPERATOR_REQUIRED")
	}
	if err = s.audit(ctx, actor, op, "backup.create.started", "STARTED"); err != nil {
		return err
	}
	authority, err := s.authorize(ctx, actor)
	if err != nil {
		return err
	}
	defer rollback(authority)
	if err = privatePath(dir, false, true); err != nil {
		return err
	}
	if _, statErr := os.Lstat(dir); !os.IsNotExist(statErr) {
		return errors.New("PACKAGE_EXISTS")
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		return errors.New("INVALID_PACKAGE")
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	listTx, e := s.pool.Begin(ctx)
	if e != nil {
		return errors.New("BACKUP_INCONSISTENT")
	}
	names, e := publicNames(ctx, listTx, "rp")
	_ = listTx.Rollback(ctx)
	if e != nil || len(names) == 0 {
		return errors.New("BACKUP_INCONSISTENT")
	}
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if e != nil {
		return errors.New("BACKUP_INCONSISTENT")
	}
	defer rollback(tx)
	if err = lockTables(ctx, tx, names); err != nil {
		return err
	}
	if err = setInventorySession(ctx, tx); err != nil {
		return err
	}
	lockedNames, e := publicNames(ctx, tx, "rp")
	if e != nil || !reflect.DeepEqual(names, lockedNames) {
		return errors.New("SCHEMA_MISMATCH")
	}
	schema, e := db.BackupSchemaState(ctx, tx)
	if e != nil {
		return errors.New("SCHEMA_MISMATCH")
	}
	major, e := pgMajor(ctx, tx)
	if e != nil {
		return e
	}
	before, e := inventorySequences(ctx, tx)
	if e != nil {
		return e
	}
	var snapshot string
	if tx.QueryRow(ctx, `SELECT pg_export_snapshot()`).Scan(&snapshot) != nil {
		return errors.New("BACKUP_INCONSISTENT")
	}
	tables, e := inventoryTables(ctx, tx)
	if e != nil {
		return e
	}
	attachments, e := support.BackupInventory(ctx, tx)
	if e != nil {
		return e
	}
	dumpPath := filepath.Join(dir, "database.dump")
	dumpFile, e := os.OpenFile(dumpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return errors.New("INVALID_PACKAGE")
	}
	if e = dumpFile.Close(); e != nil {
		return errors.New("INVALID_PACKAGE")
	}
	if err = runPG(ctx, s.sourceURL, "pg_dump", "BACKUP_DUMP_FAILED", "--format=custom", "--no-owner", "--no-privileges", "--snapshot="+snapshot, "--file="+dumpPath); err != nil {
		return err
	}
	after, e := inventorySequences(ctx, tx)
	if e != nil || !reflect.DeepEqual(before, after) {
		return errors.New("BACKUP_INCONSISTENT")
	}
	if err = tx.Rollback(ctx); err != nil {
		return errors.New("BACKUP_INCONSISTENT")
	}
	if err = privatePath(dumpPath, true, false); err != nil {
		return err
	}
	size, sum, e := hashFile(dumpPath)
	if e != nil || size < 1 {
		return errors.New("BACKUP_DUMP_FAILED")
	}
	m := manifest{Version: 1, OperationID: op, CreatedAt: time.Now().UTC(), PGMajor: major, Schema: schema, DumpSize: size, DumpSHA256: sum, Tables: tables, Sequences: before, Attachments: attachments}
	b, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return errors.New("INVALID_PACKAGE")
	}
	metaPath := filepath.Join(dir, "manifest.json")
	f, e := os.OpenFile(metaPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return errors.New("INVALID_PACKAGE")
	}
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	if closeErr := f.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return errors.New("INVALID_PACKAGE")
	}
	if _, e = readPackage(dir); e != nil {
		return e
	}
	dumpSync, e := os.Open(dumpPath)
	if e != nil {
		return errors.New("INVALID_PACKAGE")
	}
	e = dumpSync.Sync()
	if closeErr := dumpSync.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return errors.New("INVALID_PACKAGE")
	}
	dirSync, e := os.Open(dir)
	if e != nil {
		return errors.New("INVALID_PACKAGE")
	}
	e = dirSync.Sync()
	if closeErr := dirSync.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return errors.New("INVALID_PACKAGE")
	}
	if err = authority.Rollback(ctx); err != nil {
		return errors.New("OPERATOR_REQUIRED")
	}
	if err = s.audit(ctx, actor, op, "backup.create.succeeded", "SUCCEEDED"); err != nil {
		return err
	}
	return nil
}

var targetRE = regexp.MustCompile(`^rehearsal_[a-z0-9][a-z0-9_]{0,49}$`)

func (s *Service) Rehearse(ctx context.Context, actor uuid.UUID, dir, target string) (err error) {
	if err = ValidateConnectionURL(s.sourceURL); err != nil {
		return err
	}
	if err = ValidateConnectionURL(s.restoreURL); err != nil {
		return err
	}
	op := uuid.New()
	defer func() {
		if err != nil {
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.audit(c, actor, op, "backup.rehearse.failed", err.Error())
		}
	}()
	if accounts.New(s.pool, nil, nil, accounts.Config{}).RequireOperator(ctx, actor) != nil {
		return errors.New("OPERATOR_REQUIRED")
	}
	if err = s.audit(ctx, actor, op, "backup.rehearse.started", "STARTED"); err != nil {
		return err
	}
	authority, err := s.authorize(ctx, actor)
	if err != nil {
		return err
	}
	defer rollback(authority)
	if !targetRE.MatchString(target) {
		return errors.New("INVALID_TARGET")
	}
	m, err := readPackage(dir)
	if err != nil {
		return err
	}
	sourceTx, e := s.pool.Begin(ctx)
	if e != nil {
		return errors.New("SCHEMA_MISMATCH")
	}
	schema, e := db.BackupSchemaState(ctx, sourceTx)
	if e != nil || schema != m.Schema {
		_ = sourceTx.Rollback(ctx)
		return errors.New("SCHEMA_MISMATCH")
	}
	major, e := pgMajor(ctx, sourceTx)
	_ = sourceTx.Rollback(ctx)
	if e != nil || major != m.PGMajor {
		return errors.New("SCHEMA_MISMATCH")
	}
	if s.restoreURL == "" {
		return errors.New("RESTORE_UNAVAILABLE")
	}
	cfg, e := pgx.ParseConfig(s.restoreURL)
	if e != nil {
		return errors.New("RESTORE_UNAVAILABLE")
	}
	admin, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		return errors.New("RESTORE_UNAVAILABLE")
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = admin.Close(c)
	}()
	var super, createdb, exists bool
	if admin.QueryRow(ctx, `SELECT rolsuper,rolcreatedb FROM pg_roles WHERE rolname=current_user`).Scan(&super, &createdb) != nil || super || !createdb {
		return errors.New("RESTORE_ROLE_REQUIRED")
	}
	adminMajor := 0
	if admin.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer / 10000`).Scan(&adminMajor) != nil || adminMajor != m.PGMajor {
		return errors.New("SCHEMA_MISMATCH")
	}
	if admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)`, target).Scan(&exists) != nil {
		return errors.New("RESTORE_UNAVAILABLE")
	}
	if exists {
		return errors.New("TARGET_EXISTS")
	}
	if _, e = admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{target}.Sanitize()+` TEMPLATE template0`); e != nil {
		return errors.New("RESTORE_CREATE_FAILED")
	}
	if err = runPG(ctx, s.restoreURL, "pg_restore", "RESTORE_FAILED", "--no-owner", "--no-privileges", "--single-transaction", "--exit-on-error", "--dbname="+target, filepath.Join(dir, "database.dump")); err != nil {
		return err
	}
	restored := cfg.Copy()
	restored.Database = target
	conn, e := pgx.ConnectConfig(ctx, restored)
	if e != nil {
		return errors.New("RESTORE_VERIFY_FAILED")
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(c)
	}()
	tx, e := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return errors.New("RESTORE_VERIFY_FAILED")
	}
	defer rollback(tx)
	if setInventorySession(ctx, tx) != nil {
		return errors.New("RESTORE_VERIFY_FAILED")
	}
	actualSchema, e := db.BackupSchemaState(ctx, tx)
	if e != nil || actualSchema.Version != m.Schema.Version || actualSchema.Migrations != m.Schema.Migrations || actualSchema.Portable != m.Schema.Portable {
		return errors.New("RESTORE_VERIFY_FAILED")
	}
	tables, e := inventoryTables(ctx, tx)
	if e != nil || !reflect.DeepEqual(tables, m.Tables) {
		return errors.New("RESTORE_VERIFY_FAILED")
	}
	sequences, e := inventorySequences(ctx, tx)
	if e != nil || !reflect.DeepEqual(sequences, m.Sequences) {
		return errors.New("RESTORE_VERIFY_FAILED")
	}
	attachments, e := support.BackupInventory(ctx, tx)
	if e != nil || !reflect.DeepEqual(attachments, m.Attachments) {
		return errors.New("RESTORE_VERIFY_FAILED")
	}
	if err = authority.Rollback(ctx); err != nil {
		return errors.New("OPERATOR_REQUIRED")
	}
	if err = s.audit(ctx, actor, op, "backup.rehearse.succeeded", "SUCCEEDED"); err != nil {
		return err
	}
	return nil
}

// ValidateConnectionURL limits both pgx and libpq to one explicit endpoint and
// the TLS settings that runPG transfers to its isolated environment.
func ValidateConnectionURL(connString string) error {
	_, _, err := connectionConfig(connString)
	return err
}

func connectionConfig(connString string) (*pgx.ConnConfig, []string, error) {
	bad := func() (*pgx.ConnConfig, []string, error) {
		return nil, nil, errors.New("INVALID_DATABASE_URL")
	}
	// pgx inherits these libpq-style settings, whereas the subprocess does not.
	// PGDATA, PG_MAJOR and PG_VERSION are deliberately absent: they do not
	// influence pgx connection selection.
	for _, key := range []string{
		"PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGPASSFILE",
		"PGAPPNAME", "PGCONNECT_TIMEOUT", "PGSSLMODE", "PGSSLKEY", "PGSSLCERT",
		"PGSSLSNI", "PGSSLROOTCERT", "PGSSLPASSWORD", "PGSSLNEGOTIATION",
		"PGTARGETSESSIONATTRS", "PGSERVICE", "PGSERVICEFILE", "PGTZ",
		"PGOPTIONS", "PGMINPROTOCOLVERSION", "PGMAXPROTOCOLVERSION",
		"PGCHANNELBINDING", "PGREQUIREAUTH",
	} {
		if os.Getenv(key) != "" {
			return bad()
		}
	}
	u, err := url.Parse(connString)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") ||
		u.Opaque != "" || u.Fragment != "" || u.Host == "" || strings.Contains(u.Host, ",") ||
		u.Hostname() == "" || u.User == nil || u.User.Username() == "" || len(u.Path) < 2 {
		return bad()
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return bad()
	}
	for key, values := range query {
		if len(values) != 1 || values[0] == "" {
			return bad()
		}
		switch key {
		case "sslmode", "sslrootcert", "sslcert", "sslkey":
		default:
			return bad()
		}
	}
	sslmode := query.Get("sslmode")
	if sslmode == "" {
		sslmode = "prefer"
	}
	switch sslmode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		return bad()
	}
	if query.Get("sslrootcert") == "system" && sslmode != "verify-full" {
		return bad()
	}
	if (sslmode == "verify-ca" || sslmode == "verify-full") && query.Get("sslrootcert") == "" {
		return bad()
	}
	// pgx also reads implicit TLS files from HOME. Reject an implicit client
	// credential or CA because the subprocess receives no HOME and would use
	// a different TLS configuration.
	if home, e := os.UserHomeDir(); e == nil {
		for _, item := range []struct{ key, name string }{
			{"sslrootcert", "root.crt"}, {"sslcert", "postgresql.crt"}, {"sslkey", "postgresql.key"},
		} {
			if query.Get(item.key) == "" {
				if _, e := os.Stat(filepath.Join(home, ".postgresql", item.name)); e == nil {
					return bad()
				}
			}
		}
	}
	cfg, err := pgx.ParseConfig(connString)
	if err != nil || cfg.Host != u.Hostname() || cfg.Database == "" || cfg.User == "" {
		return bad()
	}
	// Only whitelisted variables reach libpq; passwords never enter argv or logs.
	env := []string{"PATH=/usr/lib/postgresql/17/bin:/usr/bin:/bin", "PGHOST=" + cfg.Host, "PGPORT=" + strconv.Itoa(int(cfg.Port)), "PGUSER=" + cfg.User, "PGPASSWORD=" + cfg.Password, "PGDATABASE=" + cfg.Database, "PGCONNECT_TIMEOUT=10", "PGSSLMODE=" + sslmode}
	for _, item := range []struct{ key, variable string }{
		{"sslrootcert", "PGSSLROOTCERT"}, {"sslcert", "PGSSLCERT"}, {"sslkey", "PGSSLKEY"},
	} {
		if value := query.Get(item.key); value != "" {
			env = append(env, item.variable+"="+value)
		}
	}
	return cfg, env, nil
}

func runPG(ctx context.Context, connString, name, code string, args ...string) error {
	_, env, err := connectionConfig(connString)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if cmd.Run() != nil {
		return errors.New(code)
	}
	return nil
}
