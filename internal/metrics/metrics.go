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
)

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
