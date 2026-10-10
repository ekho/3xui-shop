package tests

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/modules/operations"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The digest covers public rows, including River jobs and source provenance.
// Backup's own audit rows are excluded; workers must be stopped first.
func legacyDatabaseDigest(t *testing.T, pool *pgxpool.Pool) [32]byte {
	t.Helper()
	ctx := context.Background()
	tables, err := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename`)
	if err != nil {
		t.Fatal("public schema inventory unavailable")
	}
	var names []string
	for tables.Next() {
		var name string
		if tables.Scan(&name) != nil {
			t.Fatal("public schema inventory invalid")
		}
		names = append(names, name)
	}
	if tables.Err() != nil {
		t.Fatal("public schema inventory incomplete")
	}
	tables.Close()
	h := sha256.New()
	for _, name := range names {
		query := `SELECT to_jsonb(t)::text FROM ` + pgx.Identifier{name}.Sanitize() + ` t`
		if name == "audit_events" {
			query += ` WHERE action NOT LIKE 'backup.%'`
		}
		rows, err := pool.Query(ctx, query)
		if err != nil {
			t.Fatal("public table snapshot unavailable")
		}
		var values []string
		for rows.Next() {
			var value string
			if rows.Scan(&value) != nil {
				t.Fatal("public table snapshot invalid")
			}
			values = append(values, value)
		}
		if rows.Err() != nil {
			t.Fatal("public table snapshot incomplete")
		}
		rows.Close()
		sort.Strings(values)
		h.Write([]byte(name + "\n"))
		for _, value := range values {
			h.Write([]byte(value + "\n"))
		}
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

func legacySyntheticPacket(t *testing.T, root string) operations.LegacyPackage {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("synthetic fixture directory unavailable")
	}
	source := filepath.Join(dir, "synthetic.sqlite")
	fixture := exec.Command("python3", filepath.Join(root, "tests/fixtures/legacy_snapshot.py"), source)
	if err := fixture.Run(); err != nil {
		t.Fatal("synthetic SQLite fixture creation failed")
	}
	exporter := exec.Command("go", "run", "./cmd/server", "export-legacy", source,
		"--source", "synthetic-source", "--support-bot-id", "12345", "--support-group-id", "-10012345")
	exporter.Dir = filepath.Join(root, "backend")
	raw, err := exporter.Output()
	if err != nil {
		t.Fatal("private SQLite exporter failed")
	}
	var packet operations.LegacyPackage
	if json.Unmarshal(raw, &packet) != nil || operations.ValidateLegacyPackage(packet) != nil {
		t.Fatal("exported packet failed complete-package validation")
	}
	return packet
}

func TestPopulatedLegacyMigrationPreservesNativeFacts(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("synthetic Mini App signing key unavailable")
	}
	f, bot, plan := openMode(t, true, pub), &nativeBot{}, uuid.New()
	ctx := context.Background()
	f.cfg.Payments.StarsEnabled = true
	// Native catalogue uses a distinct devices value from the source plan.
	terms := wire.CataloguePlanSnapshot{PlanId: plan, Revision: 1, Devices: 3, TrafficGb: 15, Profile: "regular", Periods: []int64{30}, Prices: []wire.CataloguePrice{{PeriodDays: 30, Currency: "XTR", AmountMinor: "100"}}}
	rawTerms, _ := json.Marshal(terms)
	if _, err := f.env.Pool.Exec(ctx, `INSERT INTO catalogue_plans(id,current_revision,current_devices,current_profile,current_hidden) VALUES($1,1,3,'regular',false)`, plan); err != nil {
		t.Fatal("synthetic native plan unavailable")
	}
	if _, err := f.env.Pool.Exec(ctx, `INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,source,changed_at) VALUES($1,1,$2,false,'legacy_import',$3)`, plan, rawTerms, time.Now()); err != nil {
		t.Fatal("synthetic native catalogue revision unavailable")
	}
	f.cfg.Bonuses = bonuses.RewardConfig{Enabled: true, LevelOneDays: 10, LevelTwoDays: 3}
	f.cfg.ReferredTrial = bonuses.ReferredTrialConfig{Enabled: true, PeriodDays: 7}
	f.panel.mu.Lock()
	f.panel.loseReply = false
	f.panel.mu.Unlock()
	nativeRewardModules(t, f, key)
	runtime, stop := launchNative(t, f, bot, true, true, true)
	wait(t, runtime.StarsGateway().Ready)
	t.Cleanup(stop)

	ancestor, actorCSRF, actor := f.signupAccount(t, nativeEmail("migration-ancestor"))
	firstCode := nativeReferralCode(t, nativeReferralRead(t, f, ancestor))
	inviter, _, inviterID := f.signupAccount(t, nativeEmail("migration-inviter"), firstCode)
	secondCode := nativeReferralCode(t, nativeReferralRead(t, f, inviter))
	buyerTg := int64(uuid.New().ID()) + 1000000000
	buyer := nativeReferralMini(t, f, key, buyerTg, secondCode)
	_, funded := nativeStarsOrder(t, f, key, buyerTg, plan)
	bot.starsMoney(nativeStarsProof(buyerTg, funded.OrderId, "synthetic-migration-charge", time.Now()), false)
	wait(t, func() bool {
		var applied bool
		return f.env.Pool.QueryRow(ctx, `SELECT fulfillment_status='applied' FROM purchase_orders WHERE id=$1`, funded.OrderId).Scan(&applied) == nil && applied
	})
	assertNativeStarsPanel(t, f, funded.OrderId)
	wait(t, func() bool {
		var granted int
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM referrer_rewards WHERE source_order_id=$1 AND rewarded_at IS NOT NULL`, funded.OrderId).Scan(&granted) == nil && granted == 2
	})
	rewardFacts := nativeRewardFacts(t, f, funded.OrderId)
	if rewardFacts[0].Account != inviterID || rewardFacts[1].Account != actor || !rewardFacts[0].Granted || !rewardFacts[1].Granted {
		t.Fatal("two-level native reward delivery incomplete")
	}

	trialTg := int64(uuid.New().ID()) + 1000000000
	trial := nativeReferralMini(t, f, key, trialTg, secondCode)
	grant := nativeReferredActivate(t, f, trial, uuid.NewString(), 201)
	wait(t, func() bool { return applied(f, *grant.OperationId) })
	trialTarget := nativeReferredTarget(t, f, *grant.OperationId)
	panel := vpn.NewPanelClient(f.cfg.VPN.Panel)
	view, err := panel.GetClient(ctx, trialTarget.PanelKey)
	panel.Close()
	if err != nil || view == nil || view.VPNID != trialTarget.VPNID || view.SubID != trialTarget.SubID {
		t.Fatal("native referred trial panel readback failed")
	}
	stop()
	nativeRewardModules(t, f, key)
	_, stop = launchNative(t, f, bot, true, false, true)
	defer stop()
	undelivered := nativeReferralMini(t, f, key, int64(uuid.New().ID())+1000000000, secondCode)
	pendingTrial := nativeReferredActivate(t, f, undelivered, uuid.NewString(), 201)
	_, pendingOrder := nativeStarsOrder(t, f, key, int64(uuid.New().ID())+1000000000, plan)
	stop()
	if f.svc.Accounts.ChangeOperatorRole(ctx, actor, true) != nil {
		t.Fatal("synthetic operator role unavailable")
	}
	packet := legacySyntheticPacket(t, f.root)
	beforeImport := legacyDatabaseDigest(t, f.env.Pool)
	dry, err := f.svc.LegacyImport.Import(ctx, actor, packet, true)
	if err != nil {
		t.Fatal("populated dry-run failed", err)
	}
	if !dry.DryRun || dry.Replayed || legacyDatabaseDigest(t, f.env.Pool) != beforeImport {
		t.Fatal("populated dry-run changed PostgreSQL state")
	}
	if _, err = f.svc.LegacyImport.Import(ctx, buyer.Account.AccountId, packet, false); err == nil || legacyDatabaseDigest(t, f.env.Pool) != beforeImport {
		t.Fatal("nonoperator import accepted or changed state")
	}
	report, err := f.svc.LegacyImport.Import(ctx, actor, packet, false)
	if err != nil {
		t.Fatal("populated source import failed")
	}
	if report.Replayed || report.DryRun || report.Counts["users"] != len(packet.Users) || report.Counts["transactions"] != 4 ||
		report.PaymentStatuses["pending"] != 1 || report.PaymentStatuses["completed"] != 1 || report.PaymentStatuses["canceled"] != 1 || report.PaymentStatuses["refunded"] != 1 ||
		report.Totals.RewardDays != "10" || report.Totals.RewardMoneyUnknownCurrency != "2.250000000000000000" || report.Unknown["trial"] != 2 || report.Unknown["recurring"] != 3 {
		t.Fatal("populated reconciliation lost statuses, units, or unknown guards")
	}
	for _, source := range packet.Users {
		var id uuid.UUID
		var vpnID uuid.UUID
		var subID, panelKey string
		var tg, legacyID int64
		if f.env.Pool.QueryRow(ctx, `SELECT a.id,a.vpn_id,a.sub_id,a.panel_key,a.telegram_id,a.legacy_user_id FROM accounts a JOIN legacy_account_imports l ON l.account_id=a.id WHERE l.source_id=$1`, source.SourceID).Scan(&id, &vpnID, &subID, &panelKey, &tg, &legacyID) != nil ||
			vpnID.String() != source.VPNID || subID != source.SubID || panelKey != fmt.Sprint(source.TgID) || tg != source.TgID || legacyID != source.SourceID {
			t.Fatal("source account identity changed during import")
		}
		encoded, _ := json.Marshal(source)
		var same bool
		if f.env.Pool.QueryRow(ctx, `SELECT source_snapshot=$2::jsonb FROM legacy_account_imports WHERE source_id=$1`, source.SourceID, encoded).Scan(&same) != nil || !same {
			t.Fatal("source account snapshot lost exact fields or NULLs")
		}
		tx, err := f.env.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if err != nil {
			t.Fatal("source trial guard transaction unavailable")
		}
		trialState, err := accounts.LegacyTrialStateTx(ctx, tx, id)
		_ = tx.Rollback(ctx)
		wantTrial := "unknown"
		if source.IsTrialUsed != nil && *source.IsTrialUsed {
			wantTrial = "used"
		}
		if err != nil || trialState != wantTrial {
			t.Fatal("source trial guard promoted unknown or lost used fact")
		}
		starsState, err := f.svc.Payments.StarsSubscription(ctx, id)
		if err != nil || starsState.State != "legacy_unknown" {
			t.Fatal("source recurring guard promoted unknown billing state")
		}
		var orders, grants, accesses int
		if f.env.Pool.QueryRow(ctx, `SELECT
		 (SELECT count(*) FROM purchase_orders WHERE account_id=$1),
		 (SELECT count(*) FROM trial_grants WHERE account_id=$1),
		 (SELECT count(*) FROM access_operations WHERE account_id=$1)`, id).Scan(&orders, &grants, &accesses) != nil || orders != 0 || grants != 0 || accesses != 0 {
			t.Fatal("historical source created a native payment or access grant")
		}
	}
	for _, source := range packet.Stars {
		var charge *string
		var renew *bool
		var expires *int64
		if f.env.Pool.QueryRow(ctx, `SELECT stars_charge_id,is_stars_auto_renew,stars_expires_at FROM legacy_stars_imports WHERE source_legacy_user_id=$1`, source.SourceLegacyUserID).Scan(&charge, &renew, &expires) != nil ||
			!reflect.DeepEqual(charge, source.StarsChargeID) || !reflect.DeepEqual(renew, source.IsStarsAutoRenew) || !reflect.DeepEqual(expires, source.StarsExpiresAt) {
			t.Fatal("source recurring charge, NULL, or expiry lost")
		}
	}
	for _, source := range packet.Payments.Transactions {
		var tg int64
		var payment, subscription, status string
		var created, updated time.Time
		if f.env.Pool.QueryRow(ctx, `SELECT source_tg_id,source_payment_id,subscription,status,created_at,updated_at FROM legacy_payment_transactions WHERE source_id=$1`, source.SourceID).Scan(&tg, &payment, &subscription, &status, &created, &updated) != nil ||
			tg != source.SourceTgID || payment != source.PaymentID || subscription != source.Subscription || status != source.Status || !created.Equal(source.CreatedAt) || !updated.Equal(source.UpdatedAt) {
			t.Fatal("source payment ID, status, or microsecond time lost")
		}
	}
	for _, source := range packet.Bonuses.Rewards {
		wrapped, _ := json.Marshal(struct {
			Source string `json:"source"`
			Record any    `json:"record"`
		}{packet.Source, source})
		var same bool
		if f.env.Pool.QueryRow(ctx, `SELECT source_snapshot=$2::jsonb FROM legacy_bonus_imports WHERE kind='reward' AND source_id=$1`, source.SourceID, wrapped).Scan(&same) != nil || !same {
			t.Fatal("source reward units, level, actor, or status lost")
		}
	}
	for _, source := range packet.Audit.Events {
		var action string
		var created time.Time
		var actorType, actorName, eventSource *string
		var actorID, target *int64
		var payload []byte
		if f.env.Pool.QueryRow(ctx, `SELECT created_at,action,target_tg_id,actor_type,actor_id,actor_name,source,payload_json FROM legacy_audit_events WHERE source_id=$1`, source.SourceID).Scan(&created, &action, &target, &actorType, &actorID, &actorName, &eventSource, &payload) != nil ||
			!created.Equal(source.CreatedAt) || action != source.Action || !reflect.DeepEqual(target, source.TargetTgID) || !reflect.DeepEqual(actorType, source.ActorType) || !reflect.DeepEqual(actorID, source.ActorID) || !reflect.DeepEqual(actorName, source.ActorName) || !reflect.DeepEqual(eventSource, source.Source) ||
			(source.PayloadJSON == nil && payload != nil) || (source.PayloadJSON != nil && string(payload) != *source.PayloadJSON) {
			t.Fatal("source audit actor, NULL, raw payload, or time lost")
		}
	}
	var sourceRewards, sourcePayments, sourceAudit, nativeGrant, pendingGrant, nativePending int
	if f.env.Pool.QueryRow(ctx, `SELECT
	 (SELECT count(*) FROM legacy_bonus_imports WHERE kind='reward'),
	 (SELECT count(*) FROM legacy_payment_transactions),
	 (SELECT count(*) FROM legacy_audit_events),
	 (SELECT count(*) FROM trial_grants WHERE operation_id=$1 AND status='granted'),
	 (SELECT count(*) FROM trial_grants WHERE operation_id=$2 AND status='reserved'),
	 (SELECT count(*) FROM purchase_orders WHERE id=$3 AND payment_status='pending')`, grant.OperationId, pendingTrial.OperationId, pendingOrder.OrderId).Scan(&sourceRewards, &sourcePayments, &sourceAudit, &nativeGrant, &pendingGrant, &nativePending) != nil ||
		sourceRewards != len(packet.Bonuses.Rewards) || sourcePayments != 4 || sourceAudit != len(packet.Audit.Events) || nativeGrant != 1 || pendingGrant != 1 || nativePending != 1 {
		t.Fatal("import lost source history or populated native facts")
	}
	// A fresh native order arrives after migration and must survive every replay.
	nativeRewardModules(t, f, key)
	_, lateStop := launchNative(t, f, bot, true, false, true)
	defer lateStop()
	_, lateOrder := nativeStarsOrder(t, f, key, int64(uuid.New().ID())+1000000000, plan)
	lateStop()
	nativeRewardModules(t, f, key)
	setNativeMaintenance(t, f, ancestor, actorCSRF, true, 0, uuid.NewString())
	f.cfg.Payments.YooMoneyNotificationSecret = []byte("owned cutover receipt fixture")
	lateFields := url.Values{"notification_type": {"p2p-incoming"}, "operation_id": {"owned-cutover-receipt"},
		"amount": {"97.00"}, "currency": {"643"}, "datetime": {f.env.Clock().Format(time.RFC3339Nano)},
		"sender": {"owned-fixture-sender"}, "codepro": {"false"}, "label": {packet.Payments.Transactions[0].PaymentID}}
	proof := sha1.Sum([]byte(strings.Join([]string{lateFields.Get("notification_type"), lateFields.Get("operation_id"),
		lateFields.Get("amount"), lateFields.Get("currency"), lateFields.Get("datetime"), lateFields.Get("sender"),
		lateFields.Get("codepro"), string(f.cfg.Payments.YooMoneyNotificationSecret), lateFields.Get("label")}, "&")))
	lateFields.Set("sha1_hash", hex.EncodeToString(proof[:]))
	latePayment := func() {
		response, err := f.public.Client().PostForm(f.public.URL+"/yoomoney", lateFields)
		if err != nil {
			t.Fatal("late callback unavailable", err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatal("late callback rejected in maintenance", response.StatusCode)
		}
	}
	latePayment()
	latePayment()
	var receipts, invented int
	if f.env.Pool.QueryRow(ctx, `SELECT
	 (SELECT count(*) FROM legacy_payment_receipts WHERE provider='yoomoney' AND source_id='owned-cutover-receipt' AND amount_minor=9700 AND currency='RUB' AND state='review' AND source_transaction_id=$1),
	 (SELECT count(*) FROM purchase_orders WHERE account_id IN (SELECT account_id FROM legacy_payment_transactions))`, packet.Payments.Transactions[0].SourceID).Scan(&receipts, &invented) != nil || receipts != 1 || invented != 0 {
		t.Fatal("late verified money lost or synthetic funding created")
	}
	steady := legacyDatabaseDigest(t, f.env.Pool)
	replay, err := f.svc.LegacyImport.Import(ctx, actor, packet, false)
	if err != nil || !replay.Replayed || replay.SourceDigest != report.SourceDigest || legacyDatabaseDigest(t, f.env.Pool) != steady {
		t.Fatal("same-process replay changed populated schema")
	}
	nativeRewardModules(t, f, key)
	latePayment()
	replay, err = f.svc.LegacyImport.Import(ctx, actor, packet, false)
	if err != nil || !replay.Replayed || legacyDatabaseDigest(t, f.env.Pool) != steady || !reflect.DeepEqual(nativeRewardFacts(t, f, funded.OrderId), rewardFacts) {
		t.Fatal("application restart replay changed native/source facts")
	}
	var latePending int
	if f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM purchase_orders WHERE id=$1 AND payment_status='pending'`, lateOrder.OrderId).Scan(&latePending) != nil || latePending != 1 {
		t.Fatal("late native pending order lost on source replay")
	}
	legacyRestorePopulated(t, f, actor, steady)
}

func nativeReferredTarget(t *testing.T, f *fixture, id uuid.UUID) vpn.ProvisionTarget {
	t.Helper()
	var raw []byte
	var target vpn.ProvisionTarget
	if f.env.Pool.QueryRow(context.Background(), `SELECT target FROM trial_operations WHERE id=$1`, id).Scan(&raw) != nil || json.Unmarshal(raw, &target) != nil {
		t.Fatal("trial target unavailable")
	}
	return target
}

func legacyRestorePopulated(t *testing.T, f *fixture, actor uuid.UUID, want [32]byte) {
	t.Helper()
	ctx := context.Background()
	_ = controlledPostgresContainer(t, f.root, f.env.Pool.Config().ConnConfig)
	var meta postgresFixtureMetadata
	var err error
	if private := os.Getenv("TEST_POSTGRES_FIXTURE_FILE"); private != "" {
		meta, err = readPostgresFixtureMetadata(private)
	} else {
		compose := os.Getenv("TEST_POSTGRES_COMPOSE_FILE")
		if compose == "" {
			compose = filepath.Join(f.root, "deploy/acceptance/compose.test.yml")
		}
		var owned string
		owned, err = ownedPostgresComposeFile(compose)
		if err == nil {
			meta, err = composePostgresFixture(ctx, owned, f.env.Pool.Config().ConnConfig)
		}
	}
	if err != nil {
		t.Fatal("owned operations image metadata unavailable")
	}
	image := meta.Project + "-operations:local"
	if supplied := os.Getenv("TEST_OPERATIONS_IMAGE"); supplied != "" {
		if supplied != image && supplied != "cabinet-migration-operations:local" {
			t.Fatal("operations image is outside the owned acceptance tags")
		}
		image = supplied
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("private backup directory unavailable")
	}
	text, err := operations.ReadPrivateText(os.Getenv("TEST_DATABASE_URL_FILE"))
	if err != nil {
		t.Fatal("owned PostgreSQL fixture URL unavailable")
	}
	source, err := url.Parse(text)
	if err != nil || source.User == nil || source.User.Username() != "platform_test" {
		t.Fatal("owned PostgreSQL fixture URL invalid")
	}
	source.Host, source.Path, source.RawQuery = "postgres:5432", "/"+f.env.Pool.Config().ConnConfig.Database, "sslmode=disable"
	role := "migration_restore_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := f.env.Pool.Exec(ctx, `CREATE ROLE `+pgx.Identifier{role}.Sanitize()+` LOGIN CREATEDB NOSUPERUSER`); err != nil {
		t.Fatal("isolated restore role unavailable")
	}
	t.Cleanup(func() {
		if _, err := f.env.Pool.Exec(context.Background(), `DROP ROLE `+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Error("isolated restore role cleanup failed")
		}
	})
	restore := *source
	restore.Path = "/platform_test"
	restore.User = url.User(role)
	for name, value := range map[string]string{"source-url": source.String(), "restore-url": restore.String(), "operator": actor.String()} {
		if os.WriteFile(filepath.Join(dir, name), []byte(value), 0600) != nil {
			t.Fatal("private backup input unavailable")
		}
	}
	run := func(args ...string) {
		cmdArgs := []string{"run", "--rm", "--network", meta.Project + "_default", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--tmpfs", "/tmp:mode=1777", "-v", dir + ":/fixture", "-e", "DATABASE_URL_FILE=/fixture/source-url", "-e", "RESTORE_DATABASE_URL_FILE=/fixture/restore-url", image}
		cmd := exec.CommandContext(ctx, "docker", append(cmdArgs, args...)...)
		var output bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &output
		if cmd.Run() != nil {
			codes := regexp.MustCompile(`code=([A-Z_]+)`).FindAllSubmatch(output.Bytes(), -1)
			if len(codes) == 0 {
				t.Fatal("owned #45 backup operation failed without stable code", args[1])
			}
			t.Fatal("owned #45 backup operation failed", args[1], string(codes[0][1]))
		}
	}
	run("backup", "create", "--operator-file", "/fixture/operator", "--directory", "/fixture/backup")
	target := "rehearsal_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Cleanup(func() {
		_, _ = f.env.Pool.Exec(context.Background(), `DROP DATABASE `+pgx.Identifier{target}.Sanitize()+` WITH (FORCE)`)
	})
	run("backup", "rehearse", "--operator-file", "/fixture/operator", "--directory", "/fixture/backup", "--target", target)
	config := f.env.Pool.Config().Copy()
	config.ConnConfig.Database = target
	restored, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("isolated restored pool unavailable")
	}
	defer restored.Close()
	if legacyDatabaseDigest(t, restored) != want {
		t.Fatal("restored public schema lost populated source/native rows")
	}
}
