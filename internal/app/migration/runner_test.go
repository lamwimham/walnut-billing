package migration

import (
	"context"
	"strings"
	"testing"

	"walnut-billing/internal/domain"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRunVersionedAppliesBaselineAndRecordsMetadata(t *testing.T) {
	db := openMigrationTestDB(t)

	if err := Run(context.Background(), db, Options{Mode: ModeVersioned}); err != nil {
		t.Fatalf("run versioned migration: %v", err)
	}
	if !db.Migrator().HasTable(&domain.Order{}) {
		t.Fatalf("expected baseline domain schema to be created")
	}

	var records []SchemaMigration
	if err := db.Order("version").Find(&records).Error; err != nil {
		t.Fatalf("list schema migrations: %v", err)
	}
	if len(records) != 1 || records[0].Version != "202606190001" || records[0].Name != "baseline_control_plane_schema" || records[0].Checksum == "" {
		t.Fatalf("unexpected migration metadata: %#v", records)
	}

	if err := Run(context.Background(), db, Options{Mode: ModeVersioned}); err != nil {
		t.Fatalf("rerun versioned migration: %v", err)
	}
	var count int64
	if err := db.Model(&SchemaMigration{}).Count(&count).Error; err != nil {
		t.Fatalf("count schema migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected idempotent migration metadata, got %d records", count)
	}
}

func TestRunAutoKeepsDevelopmentAutoMigratePath(t *testing.T) {
	db := openMigrationTestDB(t)

	if err := Run(context.Background(), db, Options{Mode: ModeAuto}); err != nil {
		t.Fatalf("run auto migration: %v", err)
	}
	if !db.Migrator().HasTable(&domain.Product{}) {
		t.Fatalf("expected auto migration to create domain tables")
	}
	if db.Migrator().HasTable(&SchemaMigration{}) {
		t.Fatalf("auto mode must not write version metadata")
	}
}

func TestRunDisabledDoesNotMutateSchema(t *testing.T) {
	db := openMigrationTestDB(t)

	if err := Run(context.Background(), db, Options{Mode: ModeDisabled}); err != nil {
		t.Fatalf("run disabled migration: %v", err)
	}
	if db.Migrator().HasTable(&SchemaMigration{}) || db.Migrator().HasTable(&domain.Order{}) {
		t.Fatalf("disabled migration mode must not create tables")
	}
}

func TestRunnerRejectsDuplicateMigrationVersions(t *testing.T) {
	db := openMigrationTestDB(t)
	runner := NewRunner(db, []Migration{
		{Version: "202606190001", Name: "first", Up: noopMigration},
		{Version: "202606190001", Name: "duplicate", Up: noopMigration},
	})

	err := runner.Run(context.Background(), Options{Mode: ModeVersioned})
	if err == nil || !strings.Contains(err.Error(), "duplicate schema migration version") {
		t.Fatalf("expected duplicate version error, got %v", err)
	}
}

func TestRunnerRejectsChangedAppliedMigrationChecksum(t *testing.T) {
	db := openMigrationTestDB(t)
	first := NewRunner(db, []Migration{{Version: "202606190001", Name: "baseline", Up: noopMigration}})
	if err := first.Run(context.Background(), Options{Mode: ModeVersioned}); err != nil {
		t.Fatalf("apply initial migration: %v", err)
	}

	changed := NewRunner(db, []Migration{{Version: "202606190001", Name: "changed_name", Up: noopMigration}})
	err := changed.Run(context.Background(), Options{Mode: ModeVersioned})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func noopMigration(context.Context, *gorm.DB) error { return nil }

func openMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	return db
}
