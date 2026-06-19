package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"walnut-billing/internal/service"
)

func TestOperationsObserverLogsSnapshotWithoutRawDeviceID(t *testing.T) {
	var out bytes.Buffer
	observer := NewOperationsObserver(slog.New(slog.NewTextHandler(&out, nil)))

	observer.ObserveAccessSnapshot(context.Background(), service.AccessSnapshotObservation{
		UserID:         "usr_1",
		DevicePresent:  true,
		DeviceStatus:   "active",
		Status:         service.ObservationStatusFailed,
		ErrorKind:      "signature_error",
		SignatureKeyID: "prod-key",
		SignatureAlg:   "Ed25519",
		Duration:       time.Millisecond,
	})

	logLine := out.String()
	if !strings.Contains(logLine, "access_snapshot_observed") || !strings.Contains(logLine, "signature_error") || !strings.Contains(logLine, "device_present=true") {
		t.Fatalf("expected snapshot observation log, got %s", logLine)
	}
	if strings.Contains(logLine, "raw-device") || strings.Contains(logLine, "device_id") {
		t.Fatalf("snapshot observation must not log raw device id: %s", logLine)
	}
}

func TestOperationsObserverLogsCloudQuotaFieldsWithoutObjectKey(t *testing.T) {
	var out bytes.Buffer
	observer := NewOperationsObserver(slog.New(slog.NewTextHandler(&out, nil)))

	observer.ObserveCloudSync(context.Background(), service.CloudSyncObservation{
		Operation:      "authorize_sync",
		Provider:       "r2",
		UserID:         "usr_1",
		Status:         service.ObservationStatusBlocked,
		ErrorKind:      "over_quota",
		RequestedBytes: 1200,
		UsedBytes:      900,
		QuotaBytes:     1000,
		OverQuota:      true,
		Duration:       time.Millisecond,
	})

	logLine := out.String()
	if !strings.Contains(logLine, "cloud_sync_observed") || !strings.Contains(logLine, "over_quota") || !strings.Contains(logLine, "requested_bytes=1200") {
		t.Fatalf("expected cloud sync observation log, got %s", logLine)
	}
	if strings.Contains(logLine, "object_key") || strings.Contains(logLine, "upload_url") || strings.Contains(logLine, "sha256:manifest") {
		t.Fatalf("cloud observation must not log object keys or raw manifests: %s", logLine)
	}
}
