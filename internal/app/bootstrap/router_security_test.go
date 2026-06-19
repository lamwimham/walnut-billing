package bootstrap

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"walnut-billing/internal/api/handler"
	"walnut-billing/internal/config"
)

func TestBuildRouterAppliesProductionSecurityMiddleware(t *testing.T) {
	r, err := buildRouter(routerDependencies{
		Config: &config.Config{
			Server: config.ServerConfig{Env: config.ProductionEnv},
			HTTP: config.HTTPConfig{
				CORSAllowedOrigins: []string{"https://ops.walnut.example"},
				SecurityHeaders: config.HTTPSecurityHeadersConfig{
					Enabled:           true,
					HSTSMaxAgeSeconds: 31536000,
				},
			},
			Admin:     config.AdminConfig{APIKeys: []string{"ops-key"}},
			RateLimit: config.RateLimitConfig{Enabled: false, MaxTokens: 20, RefillRate: 2},
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handlers: applicationHandlers{
			Health: &handler.HealthHandler{},
		},
	})
	if err != nil {
		t.Fatalf("build router: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", "https://ops.walnut.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected ping 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://ops.walnut.example" {
		t.Fatalf("expected configured CORS origin, got %q", got)
	}
	if got := w.Header().Get("Strict-Transport-Security"); got == "" {
		t.Fatalf("expected HSTS header")
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("expected frame denial header, got %q", got)
	}
}
