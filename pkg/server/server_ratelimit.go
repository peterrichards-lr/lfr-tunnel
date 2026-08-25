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

// recordViolation counts one refused request against an address and returns the running total
// within the decay window (#1327).
//
// The decay is applied on read rather than only by the cleaner: the cleaner runs every ten
// minutes, so between ticks a stale count would still be live, and the ban it triggers is
// permanent. Checking the age here means the count that decides a ban is always the recent one,
// whatever the cleaner has or has not got to yet.
func (s *Server) recordViolation(ip string) int {
	s.vMutex.Lock()
	defer s.vMutex.Unlock()

	now := time.Now()
	entry, exists := s.violations[ip]
	if !exists || now.Sub(entry.lastSeen) > violationWindow {
		// Either the first violation from this address, or the previous run of them is old
		// enough to have expired. Both start a fresh count.
		entry = &ipViolations{}
		s.violations[ip] = entry
	}
	entry.count++
	entry.lastSeen = now
	return entry.count
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
				now := time.Now()

				s.rlMutex.Lock()
				for ip, entry := range s.rateLimiters {
					if now.Sub(entry.lastSeen) > violationWindow {
						delete(s.rateLimiters, ip)
					}
				}
				s.rlMutex.Unlock()

				// Violations are pruned on the same ticker and the same window as the limiters
				// they belong to. Without this the map held one entry per address that had ever
				// tripped the limiter, for the life of the process -- on an internet-facing
				// gateway under routine background scanning, that only ever grows.
				s.vMutex.Lock()
				for ip, entry := range s.violations {
					if now.Sub(entry.lastSeen) > violationWindow {
						delete(s.violations, ip)
					}
				}
				s.vMutex.Unlock()
			}
		}
	}()
}
