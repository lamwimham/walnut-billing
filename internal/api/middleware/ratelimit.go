package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// TokenBucket implements a simple token bucket rate limiter.
type TokenBucket struct {
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
	mu         sync.Mutex
}

// NewTokenBucket creates a new token bucket.
func NewTokenBucket(maxTokens, refillRate float64) *TokenBucket {
	return &TokenBucket{
		tokens:     maxTokens,
		maxTokens:  maxTokens,
		refillRate: refillRate,
		lastRefill: time.Now(),
	}
}

// Allow checks if a request is allowed.
func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens += elapsed * tb.refillRate
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}
	tb.lastRefill = now

	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}

// IPRateLimiter manages per-IP token buckets.
type IPRateLimiter struct {
	buckets    map[string]*TokenBucket
	maxTokens  float64
	refillRate float64
	mu         sync.Mutex
	cleanupTicker *time.Ticker
}

// NewIPRateLimiter creates a new IP-based rate limiter.
// maxTokens: burst size (e.g., 10 requests)
// refillRate: tokens per second (e.g., 1 = 1 request/sec)
func NewIPRateLimiter(maxTokens, refillRate float64) *IPRateLimiter {
	l := &IPRateLimiter{
		buckets:    make(map[string]*TokenBucket),
		maxTokens:  maxTokens,
		refillRate: refillRate,
	}

	// Cleanup stale buckets every 5 minutes
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			l.cleanup()
		}
	}()

	return l
}

func (l *IPRateLimiter) getBucket(ip string) *TokenBucket {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, exists := l.buckets[ip]
	if !exists {
		bucket = NewTokenBucket(l.maxTokens, l.refillRate)
		l.buckets[ip] = bucket
	}
	return bucket
}

func (l *IPRateLimiter) cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	for ip, bucket := range l.buckets {
		bucket.mu.Lock()
		if time.Since(bucket.lastRefill) > 10*time.Minute {
			delete(l.buckets, ip)
		}
		bucket.mu.Unlock()
	}
}

// RateLimit returns a Gin middleware that rate-limits by client IP.
func RateLimit(limiter *IPRateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		bucket := limiter.getBucket(ip)

		if !bucket.Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}

		c.Next()
	}
}
