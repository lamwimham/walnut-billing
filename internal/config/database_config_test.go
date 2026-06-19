package config

import "testing"

func TestLoadReadsDatabaseMigrationModeEnvConfig(t *testing.T) {
	t.Setenv("DATABASE_MIGRATION_MODE", "versioned")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Database.MigrationMode != DatabaseMigrationModeVersioned {
		t.Fatalf("unexpected migration mode: %#v", cfg.Database)
	}
}
