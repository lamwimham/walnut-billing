package bootstrap

import (
	"net/url"
	"strings"
	"testing"
	"walnut-billing/internal/config"
	"walnut-billing/internal/service"
)

func TestBuildCloudObjectStorageProviderBuildsS3CompatibleProvider(t *testing.T) {
	provider, err := buildCloudObjectStorageProvider(&config.Config{CloudStorage: config.CloudStorageConfig{
		Provider:                 "r2",
		EndpointURL:              "https://account.r2.cloudflarestorage.com",
		Region:                   "auto",
		Bucket:                   "walnut-sync",
		AccessKeyID:              "access-key",
		SecretAccessKey:          "secret-key",
		ObjectTagging:            true,
		UploadTargetTTLSeconds:   600,
		DownloadTargetTTLSeconds: 300,
		OperationTTLSeconds:      30,
	}})
	if err != nil {
		t.Fatalf("build cloud provider: %v", err)
	}
	if provider.ProviderID() != "r2" {
		t.Fatalf("expected r2 provider, got %s", provider.ProviderID())
	}
	target, err := provider.BuildDownloadTarget(t.Context(), service.CloudObjectDownloadRequest{ObjectKey: "accounts/usr_1/file.txt"})
	if err != nil {
		t.Fatalf("build download target: %v", err)
	}
	if target.Provider != "r2" || !strings.Contains(target.DownloadURL, "X-Amz-Signature=") {
		t.Fatalf("unexpected target: %#v", target)
	}
	parsed, err := url.Parse(target.DownloadURL)
	if err != nil {
		t.Fatalf("parse download target: %v", err)
	}
	if parsed.Host != "account.r2.cloudflarestorage.com" || !strings.HasPrefix(parsed.EscapedPath(), "/walnut-sync/accounts/usr_1/") {
		t.Fatalf("expected R2 path-style target, got %s", target.DownloadURL)
	}
}

func TestBuildCloudObjectStorageProviderDefaultsToUnconfigured(t *testing.T) {
	provider, err := buildCloudObjectStorageProvider(&config.Config{})
	if err != nil {
		t.Fatalf("build cloud provider: %v", err)
	}
	if provider.ProviderID() != "unconfigured" {
		t.Fatalf("expected unconfigured provider, got %s", provider.ProviderID())
	}
}

func TestBuildCloudObjectStorageProviderRejectsUnsupportedProvider(t *testing.T) {
	_, err := buildCloudObjectStorageProvider(&config.Config{CloudStorage: config.CloudStorageConfig{Provider: "future-provider"}})
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("expected unsupported provider error, got %v", err)
	}
}
