package observability

import (
	"context"
	"log/slog"

	"walnut-billing/internal/metrics"
	"walnut-billing/internal/service"
)

// CommerceObserver bridges provider-agnostic service observations to metrics and
// structured logs. It deliberately keeps high-cardinality IDs out of metric
// labels while retaining them in logs for incident investigation.
type CommerceObserver struct {
	logger *slog.Logger
}

var (
	_ service.CheckoutObserver          = (*CommerceObserver)(nil)
	_ service.PaymentEventObserver      = (*CommerceObserver)(nil)
	_ service.FulfillmentObserver       = (*CommerceObserver)(nil)
	_ service.PaymentAdjustmentObserver = (*CommerceObserver)(nil)
)

func NewCommerceObserver(logger *slog.Logger) *CommerceObserver {
	if logger == nil {
		logger = slog.Default()
	}
	return &CommerceObserver{logger: logger}
}

func (o *CommerceObserver) ObserveCheckout(ctx context.Context, observation service.CheckoutObservation) {
	metrics.RecordCommerceCheckout(observation.Provider, observation.SKUCode, observation.Status, observation.ErrorKind, observation.Duration)
	if observation.Blocked {
		metrics.RecordCheckoutPolicyBlock(observation.PolicyReason, defaultCheckoutPolicyAction(observation.PolicyAction))
	}
	o.log(ctx, logLevelForStatus(observation.Status), "commerce_checkout_observed",
		"provider", observation.Provider,
		"sku_code", observation.SKUCode,
		"user_id", observation.UserID,
		"out_trade_no", observation.OutTradeNo,
		"status", observation.Status,
		"error_kind", observation.ErrorKind,
		"blocked", observation.Blocked,
		"policy_reason", observation.PolicyReason,
		"policy_action", observation.PolicyAction,
		"duration_ms", observation.Duration.Milliseconds(),
	)
}

func (o *CommerceObserver) ObservePaymentEvent(ctx context.Context, observation service.PaymentEventObservation) {
	metrics.RecordPaymentEvent(observation.Operation, observation.Provider, observation.EventType, observation.InboxStatus, observation.ErrorKind, observation.Duration)
	o.log(ctx, logLevelForStatus(observation.InboxStatus), "payment_event_observed",
		"operation", observation.Operation,
		"provider", observation.Provider,
		"provider_event_id", observation.ProviderEventID,
		"event_type", observation.EventType,
		"out_trade_no", observation.OutTradeNo,
		"inbox_status", observation.InboxStatus,
		"processed", observation.Processed,
		"duplicate", observation.Duplicate,
		"attempts", observation.Attempts,
		"error_kind", observation.ErrorKind,
		"process_note", observation.ProcessNote,
		"duration_ms", observation.Duration.Milliseconds(),
	)
}

func (o *CommerceObserver) ObserveFulfillment(ctx context.Context, observation service.FulfillmentObservation) {
	metrics.RecordFulfillment(observation.SKUCode, observation.OrderType, observation.Status, observation.ErrorKind, observation.Duration)
	o.log(ctx, logLevelForStatus(observation.Status), "commerce_fulfillment_observed",
		"out_trade_no", observation.OutTradeNo,
		"user_id", observation.UserID,
		"sku_code", observation.SKUCode,
		"order_type", observation.OrderType,
		"status", observation.Status,
		"error_kind", observation.ErrorKind,
		"execution_count", observation.ExecutionCount,
		"already_fulfilled", observation.AlreadyFulfilled,
		"duration_ms", observation.Duration.Milliseconds(),
	)
}

func (o *CommerceObserver) ObservePaymentAdjustment(ctx context.Context, observation service.PaymentAdjustmentObservation) {
	metrics.RecordPaymentAdjustment(observation.EventType, observation.Status, observation.PolicyAction, observation.ErrorKind, observation.Duration)
	o.log(ctx, logLevelForStatus(observation.Status), "payment_adjustment_observed",
		"provider", observation.Provider,
		"provider_event_id", observation.ProviderEventID,
		"event_type", observation.EventType,
		"out_trade_no", observation.OutTradeNo,
		"status", observation.Status,
		"error_kind", observation.ErrorKind,
		"policy_action", observation.PolicyAction,
		"policy_reason", observation.PolicyReason,
		"risk_flag_created", observation.RiskFlagCreated,
		"revoked_grant_count", observation.RevokedGrantCount,
		"clawback_credits", observation.ClawbackCredits,
		"duration_ms", observation.Duration.Milliseconds(),
	)
}

func (o *CommerceObserver) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	logger := o.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Log(ctx, level, msg, args...)
}

func logLevelForStatus(status string) slog.Level {
	switch status {
	case service.ObservationStatusFailed:
		return slog.LevelError
	case service.ObservationStatusBlocked,
		service.ObservationStatusIgnored,
		"review_required",
		"policy_rejected":
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

func defaultCheckoutPolicyAction(action string) string {
	if action == "" {
		return "unknown"
	}
	return action
}
