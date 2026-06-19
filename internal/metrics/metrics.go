package metrics

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// HTTPRequestsTotal counts total HTTP requests by method, path, and status.
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	// HTTPRequestDuration tracks request latency histogram.
	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	// LicenseActivationsTotal counts license activations.
	LicenseActivationsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "license_activations_total",
			Help: "Total number of license activations",
		},
	)

	// LicenseVerificationsTotal counts license verifications.
	LicenseVerificationsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "license_verifications_total",
			Help: "Total number of license verification attempts",
		},
	)

	// OrdersCreatedTotal counts orders created.
	OrdersCreatedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "orders_created_total",
			Help: "Total number of orders created",
		},
	)

	// PaymentCallbacksTotal counts payment webhook callbacks.
	PaymentCallbacksTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_callbacks_total",
			Help: "Total number of payment provider callbacks",
		},
		[]string{"provider", "status"},
	)

	CommerceCheckoutsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "commerce_checkouts_total",
			Help: "Total number of commerce checkout attempts",
		},
		[]string{"provider", "sku_code", "status", "error_kind"},
	)

	CommerceCheckoutDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "commerce_checkout_duration_seconds",
			Help:    "Commerce checkout orchestration duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"provider", "sku_code", "status"},
	)

	CheckoutPolicyBlocksTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "checkout_policy_blocks_total",
			Help: "Total number of checkout attempts blocked by policy",
		},
		[]string{"reason", "action"},
	)

	PaymentEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_events_total",
			Help: "Total number of provider-agnostic payment events processed by the inbox",
		},
		[]string{"operation", "provider", "event_type", "status", "error_kind"},
	)

	PaymentEventDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "payment_event_duration_seconds",
			Help:    "Payment event receive/reprocess duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation", "provider", "event_type", "status"},
	)

	FulfillmentsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fulfillments_total",
			Help: "Total number of commerce fulfillment attempts",
		},
		[]string{"sku_code", "order_type", "status", "error_kind"},
	)

	FulfillmentDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "fulfillment_duration_seconds",
			Help:    "Commerce fulfillment duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"sku_code", "order_type", "status"},
	)

	PaymentAdjustmentsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_adjustments_total",
			Help: "Total number of payment adjustment policy attempts",
		},
		[]string{"event_type", "status", "policy_action", "error_kind"},
	)

	PaymentAdjustmentDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "payment_adjustment_duration_seconds",
			Help:    "Payment adjustment policy duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"event_type", "status", "policy_action"},
	)

	SubscriptionActionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "subscription_actions_total",
			Help: "Total number of subscription control actions",
		},
		[]string{"operation", "sku_code", "status", "error_kind"},
	)

	SubscriptionActionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "subscription_action_duration_seconds",
			Help:    "Subscription control action duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation", "sku_code", "status"},
	)

	CloudSyncOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloud_sync_total",
			Help: "Total number of cloud storage control-plane operations",
		},
		[]string{"operation", "provider", "status", "error_kind"},
	)

	CloudSyncOperationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cloud_sync_duration_seconds",
			Help:    "Cloud storage control-plane operation duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation", "provider", "status"},
	)

	AccessSnapshotsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "access_snapshots_total",
			Help: "Total number of signed access snapshot issuance attempts",
		},
		[]string{"status", "error_kind"},
	)

	AccessSnapshotDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "access_snapshot_duration_seconds",
			Help:    "Signed access snapshot issuance duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"status"},
	)

	AdminActionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "admin_actions_total",
			Help: "Total number of audited admin actions",
		},
		[]string{"action", "success"},
	)
)

func RecordCommerceCheckout(provider string, skuCode string, status string, errorKind string, duration time.Duration) {
	CommerceCheckoutsTotal.WithLabelValues(labelValue(provider), labelValue(skuCode), labelValue(status), labelValue(errorKind)).Inc()
	CommerceCheckoutDuration.WithLabelValues(labelValue(provider), labelValue(skuCode), labelValue(status)).Observe(duration.Seconds())
}

func RecordCheckoutPolicyBlock(reason string, action string) {
	CheckoutPolicyBlocksTotal.WithLabelValues(labelValue(reason), labelValue(action)).Inc()
}

func RecordPaymentEvent(operation string, provider string, eventType string, status string, errorKind string, duration time.Duration) {
	PaymentEventsTotal.WithLabelValues(labelValue(operation), labelValue(provider), labelValue(eventType), labelValue(status), labelValue(errorKind)).Inc()
	PaymentEventDuration.WithLabelValues(labelValue(operation), labelValue(provider), labelValue(eventType), labelValue(status)).Observe(duration.Seconds())
}

func RecordFulfillment(skuCode string, orderType string, status string, errorKind string, duration time.Duration) {
	FulfillmentsTotal.WithLabelValues(labelValue(skuCode), labelValue(orderType), labelValue(status), labelValue(errorKind)).Inc()
	FulfillmentDuration.WithLabelValues(labelValue(skuCode), labelValue(orderType), labelValue(status)).Observe(duration.Seconds())
}

func RecordPaymentAdjustment(eventType string, status string, policyAction string, errorKind string, duration time.Duration) {
	PaymentAdjustmentsTotal.WithLabelValues(labelValue(eventType), labelValue(status), labelValue(policyAction), labelValue(errorKind)).Inc()
	PaymentAdjustmentDuration.WithLabelValues(labelValue(eventType), labelValue(status), labelValue(policyAction)).Observe(duration.Seconds())
}

func RecordSubscriptionAction(operation string, skuCode string, status string, errorKind string, duration time.Duration) {
	SubscriptionActionsTotal.WithLabelValues(labelValue(operation), labelValue(skuCode), labelValue(status), labelValue(errorKind)).Inc()
	SubscriptionActionDuration.WithLabelValues(labelValue(operation), labelValue(skuCode), labelValue(status)).Observe(duration.Seconds())
}

func RecordCloudSync(operation string, provider string, status string, errorKind string, duration time.Duration) {
	CloudSyncOperationsTotal.WithLabelValues(labelValue(operation), labelValue(provider), labelValue(status), labelValue(errorKind)).Inc()
	CloudSyncOperationDuration.WithLabelValues(labelValue(operation), labelValue(provider), labelValue(status)).Observe(duration.Seconds())
}

func RecordAccessSnapshot(status string, errorKind string, duration time.Duration) {
	AccessSnapshotsTotal.WithLabelValues(labelValue(status), labelValue(errorKind)).Inc()
	AccessSnapshotDuration.WithLabelValues(labelValue(status)).Observe(duration.Seconds())
}

func RecordAdminAction(action string, success bool) {
	AdminActionsTotal.WithLabelValues(labelValue(action), strconv.FormatBool(success)).Inc()
}

func labelValue(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

// Middleware returns a Gin middleware that records HTTP metrics.
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := strconv.Itoa(c.Writer.Status())
		method := c.Request.Method
		path := c.FullPath() // Use route pattern, not actual path

		if path == "" {
			path = c.Request.URL.Path
		}

		HTTPRequestsTotal.WithLabelValues(method, path, status).Inc()
		HTTPRequestDuration.WithLabelValues(method, path).Observe(time.Since(start).Seconds())
	}
}

// Handler returns the Prometheus metrics HTTP handler.
func Handler() gin.HandlerFunc {
	h := promhttp.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}
