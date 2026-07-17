package server

import (
	"context"
	"time"

	"golang.org/x/time/rate"
)

// getRateLimiter retrieves or creates a rate limiter for an IP.
func (s *Server) getRateLimiter(ip string) *rate.Limiter {
	s.rlMutex.Lock()
	defer s.rlMutex.Unlock()
	entry, exists := s.rateLimiters[ip]
	if !exists {
		// 10 requests per second, burst of 20
		entry = &ipLimiter{
			limiter:  rate.NewLimiter(rate.Limit(10), 20),
			lastSeen: time.Now(),
		}
		s.rateLimiters[ip] = entry
	} else {
		entry.lastSeen = time.Now()
	}
	return entry.limiter
}

// startRateLimiterCleaner runs a background routine that periodically prunes stale IP rate limiters.
func (s *Server) startRateLimiterCleaner(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.rlMutex.Lock()
				now := time.Now()
				for ip, entry := range s.rateLimiters {
					if now.Sub(entry.lastSeen) > 1*time.Hour {
						delete(s.rateLimiters, ip)
					}
				}
				s.rlMutex.Unlock()
			}
		}
	}()
}
