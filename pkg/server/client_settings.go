package server

// advertisedClientSettings builds the declarative settings block served on /api/version
// (#1948).
//
// Deliberately NOT a command channel -- see pkg/server/diagnostics_transport.go, whose
// reasoning about why collect_logs is the only command still holds. What goes here is a small
// set of named numbers a client already had constants for, and the client clamps every one of
// them into a range IT chose. A value interpreted within client-set bounds is a different risk
// class from remote execution, and keeping that line is the whole design.
//
// A zero value is omitted rather than sent, so a gateway with no opinion says nothing and the
// client keeps its own default. That is what lets the block grow without a flag day.
func (s *Server) advertisedClientSettings() map[string]int {
	out := map[string]int{}
	if s.cfg == nil {
		return out
	}
	if secs := int(s.cfg.ClientReconnectWindow.Seconds()); secs > 0 {
		out["reconnect_seconds"] = secs
	}
	if secs := int(s.cfg.ClientHeartbeatInterval.Seconds()); secs > 0 {
		out["heartbeat_seconds"] = secs
	}
	return out
}
