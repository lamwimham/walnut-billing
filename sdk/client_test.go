package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	c, err := NewClient("SM-PRO-0001-0001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.cfg.BaseURL != DefaultBaseURL {
		t.Errorf("expected default base URL %s, got %s", DefaultBaseURL, c.cfg.BaseURL)
	}
	if c.cfg.DeviceID == "" {
		t.Error("expected auto-generated device ID")
	}
	if c.cfg.MaxRetries != DefaultMaxRetries {
		t.Errorf("expected max retries %d, got %d", DefaultMaxRetries, c.cfg.MaxRetries)
	}
}

func TestNewClient_EmptyKey(t *testing.T) {
	_, err := NewClient("")
	if err == nil {
		t.Fatal("expected error for empty license key")
	}
}

func TestVerify_Success(t *testing.T) {
	calls := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(verifyResponse{
			Status:    "active",
			Valid:     true,
			ExpiresAt: time.Now().AddDate(1, 0, 0).Format(time.RFC3339),
		})
	}))
	defer srv.Close()

	c, _ := NewClient("test-key", WithBaseURL(srv.URL), WithMaxRetries(0))
	result, err := c.Verify(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsValid {
		t.Error("expected valid result")
	}
	if result.ServerStatus != "active" {
		t.Errorf("expected status active, got %s", result.ServerStatus)
	}
	if result.IsOffline {
		t.Error("expected online result")
	}
	if calls.Load() != 1 {
		t.Errorf("expected 1 call, got %d", calls.Load())
	}
}

func TestVerify_ServerError_OfflineFallback(t *testing.T) {
	calls := atomic.Int32{}
	// Server fails all requests
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c, _ := NewClient("test-key", WithBaseURL(srv.URL), WithMaxRetries(1))

	// First verify: no cache, so should fail
	result, err := c.Verify(context.Background())
	if err == nil {
		t.Fatal("expected error for first verify with no cache")
	}
	if result.IsValid {
		t.Error("expected invalid result for no cache")
	}

	// Now simulate a successful verification to populate cache
	c.cache = &VerifyResult{IsValid: true, ServerStatus: "active"}
	c.cachedAt = time.Now()

	// Second verify: server fails, but cache hits
	result, err = c.Verify(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsValid {
		t.Error("expected valid result from cache")
	}
	if !result.IsOffline {
		t.Error("expected offline flag")
	}
}

func TestVerify_OfflineGraceExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c, _ := NewClient("test-key",
		WithBaseURL(srv.URL),
		WithMaxRetries(0),
		WithOfflineGracePeriod(1*time.Millisecond),
	)

	// Populate cache with old timestamp
	c.cache = &VerifyResult{IsValid: true, ServerStatus: "active"}
	c.cachedAt = time.Now().Add(-10 * time.Minute)

	result, err := c.Verify(context.Background())
	if err == nil {
		t.Fatal("expected error for expired grace period")
	}
	if result.IsValid {
		t.Error("expected invalid when grace expired")
	}
}

func TestActivate_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(activateResponse{
			Status:   "active",
			DeviceID: "test-device",
			MaxSeats: 3,
		})
	}))
	defer srv.Close()

	c, _ := NewClient("test-key", WithBaseURL(srv.URL), WithDeviceID("test-device"), WithMaxRetries(0))
	result, err := c.Activate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected activation success")
	}
	if result.MaxSeats != 3 {
		t.Errorf("expected max seats 3, got %d", result.MaxSeats)
	}
}

func TestRetry_ExponentialBackoff(t *testing.T) {
	calls := atomic.Int32{}
	start := time.Now()
	var mu sync.Mutex
	var timestamps []time.Time

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		timestamps = append(timestamps, time.Now())
		mu.Unlock()
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c, _ := NewClient("test-key", WithBaseURL(srv.URL), WithMaxRetries(3), WithTimeout(2*time.Second))
	c.Verify(context.Background())

	elapsed := time.Since(start)
	if elapsed < 1*time.Second {
		t.Errorf("expected retry delays, but finished in %v", elapsed)
	}
	if calls.Load() != 4 { // 1 initial + 3 retries
		t.Errorf("expected 4 calls, got %d", calls.Load())
	}
	t.Logf("Retry took %v with %d attempts", elapsed, calls.Load())
}

func TestVerifyAndActivate(t *testing.T) {
	var mu sync.Mutex
	callTypes := []string{}
	activated := atomic.Bool{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callTypes = append(callTypes, r.URL.Path)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/verify" {
			if activated.Load() {
				json.NewEncoder(w).Encode(verifyResponse{Status: "active", Valid: true})
			} else {
				json.NewEncoder(w).Encode(verifyResponse{Status: "inactive", Valid: false})
			}
		} else {
			activated.Store(true)
			json.NewEncoder(w).Encode(activateResponse{Status: "active", DeviceID: "dev-1"})
		}
	}))
	defer srv.Close()

	c, _ := NewClient("test-key", WithBaseURL(srv.URL), WithDeviceID("dev-1"), WithMaxRetries(0))
	result, err := c.VerifyAndActivate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsValid {
		t.Error("expected valid after activation")
	}
	if result.ServerStatus != "active" {
		t.Errorf("expected status active, got %s", result.ServerStatus)
	}
	// Expected: verify -> activate -> verify (after activation)
	if len(callTypes) != 3 {
		t.Errorf("expected 3 calls (verify, activate, re-verify), got %d: %v", len(callTypes), callTypes)
	}
}

func TestDeviceID_Generation(t *testing.T) {
	id := defaultDeviceID()
	if id == "" {
		t.Fatal("expected non-empty device ID")
	}
	// Should contain platform info
	if len(id) < 10 {
		t.Errorf("device ID too short: %s", id)
	}
	t.Logf("Generated device ID: %s", id)
}
