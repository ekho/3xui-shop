package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// BackupSchema identifies the embedded migration set and the live public schema.
type BackupSchema struct {
	Version    int64  `json:"version"`
	Migrations string `json:"migrations_sha256"`
	Structure  string `json:"structure_sha256"`
	Portable   string `json:"portable_structure_sha256"`
}

func BackupSchemaState(ctx context.Context, tx pgx.Tx) (BackupSchema, error) {
	var unsupported bool
	if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname NOT IN ('public','information_schema') AND left(nspname,3)<>'pg_') OR EXISTS(SELECT 1 FROM pg_largeobject_metadata)`).Scan(&unsupported) != nil || unsupported {
		return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil || len(files) == 0 {
		return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
	}
	h := sha256.New()
	for _, name := range files {
		b, e := migrations.ReadFile(name)
		if e != nil {
			return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	latestName := strings.TrimPrefix(files[len(files)-1], "migrations/")
	latest, err := strconv.ParseInt(strings.SplitN(latestName, "_", 2)[0], 10, 64)
	if err != nil {
		return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
	}
	var version int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(version_id),0) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != latest {
		return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (version_id) version_id,is_applied FROM goose_db_version WHERE version_id>0 ORDER BY version_id,id DESC`)
	if err != nil {
		return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
	}
	applied := make(map[int64]bool)
	for rows.Next() {
		var id int64
		var enabled bool
		if rows.Scan(&id, &enabled) != nil {
			rows.Close()
			return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
		}
		applied[id] = enabled
	}
	if rows.Err() != nil {
		rows.Close()
		return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
	}
	rows.Close()
	if len(applied) != len(files) {
		return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
	}
	for _, name := range files {
		id, e := strconv.ParseInt(strings.SplitN(strings.TrimPrefix(name, "migrations/"), "_", 2)[0], 10, 64)
		if e != nil || !applied[id] {
			return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
		}
	}
	state := BackupSchema{Version: version, Migrations: hex.EncodeToString(h.Sum(nil))}
	// Catalog definitions catch drift that a matching goose marker would miss.
	prettyRows, err := tx.Query(ctx, `SELECT c.relname||'.'||co.conname,pg_get_constraintdef(co.oid,true) FROM pg_constraint co JOIN pg_class c ON c.oid=co.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public'`)
	if err != nil {
		return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
	}
	prettyConstraints := make(map[string]string)
	for prettyRows.Next() {
		var name, definition string
		if prettyRows.Scan(&name, &definition) != nil {
			prettyRows.Close()
			return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
		}
		prettyConstraints[name] = definition
	}
	if prettyRows.Err() != nil {
		prettyRows.Close()
		return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
	}
	prettyRows.Close()
	rows, err = tx.Query(ctx, `SELECT kind, name, definition FROM (
	 SELECT 'column' AS kind, c.relname||'.'||a.attname AS name,
	   format_type(a.atttypid,a.atttypmod)||':'||a.attnotnull||':'||a.attidentity::text||':'||COALESCE(pg_get_expr(d.adbin,d.adrelid),'') AS definition
	 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
	 LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum WHERE n.nspname='public' AND c.relkind IN ('r','p','S')
	 UNION ALL SELECT 'constraint', c.relname||'.'||co.conname, pg_get_constraintdef(co.oid)
	 FROM pg_constraint co JOIN pg_class c ON c.oid=co.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public'
	 UNION ALL SELECT 'index', i.relname, pg_get_indexdef(i.oid)
	 FROM pg_class i JOIN pg_namespace n ON n.oid=i.relnamespace WHERE n.nspname='public' AND i.relkind='i'
	 UNION ALL SELECT 'function', p.proname||':'||pg_get_function_identity_arguments(p.oid), pg_get_functiondef(p.oid)
	 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND p.prokind IN ('f','p')
	 UNION ALL SELECT 'trigger', c.relname||'.'||t.tgname, pg_get_triggerdef(t.oid)||':'||t.tgenabled::text
	 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND NOT t.tgisinternal
	 UNION ALL SELECT 'enum', t.typname, json_agg(e.enumlabel ORDER BY e.enumsortorder)::text
	 FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_enum e ON e.enumtypid=t.oid WHERE n.nspname='public' GROUP BY t.typname
	 UNION ALL SELECT 'view', c.relname, pg_get_viewdef(c.oid,true)
	 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('v','m')
	 UNION ALL SELECT 'sequence', c.relname, s.seqtypid::regtype::text||':'||s.seqstart||':'||s.seqincrement||':'||s.seqmax||':'||s.seqmin||':'||s.seqcache||':'||s.seqcycle
	 FROM pg_sequence s JOIN pg_class c ON c.oid=s.seqrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public'
	 UNION ALL SELECT 'table_flags', c.relname, c.relkind::text||':'||c.relrowsecurity||':'||c.relforcerowsecurity
	 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p')
	) s ORDER BY kind,name`)
	if err != nil {
		return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
	}
	defer rows.Close()
	structure := sha256.New()
	portable := sha256.New()
	for rows.Next() {
		var kind, name, definition string
		if rows.Scan(&kind, &name, &definition) != nil {
			return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
		}
		structure.Write([]byte(kind))
		structure.Write([]byte{0})
		structure.Write([]byte(name))
		structure.Write([]byte{0})
		structure.Write([]byte(definition))
		structure.Write([]byte{0})
		portable.Write([]byte(kind))
		portable.Write([]byte{0})
		portable.Write([]byte(name))
		portable.Write([]byte{0})
		if kind == "constraint" {
			definition = prettyConstraints[name]
			if definition == "" {
				return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
			}
		}
		portable.Write([]byte(definition))
		portable.Write([]byte{0})
	}
	if rows.Err() != nil {
		return BackupSchema{}, errors.New("SCHEMA_MISMATCH")
	}
	state.Structure = hex.EncodeToString(structure.Sum(nil))
	state.Portable = hex.EncodeToString(portable.Sum(nil))
	return state, nil
}
