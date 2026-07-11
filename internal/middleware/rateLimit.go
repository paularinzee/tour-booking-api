package middleware

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/paularinzee/natour/pkg/utils"
)

type Visitor struct {
	lastSeen time.Time
	count    int
}

type RateLimiter struct {
	visitors map[string]*Visitor
	mu       sync.RWMutex // Use an RWMutex for better read performance under heavy load
	requests int
	duration time.Duration
}

func NewRateLimiter(requests int, duration time.Duration, cleanupInterval time.Duration, stopChan chan struct{}) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*Visitor),
		requests: requests,
		duration: duration,
	}

	// Pass a stop channel to cleanly kill the goroutine during hot-reloads or testing teardowns
	go rl.startCleanup(cleanupInterval, stopChan)
	return rl
}

func (rl *RateLimiter) startCleanup(interval time.Duration, stopChan chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for ip, v := range rl.visitors {
				if now.Sub(v.lastSeen) > rl.duration {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		case <-stopChan:
			return // Exit cleanly
		}
	}
}

func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock() // Hold the lock through the evaluation to prevent cleanup race conditions

	now := time.Now()
	v, exists := rl.visitors[ip]
	if !exists {
		rl.visitors[ip] = &Visitor{lastSeen: now, count: 1}
		return true
	}

	if now.Sub(v.lastSeen) > rl.duration {
		v.count = 1
		v.lastSeen = now
		return true
	}

	if v.count >= rl.requests {
		return false
	}

	v.count++
	v.lastSeen = now
	return true
}

// RateLimiterMiddleware initializes a centralized, safe middleware handler instance.
func RateLimiterMiddleware(requests int, duration time.Duration) gin.HandlerFunc {
	// Use a 5-minute cleanup interval; pass nil/empty channel if lifecycle manages globally
	limiter := NewRateLimiter(requests, duration, 5*time.Minute, nil)

	return func(c *gin.Context) {
		ip := c.ClientIP()
		if !limiter.Allow(ip) {
			slog.Warn("Rate limit exceeded for client", "ip", ip, "limit", requests)

			// Set mandatory HTTP standard headers for 429 compliance
			c.Writer.Header().Set("Retry-After", "60")

			c.Error(utils.NewAppError(http.StatusTooManyRequests, "Too many requests. Please try again later.", nil))
			c.Abort()
			return
		}
		c.Next()
	}
}

// Predefined Global Instance Limiters - initialized safely once on startup
var (
	PublicLimiter  = RateLimiterMiddleware(50, time.Minute)
	DefaultLimiter = RateLimiterMiddleware(100, time.Minute)
	AdminLimiter   = RateLimiterMiddleware(200, time.Minute)
	StrictLimiter  = RateLimiterMiddleware(20, time.Minute)
)
