package config

import "testing"

func TestLoadReadsAccessEnvConfig(t *testing.T) {
	t.Setenv("ACCESS_SNAPSHOT_SIGNATURE_ALGORITHM", "Ed25519")
	t.Setenv("ACCESS_SNAPSHOT_SECRET", "prod-secret")
	t.Setenv("ACCESS_SNAPSHOT_PRIVATE_KEY", "private-key")
	t.Setenv("ACCESS_SNAPSHOT_KEY_ID", "kid-2026-06")
	t.Setenv("ACCESS_SNAPSHOT_TTL_SECONDS", "600")
	t.Setenv("ACCESS_SNAPSHOT_OFFLINE_GRACE_SECONDS", "3600")
	t.Setenv("ACCESS_MAX_DEVICES", "4")
	t.Setenv("ACCESS_CLOUD_STORAGE_QUOTA_MB", "2048")
	t.Setenv("ACCESS_TRIAL_DURATION_DAYS", "21")
	t.Setenv("CLOUD_STORAGE_PROVIDER", "future-provider")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Access.SnapshotSignatureAlgorithm != "Ed25519" || cfg.Access.SnapshotSecret != "prod-secret" || cfg.Access.SnapshotPrivateKey != "private-key" || cfg.Access.SnapshotKeyID != "kid-2026-06" {
		t.Fatalf("unexpected snapshot signer config: %#v", cfg.Access)
	}
	if cfg.Access.SnapshotTTLSeconds != 600 || cfg.Access.SnapshotOfflineGraceSeconds != 3600 || cfg.Access.MaxDevices != 4 || cfg.Access.CloudStorageQuotaMB != 2048 || cfg.Access.TrialDurationDays != 21 {
		t.Fatalf("unexpected access config: %#v", cfg.Access)
	}
	if cfg.CloudStorage.Provider != "future-provider" {
		t.Fatalf("unexpected cloud storage config: %#v", cfg.CloudStorage)
	}
}
