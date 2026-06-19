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
	t.Setenv("ACCESS_CLOUD_STORAGE_TRIAL_QUOTA_MB", "256")
	t.Setenv("ACCESS_CLOUD_STORAGE_MONTHLY_QUOTA_MB", "4096")
	t.Setenv("ACCESS_CLOUD_STORAGE_LIFETIME_QUOTA_MB", "8192")
	t.Setenv("ACCESS_TRIAL_DURATION_DAYS", "21")
	t.Setenv("ACCESS_LOGIN_CHALLENGE_TTL_SECONDS", "300")
	t.Setenv("ACCESS_LOGIN_CHALLENGE_MAX_ATTEMPTS", "3")
	t.Setenv("ACCESS_LOGIN_CHALLENGE_RATE_LIMIT_WINDOW_SECONDS", "900")
	t.Setenv("ACCESS_LOGIN_CHALLENGE_MAX_CREATES_PER_EMAIL", "4")
	t.Setenv("ACCESS_LOGIN_CHALLENGE_MAX_CREATES_PER_IP", "30")
	t.Setenv("ACCESS_LOGIN_CHALLENGE_DELIVERY", "email")
	t.Setenv("ACCESS_LOGIN_CHALLENGE_SECRET", "login-secret")
	t.Setenv("CLOUD_STORAGE_PROVIDER", "r2")
	t.Setenv("CLOUD_STORAGE_ENDPOINT_URL", "https://account.r2.cloudflarestorage.com")
	t.Setenv("CLOUD_STORAGE_REGION", "auto")
	t.Setenv("CLOUD_STORAGE_BUCKET", "walnut-sync")
	t.Setenv("CLOUD_STORAGE_ACCESS_KEY_ID", "access-key")
	t.Setenv("CLOUD_STORAGE_SECRET_ACCESS_KEY", "secret-key")
	t.Setenv("CLOUD_STORAGE_SESSION_TOKEN", "session-token")
	t.Setenv("CLOUD_STORAGE_FORCE_PATH_STYLE", "true")
	t.Setenv("CLOUD_STORAGE_OBJECT_TAGGING", "true")
	t.Setenv("CLOUD_STORAGE_UPLOAD_TARGET_TTL_SECONDS", "600")
	t.Setenv("CLOUD_STORAGE_DOWNLOAD_TARGET_TTL_SECONDS", "300")
	t.Setenv("CLOUD_STORAGE_OPERATION_TTL_SECONDS", "30")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Access.SnapshotSignatureAlgorithm != "Ed25519" || cfg.Access.SnapshotSecret != "prod-secret" || cfg.Access.SnapshotPrivateKey != "private-key" || cfg.Access.SnapshotKeyID != "kid-2026-06" {
		t.Fatalf("unexpected snapshot signer config: %#v", cfg.Access)
	}
	if cfg.Access.SnapshotTTLSeconds != 600 || cfg.Access.SnapshotOfflineGraceSeconds != 3600 || cfg.Access.MaxDevices != 4 || cfg.Access.CloudStorageQuotaMB != 2048 || cfg.Access.CloudStorageTrialQuotaMB != 256 || cfg.Access.CloudStorageMonthlyQuotaMB != 4096 || cfg.Access.CloudStorageLifetimeQuotaMB != 8192 || cfg.Access.TrialDurationDays != 21 {
		t.Fatalf("unexpected access config: %#v", cfg.Access)
	}
	if cfg.Access.LoginChallengeTTLSeconds != 300 || cfg.Access.LoginChallengeMaxAttempts != 3 || cfg.Access.LoginChallengeRateLimitWindowSeconds != 900 || cfg.Access.LoginChallengeMaxCreatesPerEmail != 4 || cfg.Access.LoginChallengeMaxCreatesPerIP != 30 || cfg.Access.LoginChallengeDelivery != "email" || cfg.Access.LoginChallengeSecret != "login-secret" {
		t.Fatalf("unexpected login challenge config: %#v", cfg.Access)
	}
	if cfg.CloudStorage.Provider != "r2" || cfg.CloudStorage.EndpointURL != "https://account.r2.cloudflarestorage.com" || cfg.CloudStorage.Region != "auto" || cfg.CloudStorage.Bucket != "walnut-sync" || cfg.CloudStorage.AccessKeyID != "access-key" || cfg.CloudStorage.SecretAccessKey != "secret-key" || cfg.CloudStorage.SessionToken != "session-token" {
		t.Fatalf("unexpected cloud storage config: %#v", cfg.CloudStorage)
	}
	if !cfg.CloudStorage.ForcePathStyle || !cfg.CloudStorage.ObjectTagging || cfg.CloudStorage.UploadTargetTTLSeconds != 600 || cfg.CloudStorage.DownloadTargetTTLSeconds != 300 || cfg.CloudStorage.OperationTTLSeconds != 30 {
		t.Fatalf("unexpected cloud storage config: %#v", cfg.CloudStorage)
	}
}
