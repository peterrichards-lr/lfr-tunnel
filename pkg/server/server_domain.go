package server

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

func (s *Server) generateRandomSubdomainPrefix(style string) string {
	randInt := func(max int) int {
		b := make([]byte, 4)
		_, _ = rand.Read(b) //nolint:errcheck
		val := int(uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]))
		if val < 0 {
			val = -val
		}
		return val % max
	}

	switch style {
	case "words":
		return fmt.Sprintf("%s-%s-%s", generatorWords[randInt(len(generatorWords))], generatorWords[randInt(len(generatorWords))], generatorWords[randInt(len(generatorWords))])
	case "heroku":
		return fmt.Sprintf("%s-%s-%d", generatorAdjectives[randInt(len(generatorAdjectives))], generatorNouns[randInt(len(generatorNouns))], randInt(9000)+1000)
	case "liferay":
		return fmt.Sprintf("%s-%s-%d", generatorTechAdjectives[randInt(len(generatorTechAdjectives))], generatorLiferayNouns[randInt(len(generatorLiferayNouns))], randInt(900)+100)
	default: // Completely Random (Alphanumeric) [a-z0-9]{8}
		const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
		b := make([]byte, 8)
		_, _ = rand.Read(b) //nolint:errcheck
		for i := range b {
			b[i] = chars[int(b[i])%len(chars)]
		}
		return string(b)
	}
}

// isCustomDomain checks if a host does not belong to configured root domains.
func (s *Server) isCustomDomain(host string) bool {
	for _, d := range s.cfg.Domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return false
		}
	}
	return true
}

// runVanityDomainHook runs the external script with action ("add"/"remove") and domain.
func (s *Server) runVanityDomainHook(action, domain string) {
	if s.cfg.VanityDomainHook == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	slog.Info(fmt.Sprintf("[Server] Executing vanity domain hook: %s %s %s", s.cfg.VanityDomainHook, action, domain))
	cmd := exec.CommandContext(ctx, s.cfg.VanityDomainHook, action, domain)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Info(fmt.Sprintf("[Server] Vanity domain hook error running %s for %s: %v. Output: %s", action, domain, err, string(output)))
	} else {
		slog.Info(fmt.Sprintf("[Server] Vanity domain hook ran successfully for %s %s", action, domain))
	}
}

func (s *Server) checkQuarantineStatus(host string) (bool, string, string) {
	if s.db == nil {
		return false, "", ""
	}
	for _, domain := range s.cfg.Domains {
		if strings.HasSuffix(host, "."+domain) {
			subdomain := strings.TrimSuffix(host, "."+domain)
			existing, err := s.db.GetSubdomainReservationByName(subdomain, domain)
			if err == nil && existing != nil {
				if existing.ExpiresAt != nil && existing.ExpiresAt.Before(time.Now()) {
					quarantineCutoff := existing.ExpiresAt.AddDate(0, 0, s.cfg.SubdomainQuarantineDays)
					if time.Now().Before(quarantineCutoff) {
						return true, host, quarantineCutoff.Format("2006-01-02 15:04:05 MST")
					}
				}
			}
		}
	}
	return false, "", ""
}
