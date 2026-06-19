package observability

import (
	"context"
	"log/slog"

	"walnut-billing/internal/metrics"
	"walnut-billing/internal/service"
)

// OperationsObserver bridges cross-module operational observations to metrics
// and structured logs without coupling access/cloud services to commerce rules.
type OperationsObserver struct {
	logger *slog.Logger
}

var (
	_ service.SubscriptionActionObserver = (*OperationsObserver)(nil)
	_ service.CloudSyncObserver          = (*OperationsObserver)(nil)
	_ service.AccessSnapshotObserver     = (*OperationsObserver)(nil)
)

func NewOperationsObserver(logger *slog.Logger) *OperationsObserver {
	if logger == nil {
		logger = slog.Default()
	}
	return &OperationsObserver{logger: logger}
}

func (o *OperationsObserver) ObserveSubscriptionAction(ctx context.Context, observation service.SubscriptionActionObservation) {
	metrics.RecordSubscriptionAction(observation.Operation, observation.SKUCode, observation.Status, observation.ErrorKind, observation.Duration)
	o.log(ctx, logLevelForStatus(observation.Status), "subscription_action_observed",
		"operation", observation.Operation,
		"user_id", observation.UserID,
		"sku_code", observation.SKUCode,
		"status", observation.Status,
		"error_kind", observation.ErrorKind,
		"cancel_at_period_end", observation.CancelAtPeriodEnd,
		"current_period_ends_at", observation.CurrentPeriodEndsAt,
		"duration_ms", observation.Duration.Milliseconds(),
	)
}

func (o *OperationsObserver) ObserveCloudSync(ctx context.Context, observation service.CloudSyncObservation) {
	metrics.RecordCloudSync(observation.Operation, observation.Provider, observation.Status, observation.ErrorKind, observation.Duration)
	o.log(ctx, logLevelForStatus(observation.Status), "cloud_sync_observed",
		"operation", observation.Operation,
		"provider", observation.Provider,
		"user_id", observation.UserID,
		"client_project_id", observation.ClientProjectID,
		"cloud_project_id", observation.CloudProjectID,
		"status", observation.Status,
		"error_kind", observation.ErrorKind,
		"requested_bytes", observation.RequestedBytes,
		"used_bytes", observation.UsedBytes,
		"quota_bytes", observation.QuotaBytes,
		"over_quota", observation.OverQuota,
		"duration_ms", observation.Duration.Milliseconds(),
	)
}

func (o *OperationsObserver) ObserveAccessSnapshot(ctx context.Context, observation service.AccessSnapshotObservation) {
	metrics.RecordAccessSnapshot(observation.Status, observation.ErrorKind, observation.Duration)
	o.log(ctx, logLevelForStatus(observation.Status), "access_snapshot_observed",
		"user_id", observation.UserID,
		"device_present", observation.DevicePresent,
		"device_status", observation.DeviceStatus,
		"status", observation.Status,
		"error_kind", observation.ErrorKind,
		"signature_key_id", observation.SignatureKeyID,
		"signature_alg", observation.SignatureAlg,
		"license_state", observation.LicenseState,
		"duration_ms", observation.Duration.Milliseconds(),
	)
}

func (o *OperationsObserver) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	logger := o.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Log(ctx, level, msg, args...)
}
