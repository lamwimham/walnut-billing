package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCORSMiddlewareAllowsConfiguredOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS(CORSConfig{AllowedOrigins: []string{"https://app.walnut.example"}}))
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	req, _ := http.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", "https://app.walnut.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "https://app.walnut.example" {
		t.Fatalf("unexpected allow-origin header: %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
	if w.Header().Get("Vary") != "Origin" {
		t.Fatalf("expected Vary Origin, got %q", w.Header().Get("Vary"))
	}
}

func TestCORSMiddlewareRejectsDisallowedPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS(CORSConfig{AllowedOrigins: []string{"https://app.walnut.example"}}))
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	r.OPTIONS("/ping", func(c *gin.Context) { c.String(http.StatusOK, "unexpected") })

	req, _ := http.NewRequest(http.MethodOptions, "/ping", nil)
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for disallowed preflight, got %d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed origin must not get CORS headers")
	}
}

func TestCORSMiddlewareRejectsDisallowedActualRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS(CORSConfig{AllowedOrigins: []string{"https://app.walnut.example"}}))
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	req, _ := http.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for disallowed origin, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSecurityHeadersMiddlewareAppliesProductionHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeaders(SecurityHeadersConfig{Enabled: true, HSTSMaxAgeSeconds: 63072000}))
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	req, _ := http.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Strict-Transport-Security"); got != "max-age=63072000; includeSubDomains" {
		t.Fatalf("unexpected HSTS header: %q", got)
	}
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := w.Header().Get(header); got != want {
			t.Fatalf("unexpected %s header: got %q want %q", header, got, want)
		}
	}
}
