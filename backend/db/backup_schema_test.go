package db_test

import (
	"context"
	"testing"

	"example.com/cabinet/backend/db"
	"example.com/cabinet/backend/internal/testkit"
)

func TestBackupSchemaDetectsLiveTriggerDrift(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	read := func() db.BackupSchema {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		state, err := db.BackupSchemaState(ctx, tx)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := read()
	if _, err := e.Pool.Exec(ctx, `CREATE FUNCTION backup_probe() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `CREATE TRIGGER backup_probe_trigger BEFORE INSERT ON accounts FOR EACH ROW EXECUTE FUNCTION backup_probe()`); err != nil {
		t.Fatal(err)
	}
	if after := read(); after.Structure == before.Structure {
		t.Fatal("trigger drift was not detected")
	}
}

func TestBackupSchemaRejectsUninventoriedSchema(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	if _, err := e.Pool.Exec(ctx, `CREATE SCHEMA backup_extra`); err != nil {
		t.Fatal(err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := db.BackupSchemaState(ctx, tx); err == nil {
		t.Fatal("private schema accepted without inventory")
	}
}

func TestBackupSchemaRejectsMissingAppliedMigration(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	if _, err := e.Pool.Exec(ctx, `UPDATE goose_db_version SET is_applied=false WHERE version_id=5`); err != nil {
		t.Fatal(err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := db.BackupSchemaState(ctx, tx); err == nil {
		t.Fatal("missing applied migration accepted because latest version remained")
	}
}

func TestBackupSchemaPortableKeepsBooleanGrouping(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	read := func() string {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		state, err := db.BackupSchemaState(ctx, tx)
		if err != nil {
			t.Fatal(err)
		}
		return state.Portable
	}
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE accounts ADD CONSTRAINT backup_boolean_group_check CHECK ((locale='ru' AND kind='web') OR verified_at IS NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	first := read()
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE accounts DROP CONSTRAINT backup_boolean_group_check`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE accounts ADD CONSTRAINT backup_boolean_group_check CHECK (locale='ru' AND (kind='web' OR verified_at IS NOT NULL))`); err != nil {
		t.Fatal(err)
	}
	if second := read(); first == second {
		t.Fatal("different boolean grouping had the same portable schema hash")
	}
}

func TestBackupSchemaDetectsImmutableTriggerDisabled(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	read := func() db.BackupSchema {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		state, err := db.BackupSchemaState(ctx, tx)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := read()
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE catalogue_revisions DISABLE TRIGGER catalogue_revision_immutable`); err != nil {
		t.Fatal(err)
	}
	disabled := read()
	if disabled.Structure == before.Structure || disabled.Portable == before.Portable {
		t.Fatal("disabled immutable trigger was not detected")
	}
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE catalogue_revisions ENABLE TRIGGER catalogue_revision_immutable`); err != nil {
		t.Fatal(err)
	}
	if restored := read(); restored != before {
		t.Fatal("enabled trigger did not restore original fingerprint")
	}
}

func TestBackupSchemaDetectsRiverEnumLabelsAndOrder(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	read := func() db.BackupSchema {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		state, err := db.BackupSchemaState(ctx, tx)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := read()
	if _, err := e.Pool.Exec(ctx, `ALTER TYPE river_job_state RENAME VALUE 'running' TO 'leased'`); err != nil {
		t.Fatal(err)
	}
	renamed := read()
	if renamed.Structure == before.Structure || renamed.Portable == before.Portable {
		t.Fatal("River enum label drift was not detected")
	}
	if _, err := e.Pool.Exec(ctx, `ALTER TYPE river_job_state ADD VALUE 'queued_after' BEFORE 'scheduled'`); err != nil {
		t.Fatal(err)
	}
	reordered := read()
	if reordered.Structure == renamed.Structure || reordered.Portable == renamed.Portable {
		t.Fatal("River enum ordered-label drift was not detected")
	}
}
