package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"lfr-tunnel/pkg/db"
)

// Region latency reporting (#1151).
//
// The client probes every advertised region at startup to pick one, then throws away every
// result but the winner. Those numbers are the best available evidence for whether a region
// needs an edge of its own -- better than geography, because they include the VPN, tethering and
// routing penalties a country code cannot show. Two users who are both "Europe" can have very
// different experiences of the same edge.
//
// Nothing here stores an IP or anything derived from one. An RTT is not personal data in the way
// a location is, which is what makes this answerable without a privacy argument.

// maxRegionProbesPerRegistration caps what one registration can submit.
//
// The payload is attacker-controlled on an endpoint that authenticates but does not otherwise
// bound this field, and each entry is a database write. The real number is the count of regions
// the gateway advertises -- currently five -- so this is generous and still finite.
const maxRegionProbesPerRegistration = 32

// recordRegionProbes stores a client's probe set, if it sent one.
//
// Never fatal to a registration. This is telemetry attached to the request that sets up a
// developer's tunnel, and failing that because an analytics write failed would trade something
// that matters for something that does not.
func (s *Server) recordRegionProbes(user *db.User, probes []RegionProbe) {
	if user == nil || len(probes) == 0 || s.db == nil {
		return
	}
	if len(probes) > maxRegionProbesPerRegistration {
		probes = probes[:maxRegionProbesPerRegistration]
	}

	samples := make([]db.RegionProbeSample, 0, len(probes))
	for _, p := range probes {
		if p.Region == "" {
			continue
		}
		sample := db.RegionProbeSample{Region: p.Region}
		if !p.Unreachable {
			// A negative or absurd RTT is either a broken clock or a client that made it up.
			// Dropped rather than clamped: an invented figure that survives into the median is
			// worse than a missing one, because the report is read as "somebody experienced
			// this".
			if p.RTTMs < 0 || p.RTTMs > 60_000 {
				continue
			}
			ms := p.RTTMs
			sample.RTTMs = &ms
		}
		samples = append(samples, sample)
	}
	if len(samples) == 0 {
		return
	}

	if err := s.db.RecordRegionProbes(user.ID, samples, time.Now()); err != nil {
		slog.Warn(fmt.Sprintf("[Analytics] Could not store region probe results: %v", err))
	}
}

// handleRegionLatency serves the aggregated report.
//
// Admin-only: it is a fleet-wide view built from every user's measurements, and the question it
// answers -- where should we put an edge -- is an operator's question.
func (s *Server) handleRegionLatency(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 365 {
			http.Error(w, `{"error":"days must be between 1 and 365"}`, http.StatusBadRequest)
			return
		}
		days = parsed
	}

	report, err := s.db.GetRegionLatency(days)
	if err != nil {
		slog.Error(fmt.Sprintf("[Analytics] Region latency report failed: %v", err))
		http.Error(w, `{"error":"Failed to build the region latency report"}`, http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusOK, report)
}
