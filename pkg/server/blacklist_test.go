package server

import (
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

func TestIsBlacklisted_PermanentBanHasNoExpiry(t *testing.T) {
	s := &Server{}
	s.cacheBan("203.0.113.9", nil)

	if !s.isBlacklisted("203.0.113.9") {
		t.Error("a permanent ban must block")
	}
}

func TestIsBlacklisted_ExpiredBanStopsBlocking(t *testing.T) {
	s := &Server{}
	past := time.Now().Add(-time.Minute)
	s.cacheBan("203.0.113.9", &past)

	if s.isBlacklisted("203.0.113.9") {
		t.Error("an expired ban is still blocking")
	}
}

func TestIsBlacklisted_UnexpiredBanStillBlocks(t *testing.T) {
	s := &Server{}
	future := time.Now().Add(time.Hour)
	s.cacheBan("203.0.113.9", &future)

	if !s.isBlacklisted("203.0.113.9") {
		t.Error("a ban that has not yet expired must still block")
	}
}

// The check runs on every request ahead of all routing, so it must not consult the database to
// discover that a ban has lapsed. Finding an expired entry drops it from the cache.
func TestIsBlacklisted_DropsTheExpiredEntry(t *testing.T) {
	s := &Server{}
	past := time.Now().Add(-time.Minute)
	s.cacheBan("203.0.113.9", &past)

	s.isBlacklisted("203.0.113.9")

	if _, found := s.blacklist.Load("203.0.113.9"); found {
		t.Error("the expired entry was left in the cache")
	}
}

func TestIsBlacklisted_UnknownAddressIsNotBlocked(t *testing.T) {
	s := &Server{}
	if s.isBlacklisted("203.0.113.9") {
		t.Error("an address that was never banned must not be blocked")
	}
}

// fail2ban ships an ignoreip covering loopback, and the reasoning carries: the blacklist is
// enforced ahead of all routing, so an operator who auto-bans themselves cannot reach the admin
// API to undo it.
func TestAutoBanExempt_HonoursTheIgnoreList(t *testing.T) {
	s := &Server{cfg: &config.ServerConfig{
		AutoBan: config.AutoBanConfig{Ignore: []string{"127.0.0.1/32", "10.0.0.0/8"}},
	}}

	cases := []struct {
		ip     string
		exempt bool
	}{
		{"127.0.0.1", true},
		{"10.1.2.3", true},
		{"203.0.113.9", false},
		{"::1", false}, // not in this configured list
	}

	for _, tc := range cases {
		if got := s.autoBanExempt(tc.ip); got != tc.exempt {
			t.Errorf("autoBanExempt(%q) = %v, want %v", tc.ip, got, tc.exempt)
		}
	}
}

// A typo in an allow-list should cost a missed exemption, not a crash or a refusal to start.
func TestAutoBanExempt_IgnoresAMalformedEntry(t *testing.T) {
	s := &Server{cfg: &config.ServerConfig{
		AutoBan: config.AutoBanConfig{Ignore: []string{"not-a-cidr", "10.0.0.0/8"}},
	}}

	if !s.autoBanExempt("10.1.2.3") {
		t.Error("a malformed entry earlier in the list prevented a later, valid one from matching")
	}
	if s.autoBanExempt("203.0.113.9") {
		t.Error("a malformed entry must not match everything")
	}
}

func TestApplyAutoBan_RefusesToBanAnExemptAddress(t *testing.T) {
	s := &Server{cfg: &config.ServerConfig{
		AutoBan: config.AutoBanConfig{
			Duration: time.Hour,
			Ignore:   []string{"127.0.0.1/32"},
		},
	}}

	_, banned := s.applyAutoBan("127.0.0.1", "flooding")
	if banned {
		t.Error("an address in the ignore list was auto-banned")
	}
	if s.isBlacklisted("127.0.0.1") {
		t.Error("an exempt address ended up in the blacklist cache")
	}
}

// With no database there is no ban history to escalate from, but the ban still has to happen --
// the cache is what the request path actually reads.
func TestApplyAutoBan_BansInMemoryWithoutADatabase(t *testing.T) {
	s := &Server{cfg: &config.ServerConfig{
		AutoBan: config.AutoBanConfig{Duration: time.Hour},
	}}

	expiresAt, banned := s.applyAutoBan("203.0.113.9", "flooding")
	if !banned {
		t.Fatal("the address was not banned")
	}
	if expiresAt == nil {
		t.Error("a configured duration should have produced an expiry")
	}
	if !s.isBlacklisted("203.0.113.9") {
		t.Error("the ban did not reach the cache")
	}
}

func TestEscalationFactor_CollapsesDisabledAndMeaningless(t *testing.T) {
	cases := []struct {
		name string
		in   autoBanSettings
		want float64
	}{
		{"escalation off", autoBanSettings{Increment: false, Factor: 4}, 1},
		{"escalation on", autoBanSettings{Increment: true, Factor: 4}, 4},
		{"factor of 1", autoBanSettings{Increment: true, Factor: 1}, 1},
		{"factor below 1", autoBanSettings{Increment: true, Factor: 0.5}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.escalationFactor(); got != tc.want {
				t.Errorf("escalationFactor() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDescribeBanDuration(t *testing.T) {
	if got := describeBanDuration(nil); got != "until manually removed" {
		t.Errorf("nil expiry rendered as %q", got)
	}
	future := time.Now().Add(time.Hour)
	if got := describeBanDuration(&future); got == "until manually removed" {
		t.Error("a real expiry was described as permanent")
	}
}
