package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/gui"
	"lfr-tunnel/pkg/mcp"
	"lfr-tunnel/pkg/osutil"

	"github.com/mattn/go-isatty"
)

type arrayFlags []string

func (i *arrayFlags) String() string {
	return strings.Join(*i, ", ")
}

func (i *arrayFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}

// Package-level command-line flags
var (
	configPath         = flag.String("config", "", "Path to client-config.yaml")
	guiFlag            = flag.Bool("gui", false, "Start client in system tray GUI mode")
	serverURL          = flag.String("server", "", "Gateway server URL (e.g. https://tunnel.liferay.com)")
	token              = flag.String("token", "", "Gateway auth token")
	subdomain          = flag.String("subdomain", "", "Requested subdomain prefix (e.g. alpha-se)")
	portsStr           = flag.String("ports", "", "Comma-separated ports to expose (e.g. 8080,3000)")
	basicAuth          = flag.String("basic-auth", "", "Require HTTP Basic Auth (format: 'username:password')")
	inspectorPort      = flag.Int("inspector-port", 4040, "Local port for the Inspector Web UI")
	addHeaders         arrayFlags
	rateLimit          = flag.Int("rate-limit", 0, "Max requests per second for your subdomains (0 = unlimited)")
	targetHost         = flag.String("target-host", "", "Target hostname or IP to route traffic to (e.g. my-project.local)")
	preserveHost       = flag.Bool("preserve-host", false, "Preserve incoming Host header instead of rewriting to target host")
	insecureSkipVerify = flag.Bool("insecure-skip-verify", false, "Allow insecure local SSL (Skip TLS Verification)")
	themePref          = flag.String("theme", "", "Local UI theme preference (light, dark, system, time)")
	background         = flag.Bool("background", false, "Run client in background")
	status             = flag.Bool("status", false, "Check status of the background tunnel")
	statusJSON         = flag.Bool("status-json", false, "Print JSON status of the background tunnel")
	stop               = flag.Bool("stop", false, "Stop the background tunnel")
	versionFlag        = flag.Bool("version", false, "Print client version")
	checkVersionFlag   = flag.Bool("check-version", false, "Check server API for version requirements and print as JSON")
	upgradeFlag        = flag.Bool("upgrade", false, "Self-upgrade client to the latest release")
	noTUI              = flag.Bool("no-tui", false, "Disable interactive terminal dashboard UI")
	logBodies          = flag.Bool("log-bodies", false, "Also record request/response bodies in the traffic log (may contain tokens and customer data)")
	logDirFlag         = flag.String("log-dir", "", "Directory for the persistent traffic and error logs (default ~/.lfr-tunnel/logs)")
	passcode           = flag.String("passcode", "", "Passcode to protect the public tunnel URLs")
	whitelistIP        = flag.String("whitelist-ip", "", "Comma-separated IP addresses allowed to access the tunnel")
	region             = flag.String("region", "", "Gateway region to target (e.g. eu, us-east, us-west, latam, apac)")
	refreshRegion      = flag.Bool("refresh-region", false, "Force re-probing region latencies and refresh the 24h region cache")
	domain             = flag.String("domain", "", "Custom domain name (e.g. custom-client-site.com)")
	latency            = flag.Duration("latency", 0, "Simulated network roundtrip latency (e.g. 200ms, 1s)")
	bandwidth          = flag.String("bandwidth", "", "Simulated bandwidth throttling limit (e.g. 512kbps, 2mbps)")
)

func init() {
	flag.Var(&addHeaders, "add-header", "Inject HTTP header (e.g. 'X-Bypass-CORS: true')")

	oldUsage := flag.Usage
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: lfr-tunnel [options] [command]\n\n")
		fmt.Fprintf(os.Stderr, "Subcommands:\n")
		fmt.Fprintf(os.Stderr, "  install-service        Install background CLI autostart service (LaunchAgent/systemd/Windows startup)\n")
		fmt.Fprintf(os.Stderr, "  uninstall-service      Uninstall background CLI autostart service\n")
		fmt.Fprintf(os.Stderr, "  install-gui-service    Install background GUI tray autostart service\n")
		fmt.Fprintf(os.Stderr, "  uninstall-gui-service  Uninstall background GUI tray autostart service\n")
		fmt.Fprintf(os.Stderr, "  login                  Authenticate via browser handoff / OIDC\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		oldUsage()
	}
}

func main() {
	flag.Parse()

	// 1. Load config from file and environment variables
	cfg, err := config.LoadClientConfig(*configPath)
	if err != nil {
		log.Fatalf("[Client] Failed to load configuration: %v", err)
	}

	// 2. Override with CLI flags
	overrideConfigWithFlags(cfg)

	// Determine if subdomain flag was explicitly passed
	subdomainFlagPassed := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "subdomain" {
			subdomainFlagPassed = true
		}
	})

	// Resolve subdomain prefix early (so background, stop, status checks know the subdomain name)
	sub := cfg.Subdomain
	if sub == "" {
		hostname, err := os.Hostname()
		if err == nil && hostname != "" {
			sub = strings.ToLower(hostname)
			if idx := strings.Index(sub, "."); idx != -1 {
				sub = sub[:idx]
			}
			sub = strings.ReplaceAll(sub, " ", "-")
			sub = strings.ReplaceAll(sub, "_", "-")
		} else {
			sub = "se-dev"
		}
	}
	sub = strings.ToLower(strings.TrimSpace(sub))

	// Execute utility subcommands early (e.g., -version, install-gui-service) before network region resolution
	if executeSubcommands(cfg, sub, subdomainFlagPassed) {
		return
	}

	isExplicitServer := *serverURL != "" || os.Getenv("LFT_CLIENT_SERVER") != "" || os.Getenv("LFT_SERVER_URL") != "" || os.Getenv("LFT_SERVER") != ""
	resolveServerURL(cfg, isExplicitServer)

	// A build that bakes in no DefaultServerURL (#1188) reaches here with nothing to
	// connect to. Say so now, in terms the user can act on -- otherwise the first symptom
	// is a request to the empty string failing somewhere inside registration.
	if cfg.ServerURL == "" {
		log.Fatalf("[Error] No tunnel server configured. Pass -server https://your-gateway.example.com, " +
			"set LFT_SERVER_URL, or add server_url to your client config file.")
	}

	if *guiFlag {
		gui.StartGUI(cfg)
		return
	}

	// 3. Synchronous Server Compatibility and Maintenance Check
	var latestVersion string
	info, err := client.CheckServerCompatibility(cfg.ServerURL)
	if err == nil && info != nil {
		if info.MaintenanceMode == "true" {
			slog.Info("[Client] The Gateway server is currently undergoing maintenance.")
			for _, line := range statusPageAdvice() {
				slog.Info(line)
			}
			os.Exit(1)
		}
		if config.Version != "dev" {
			if client.CompareVersions(config.Version, info.MinVersion) < 0 {
				log.Fatalf("[Error] Your Liferay Tunnel client is too old to connect to the server. Minimum required version is %s.", info.MinVersion)
			}
			if client.CompareVersions(config.Version, info.LatestVersion) < 0 {
				slog.Info(fmt.Sprintf("[Warning] A new version of Liferay Tunnel (%s) is available. You are running %s. Run 'lfr-tunnel -upgrade' to update.", info.LatestVersion, config.Version))
			}
			latestVersion = info.LatestVersion
		}
	}

	// 3. Resolve port mappings
	portMappings := resolvePortsAndMappings(cfg)

	// Copy original ports for status monitoring before we modify portMappings to point to Interceptor ports
	var originalPorts []int
	for _, pm := range portMappings {
		originalPorts = append(originalPorts, pm.LocalPort)
	}

	// Start Interceptor Engine
	engine := client.NewInterceptorEngine(cfg.TargetHost, addHeaders)
	engine.MaintenancePath = cfg.MaintenancePath
	engine.Token = cfg.AuthToken
	engine.ServerURL = cfg.ServerURL
	engine.SelectedRegion = cfg.Region
	engine.SetCentralURL(centralControlPlaneURL(cfg))
	engine.SetLatestVersion(latestVersion)

	// Where those logs live, resolved with the same precedence as every other client
	// setting: flag, then environment, then the config file (#1223). Set before anything
	// opens a log, and process-wide because the Inspector's log viewer reads through the
	// same resolution -- otherwise it would keep looking in the default directory.
	client.SetLogDir(firstNonEmpty(*logDirFlag, os.Getenv("LFT_LOG_DIR"), cfg.LogDir))

	// Persistent traffic and diagnostic logs, for both foreground and background runs.
	// A failure to open them must not stop the tunnel starting, so it is reported and
	// the engine simply carries a nil logger, which discards.
	if logDir, lerr := client.LogDir(); lerr != nil {
		slog.Info(fmt.Sprintf("[Warning] Could not resolve the log directory, continuing without persistent logs: %v", lerr))
	} else if sessionLog, lerr := client.NewSessionLogger(logDir, sub, *logBodies); lerr != nil {
		slog.Info(fmt.Sprintf("[Warning] Could not open the client logs, continuing without them: %v", lerr))
	} else {
		engine.SetSessionLogger(sessionLog)
		defer sessionLog.Close() //nolint:errcheck
		sessionLog.Event("info", "session_start", map[string]any{
			"version":    config.Version,
			"subdomain":  sub,
			"region":     cfg.Region,
			"server_url": cfg.ServerURL,
			"log_bodies": *logBodies,
		})
	}
	engine.Passcode = cfg.Passcode
	engine.WhitelistIPs = cfg.WhitelistIPs
	engine.PreserveHost = cfg.PreserveHost
	engine.ClientSubdomain = sub
	engine.InsecureSkipVerify = cfg.InsecureSkipVerify
	engine.AccessMode = "or"
	engine.Latency = cfg.Latency
	if cfg.Bandwidth != "" {
		bwLimit, err := client.ParseBandwidth(cfg.Bandwidth)
		if err != nil {
			log.Fatalf("[Error] Invalid bandwidth value '%s': %v", cfg.Bandwidth, err)
		}
		engine.BandwidthLimit = bwLimit
	}
	actualInspectorPort, err := client.StartInspector(*inspectorPort, engine)
	if err != nil {
		log.Fatalf("[Error] Failed to start Inspector dashboard: %v", err)
	}
	*inspectorPort = actualInspectorPort
	// The bound port is not necessarily the requested one -- StartInspector walks
	// upwards when the port is taken -- so record what it actually got, for the TUI
	// to display.
	engine.SetInspectorPort(actualInspectorPort)

	if cfg.CustomDomain != "" {
		slog.Info("[Client] Custom domain: " + cfg.CustomDomain)
	} else {
		slog.Info("[Client] Subdomain prefix: " + sub)
	}
	slog.Info("[Client] Exposing ports:")
	for _, pm := range portMappings {
		suffixStr := " (Primary)"
		if pm.NameSuffix != "" {
			suffixStr = fmt.Sprintf(" (Suffix: -%s)", pm.NameSuffix)
		}
		slog.Info(fmt.Sprintf("  - Local port %d%s", pm.LocalPort, suffixStr))
	}

	// 5. Registration Handshake
	if cfg.CustomDomain != "" {
		fmt.Printf("[Client] Registering tunnel (%s) at %s...\n", cfg.CustomDomain, cfg.ServerURL)
	} else {
		fmt.Printf("[Client] Registering tunnel (%s) at %s...\n", sub, cfg.ServerURL)
	}
	if cfg.RateLimit > 0 {
		fmt.Printf("[Client] Requested Subdomain Rate Limit: %d req/s\n", cfg.RateLimit)
	}
	if cfg.Passcode != "" {
		fmt.Printf("[Client] Data Plane Passcode Protection is ENABLED\n")
	}
	if cfg.WhitelistIPs != "" {
		fmt.Printf("[Client] Data Plane IP Whitelisting is ENABLED (%s)\n", cfg.WhitelistIPs)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	regResp := performRegistrationHandshake(cfg, portMappings, sub, engine.AddedHeaders)

	// A gateway named with -server is used and nothing else -- the failover path is gated on
	// !isExplicitServer. That is the right contract, but it was never disclosed: a pinned
	// client simply dropped when its gateway hit a scheduled stop and stayed down for the
	// whole window. Say so now, while the user can still choose differently (#1275).
	if notice := client.PinnedShutdownNotice(regResp, isExplicitServer); notice != "" {
		slog.Warn(fmt.Sprintf("[Client] %s", notice))
	}

	if regResp.LanguagePreference != "" {
		engine.LanguagePreference = regResp.LanguagePreference
	}
	if cfg.Theme != "" {
		engine.ThemePreference = cfg.Theme
	} else if regResp.ThemePreference != "" {
		engine.ThemePreference = regResp.ThemePreference
	}
	if cfg.NavPlacement != "" {
		engine.NavPlacement = cfg.NavPlacement
	}

	// Keep an un-mutated copy of portMappings for server registration
	regPortMappings := make([]client.PortMapping, len(portMappings))
	copy(regPortMappings, portMappings)

	// Modify portMappings to point to dynamic Interceptor ports
	portMap := make(map[int]int)
	for i, pm := range portMappings {
		targetPort := pm.LocalPort
		interceptPort, err := engine.InterceptPort(targetPort)
		if err != nil {
			log.Fatalf("[Error] Failed to start interceptor for port %d: %v", targetPort, err)
		}
		portMappings[i].LocalPort = interceptPort
		portMap[targetPort] = interceptPort
	}

	rewriteRemotes(regResp, portMap)

	subHost := sub
	if cfg.CustomDomain != "" {
		subHost = ""
	} else if regResp.SubdomainPrefix != "" {
		subHost = regResp.SubdomainPrefix
	}

	publicURLs := printAndCollectPublicURLs(cfg, regResp, portMappings, subHost)
	engine.PublicURLs = publicURLs

	// Write dynamic client state to file
	state := &client.ClientState{
		PID:           os.Getpid(),
		InspectorPort: *inspectorPort,
		InspectorURL:  fmt.Sprintf("http://127.0.0.1:%d", *inspectorPort),
		Subdomain:     subHost,
		PublicURLs:    publicURLs,
		Ports:         originalPorts,
		StartTime:     time.Now().Format(time.RFC3339),
		Region:        cfg.Region,
		ServerURL:     cfg.ServerURL,
	}
	if err := client.WriteState(subHost, state); err != nil {
		slog.Info(fmt.Sprintf("[Warning] Failed to write state file: %v\n", err))
	}

	// 6. Run Client and wait for signals
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		slog.Info("[Client] Shutdown signal received, notifying gateway before closing tunnel... (press Ctrl+C again to force quit immediately)")

		// Proactively tell the gateway we're disconnecting so it cleans up our lease (and
		// runs any vanity domain hook removal) right away, instead of waiting for its
		// periodic orphan-lease sweep (up to 10s, see StartCleanupRoutine server-side).
		// Without this, immediately restarting with the same custom domain can hit a
		// spurious "already taken" 409 for our own not-yet-cleaned-up previous session.
		// Best-effort with a short timeout -- if the gateway is unreachable, fall through
		// to closing the tunnel anyway rather than hang indefinitely.
		done := make(chan struct{})
		go func() {
			defer close(done)
			deregisterCtx, deregisterCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer deregisterCancel()
			body, err := json.Marshal(map[string]string{"session_token": regResp.SessionToken})
			if err != nil {
				return
			}
			req, err := http.NewRequestWithContext(deregisterCtx, http.MethodPost, strings.TrimRight(cfg.ServerURL, "/")+"/api/deregister", bytes.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			resp.Body.Close() //nolint:errcheck
		}()

		select {
		case <-done:
		case <-sigs:
			slog.Info("[Client] Second shutdown signal received, force quitting...")
		}
		cancel()
	}()

	// Set lease status and subdomains info on engine
	engine.SetSubdomainDetails(sub, regResp.SubdomainPrefix, true, false)

	// Save primary region and server URL for automated failback (#1103)
	primaryRegion := cfg.Region
	primaryServerURL := cfg.ServerURL
	primaryRegionsMap := make(map[string]string)
	for k, v := range cfg.Regions {
		primaryRegionsMap[k] = v
	}
	engine.PrimaryRegion = primaryRegion
	engine.PrimaryServerURL = primaryServerURL

	// When the last failback completed, so an eviction shortly afterwards can be
	// recognised as the primary refusing the session rather than a fresh fault.
	var lastFailbackAt time.Time

	// Check if TUI is enabled and if stdout/stderr are terminals (not redirected, not backgrounded)
	tuiEnabled := !*noTUI && !*background && isatty.IsTerminal(os.Stdout.Fd()) && isatty.IsTerminal(os.Stderr.Fd())
	var cleanupTUI func()
	if tuiEnabled {
		cleanupTUI = client.StartTUIDashboard(ctx, engine, publicURLs)
	}

	// Bound to the outer ctx rather than a per-session one: this outlives failovers, and
	// a client left running for days is exactly the case a startup-only check misses.
	engine.StartVersionWatcher(ctx, cfg.ServerURL, 6*time.Hour)

	// The prober knows the primary is answering; only this side knows whether we left it
	// deliberately. Wired here so the cooldown policy stays in one place, and consulted by the
	// prober BEFORE it ends the session -- checking afterwards is too late, because ending the
	// session is itself the interruption a drain exists to avoid (#1310).
	engine.SetFailbackGate(func(regionURL string) bool {
		cooling, _ := cooldowns.coolingDown(regionURL)
		return !cooling
	})

	for ctx.Err() == nil {
		clientCtx, cancelClient := context.WithCancel(ctx)
		healthCheckPorts := make([]int, 0, len(portMappings))
		for _, pm := range portMappings {
			healthCheckPorts = append(healthCheckPorts, pm.LocalPort)
		}
		engine.StartHealthChecks(clientCtx, cancelClient, cfg.ServerURL, cfg.Region, regResp.SessionToken, healthCheckPorts)
		if primaryRegion != "" && primaryServerURL != "" {
			engine.StartFailbackProber(clientCtx, cancelClient, primaryServerURL, primaryRegion)
		}
		// Only for a client that can actually move. One pinned with -server does not fail
		// over (#1275), so ending its session on a shutdown warning would drop the tunnel
		// with nowhere to go -- strictly worse than letting it run until the gateway stops.
		if !isExplicitServer {
			engine.StartShutdownMigrator(clientCtx, cancelClient)
		}

		err = client.RunClient(clientCtx, cfg.ServerURL, regResp.SessionToken, regResp.Remotes, publicURLs, engine)
		cancelClient()

		if ctx.Err() != nil {
			break
		}

		if (err != nil || clientCtx.Err() != nil) && !isExplicitServer {
			// applySession commits a successful re-registration to every place the
			// current endpoint is recorded.
			applySession := func(newResp *client.RegisterResponse, what string) {
				regResp = newResp
				rewriteRemotes(regResp, portMap)
				publicURLs = printAndCollectPublicURLs(cfg, regResp, portMappings, subHost)
				engine.SetRegionEndpoint(cfg.Region, cfg.ServerURL, publicURLs)
				// The region list can change across a failover, so re-derive rather
				// than keeping whatever central was advertised at startup.
				engine.SetCentralURL(centralControlPlaneURL(cfg))
				engine.SetSubdomainDetails(sub, regResp.SubdomainPrefix, true, false)
				state.Region = cfg.Region
				state.ServerURL = cfg.ServerURL
				state.PublicURLs = publicURLs
				if werr := client.WriteState(subHost, state); werr != nil {
					slog.Info(fmt.Sprintf("[Warning] Failed to update state file on %s: %v\n", what, werr))
				}
				cooldowns.clear(cfg.ServerURL)
			}

			// Set when a failback attempt failed and put us back on the region we were
			// already serving from. That region did not fail -- the session was
			// cancelled deliberately to try the primary -- so the failover bookkeeping
			// below must not treat it as the casualty (issue #1137).
			failbackReturned := false

			// The gateway stopped holding our lease while the tunnel itself was fine.
			// Nothing is wrong with the region, so re-register rather than failing away
			// from it (issue #1146).
			leaseLost := engine.ConsumeLeaseLost()

			// The session ended because the gateway announced it was stopping and the
			// migrator moved us off ahead of it, rather than because anything failed
			// (#1246). The gateway is not broken and will be back, so this is logged as a
			// planned move -- and held off for far longer than a fault would be, since it
			// is about to be unreachable for its whole stop window.
			plannedAt, plannedReason, plannedMove := engine.ConsumeShutdownMigration()

			// A failback that is evicted almost immediately means the primary answers
			// /api/healthz but cannot carry the session. Hold the prober off, or it
			// retries every 15s and the client flaps between regions until something
			// reaps its lease (issue #1145).
			if !lastFailbackAt.IsZero() && time.Since(lastFailbackAt) < failbackEvictionWindow {
				slog.Info(fmt.Sprintf("[Client] Region '%s' dropped the session %s after failback; holding off further failback attempts.",
					cfg.Region, time.Since(lastFailbackAt).Round(time.Second)))
				engine.LogEvent("warn", "failback_unstable", map[string]any{
					"region":     cfg.Region,
					"held_for":   time.Since(lastFailbackAt).Round(time.Second).String(),
					"suppressed": failbackSuppression.String(),
				})
				engine.SuppressFailback(failbackSuppression)
				cooldowns.exclude(cfg.ServerURL, failbackSuppression)
				lastFailbackAt = time.Time{}
			}

			// A failback the client asked for is not automatically a failback it should take.
			// The prober only knows the primary is answering; the cooldown knows whether we
			// deliberately left it, and why. Without this a client moved off ahead of an
			// announced stop was pulled straight back the moment that gateway returned --
			// and the return trip costs a re-registration and a chisel reconnect, which is
			// the interruption the announcement existed to avoid (#1310).
			//
			// Said out loud rather than silently skipped. A signal published and never
			// consumed is how #1145 happened; a signal consumed and never mentioned is how
			// the next one will.
			// The gate above stops the prober ever getting this far while the primary is
			// cooling down. This is the second line of the same defence, for a cooldown that
			// began between the gate check and here.
			failbackReady := engine.ConsumeFailback()
			if failbackReady {
				if cooling, remaining := cooldowns.coolingDown(primaryServerURL); cooling {
					slog.Info(fmt.Sprintf("[Client] Primary region '%s' (%s) is reachable again, but we left it deliberately -- staying put for another %s.",
						primaryRegion, primaryServerURL, remaining.Round(time.Second)))
					engine.LogEvent("info", "failback_deferred", map[string]any{
						"region":             primaryRegion,
						"cooldown_remaining": remaining.Round(time.Second).String(),
					})
					failbackReady = false
				}
			}

			if failbackReady {
				slog.Info(fmt.Sprintf("[Client] Primary region '%s' (%s) recovered. Performing automated failback...", primaryRegion, primaryServerURL))

				// Remember where we are, so a failed failback can put us back rather
				// than leaving the tunnel down.
				fallbackRegion, fallbackServerURL := cfg.Region, cfg.ServerURL

				cfg.Region = primaryRegion
				cfg.ServerURL = primaryServerURL
				cfg.Regions = make(map[string]string)
				for k, v := range primaryRegionsMap {
					cfg.Regions[k] = v
				}

				newResp, failure := attemptRegistration(cfg, regPortMappings, sub, engine.AddedHeaders)
				if failure == nil {
					applySession(newResp, "failback")
					lastFailbackAt = time.Now()
					slog.Info(fmt.Sprintf("[Client] Successfully failed back to primary region '%s' (%s)", cfg.Region, cfg.ServerURL))
					engine.LogEvent("info", "failback", map[string]any{
						"from": fallbackRegion,
						"to":   cfg.Region,
					})
					continue
				}

				// A failback that fails must never be worse than not attempting one.
				// Hold the primary off for a cooldown so the prober does not retry it
				// immediately, restore the region we were working on, and let the
				// failover path below re-establish the tunnel.
				slog.Info(fmt.Sprintf("[Warning] Failback to primary region '%s' failed: %v", primaryRegion, failure.err))
				engine.LogEvent("warn", "failback_failed", map[string]any{
					"primary_region": primaryRegion,
					"staying_on":     fallbackRegion,
					"error":          failure.err.Error(),
				})
				for _, line := range failure.advice {
					slog.Info(line)
				}
				slog.Info(fmt.Sprintf("[Client] Staying on region '%s'; will retry the primary later.", fallbackRegion))
				cooldowns.exclude(primaryServerURL, regionFailoverCooldown)
				cfg.Region, cfg.ServerURL = fallbackRegion, fallbackServerURL
				failbackReturned = true
			}

			if len(cfg.Regions) > 0 {
				failedRegion := cfg.Region
				// Captured before rediscovery reassigns cfg.ServerURL.
				failedURL := cfg.ServerURL
				keepRegion := failbackReturned || leaseLost
				if keepRegion {
					// Nothing here failed, so no cooldown, no cache clear and no
					// rediscovery away from this region: leaving it a candidate is what
					// makes the "staying on '%s'" message above true.
					slog.Info(fmt.Sprintf("[Client] Re-establishing the session on region '%s' (%s)...", failedRegion, cfg.ServerURL))
				} else if plannedMove {
					// Nothing failed. The gateway told us it was stopping and we left
					// ahead of it, so this reads as a planned move rather than an
					// incident -- which matters when someone is reading these logs to
					// find out what went wrong (#1246).
					slog.Info(fmt.Sprintf("[Client] Moving off region '%s' (%s) before its scheduled stop: %s", failedRegion, cfg.ServerURL, plannedReason))
					engine.LogEvent("info", "planned_move_started", map[string]any{
						"leaving_region": failedRegion,
						"leaving_url":    cfg.ServerURL,
						"shutdown_at":    plannedAt,
						"reason":         plannedReason,
					})
					_ = client.ClearRegionCacheFile() //nolint:errcheck
				} else {
					slog.Info(fmt.Sprintf("[Client] Connection to region '%s' (%s) lost. Performing dynamic region failover...", failedRegion, cfg.ServerURL))
					failoverCause := "connection closed"
					if err != nil {
						failoverCause = err.Error()
					}
					engine.LogEvent("warn", "failover_started", map[string]any{
						"failed_region": failedRegion,
						"failed_url":    cfg.ServerURL,
						"cause":         failoverCause,
					})
					_ = client.ClearRegionCacheFile() //nolint:errcheck
				}
				// Keyed on the URL, not the region name: the same gateway is advertised
				// under several names, so excluding by name leaves it electable as its
				// alias (issue #1166).
				excludeFailedRegion(cfg, primaryRegionsMap, failedURL, keepRegion, plannedMove)

				if newResp, ok := reregisterAcrossRegions(cfg, regPortMappings, sub, engine.AddedHeaders); ok {
					applySession(newResp, "failover")
					if keepRegion {
						slog.Info(fmt.Sprintf("[Client] Session re-established on region '%s' (%s)", cfg.Region, cfg.ServerURL))
					} else {
						slog.Info(fmt.Sprintf("[Client] Successfully failed over to region '%s' (%s)", cfg.Region, cfg.ServerURL))
					}
					engine.LogEvent("info", "failover", map[string]any{
						"from":           failedRegion,
						"to":             cfg.Region,
						"url":            cfg.ServerURL,
						"after_failback": failbackReturned,
						"lease_lost":     leaseLost,
					})
					continue
				}

				slog.Info("[Error] Failover exhausted every candidate region without a successful registration.")
				engine.LogEvent("error", "failover_exhausted", map[string]any{
					"failed_region": failedRegion,
					"attempts":      maxFailoverAttempts,
				})
				break
			}
		}

		if err != nil {
			break
		}
	}

	if cleanupTUI != nil {
		cleanupTUI()
	}
	client.DeleteState(subHost)
	if err != nil && ctx.Err() == nil {
		log.Fatalf("[Client] Tunnel disconnected with error: %v", err)
	}
	slog.Info("[Client] Tunnel shutdown completed.")
}

func overrideConfigWithFlags(cfg *config.ClientConfig) {
	if *serverURL != "" {
		cfg.ServerURL = *serverURL
	}
	if *token != "" {
		cfg.AuthToken = *token
	}
	if *subdomain != "" {
		cfg.Subdomain = *subdomain
	}
	if *rateLimit > 0 {
		cfg.RateLimit = *rateLimit
	}
	if *basicAuth != "" {
		cfg.BasicAuth = *basicAuth
	}
	if *targetHost != "" {
		cfg.TargetHost = *targetHost
	}
	if *preserveHost {
		_ = os.Setenv("LFT_PRESERVE_HOST", "true") //nolint:errcheck
		cfg.PreserveHost = true
	}
	if *insecureSkipVerify {
		cfg.InsecureSkipVerify = true
	}
	if *passcode != "" {
		cfg.Passcode = *passcode
	}
	if *whitelistIP != "" {
		cfg.WhitelistIPs = *whitelistIP
	}
	if *themePref != "" {
		cfg.Theme = *themePref
	}
	if *region != "" {
		cfg.Region = *region
	}
	if *domain != "" {
		cfg.CustomDomain = *domain
	}
	if *latency > 0 {
		cfg.Latency = *latency
	}
	if *bandwidth != "" {
		cfg.Bandwidth = *bandwidth
	}
}

func executeSubcommands(cfg *config.ClientConfig, sub string, subdomainFlagPassed bool) bool {
	if len(os.Args) > 1 && os.Args[1] == "install-service" {
		if err := client.InstallService(); err != nil {
			log.Fatalf("[Error] Failed to install service: %v", err)
		}
		return true
	}

	if len(os.Args) > 1 && os.Args[1] == "install-gui-service" {
		if err := client.InstallGUIService(); err != nil {
			log.Fatalf("[Error] Failed to install GUI service: %v", err)
		}
		return true
	}

	if len(os.Args) > 1 && os.Args[1] == "uninstall-gui-service" {
		if err := client.UninstallGUIService(); err != nil {
			log.Fatalf("[Error] Failed to uninstall GUI service: %v", err)
		}
		return true
	}

	if *versionFlag {
		fmt.Printf("lfr-tunnel version %s\n", config.Version)
		return true
	}

	if *upgradeFlag {
		if err := client.SelfUpgrade(config.Version, cfg.ServerURL); err != nil {
			log.Fatalf("[Error] Upgrade failed: %v", err)
		}
		return true
	}

	if len(os.Args) > 1 && os.Args[1] == "login" {
		if err := client.RunLogin(cfg.ServerURL); err != nil {
			log.Fatalf("[Error] Login failed: %v", err)
		}
		return true
	}

	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		mcp.StartMCPServer()
		return true
	}

	if *checkVersionFlag {
		info, err := client.CheckServerCompatibility(cfg.ServerURL)
		if err != nil {
			log.Fatalf("[Error] Failed to check server compatibility: %v", err)
		}
		b, _ := json.Marshal(info)
		fmt.Println(string(b))
		return true
	}

	if *stop {
		handleStop(sub, subdomainFlagPassed)
		return true
	}

	if *status {
		handleStatus(sub, subdomainFlagPassed)
		return true
	}

	if *statusJSON {
		handleStatusJSON(sub, subdomainFlagPassed)
		return true
	}

	if *background {
		handleBackground(sub)
		return true
	}

	return false
}

func resolvePortsAndMappings(cfg *config.ClientConfig) []client.PortMapping {
	var ports []int
	var err error
	if *portsStr != "" {
		parts := strings.Split(*portsStr, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if p, err := strconv.Atoi(part); err == nil {
				ports = append(ports, p)
			}
		}
	} else {
		ports = cfg.Ports
	}

	var portMappings []client.PortMapping
	if len(ports) > 0 {
		for idx, port := range ports {
			suffix := ""
			if idx > 0 {
				// No leading dash: the separator is added when the suffix is joined to
				// the subdomain. Carrying one here produced ngriffin--58081 (#1154), and
				// left this inconsistent with client-extension suffixes, which never had
				// one.
				suffix = strconv.Itoa(port)
			}
			portMappings = append(portMappings, client.PortMapping{
				LocalPort:  port,
				NameSuffix: suffix,
			})
		}
	} else {
		if client.IsLiferayWorkspace(".") {
			slog.Info("[Client] Liferay workspace detected. Scanning for Client Extensions...")
			portMappings, err = client.DetectWorkspacePorts(".")
			if err != nil {
				slog.Info(fmt.Sprintf("[Warning] Failed to scan workspace: %v. Using defaults.", err))
				portMappings = []client.PortMapping{{LocalPort: 8080}}
			}
		} else {
			slog.Info("[Client] No explicit ports provided. Auto-discovering Liferay / LDM instances...")
			discoveryResult, err := client.AutoDiscoverTarget()
			if err == nil && discoveryResult != nil {
				slog.Info(fmt.Sprintf("[Client] Auto-discovered %s target on host '%s' with ports: %v", discoveryResult.Type, discoveryResult.Host, discoveryResult.Ports))

				// Automatically update the target host if it wasn't explicitly set via flags
				if *targetHost == "localhost" {
					cfg.TargetHost = discoveryResult.Host
				}

				for idx, port := range discoveryResult.Ports {
					suffix := ""
					if idx > 0 {
						// See above: the separator is added at join time (#1154).
						suffix = strconv.Itoa(port)
					}
					portMappings = append(portMappings, client.PortMapping{
						LocalPort:  port,
						NameSuffix: suffix,
					})
				}
			} else {
				slog.Info("[Client] No active LDM/Liferay instances discovered. Defaulting to port 8080.")
				portMappings = []client.PortMapping{{LocalPort: 8080}}
			}
		}
	}
	return portMappings
}

// maxFailoverAttempts bounds how many regions a single failover will try before
// giving up, so an outage affecting every edge terminates with one clear message
// instead of spinning.
const maxFailoverAttempts = 4

// failbackEvictionWindow is how soon after a failback an eviction is taken as evidence
// that the primary cannot actually carry the session, rather than as an unrelated fault.
// The health check runs every 5s, so anything inside a couple of cycles is the primary
// rejecting us rather than a coincidence.
const failbackEvictionWindow = 20 * time.Second

// failbackSuppression is how long the failback prober is held off after that happens.
//
// This is now a backstop, not the main defence. The prober asks the primary whether it can
// actually carry a session before moving (issue #1165), so the common case never reaches
// here. It still matters for a gateway too old to report control_plane, or an edge that is
// wrong about itself.
//
// It was five minutes, chosen to outlast a control-plane reconnect. That proved far too
// blunt in practice: an edge that recovered in seconds still cost a full five minutes
// parked on a worse region, which reads as the client being stuck. With prevention doing
// the real work, this only has to outlast a flapping reconnect.
const failbackSuppression = 45 * time.Second

// failoverRetryBackoff is the initial pause between failover registration attempts,
// doubled on each retry. A variable so tests don't have to sit through real backoff.
var failoverRetryBackoff = time.Second

// centralControlPlaneURL returns the control plane that edge sessions should also
// report their tunnel status to, taken from the gateway-advertised region list. There
// is deliberately no hardcoded fallback: a deployment that does not advertise a
// "central" region gets no second report target, rather than having this project's
// production gateway assumed on its behalf (issue #1124).
func centralControlPlaneURL(cfg *config.ClientConfig) string {
	if url, ok := cfg.Regions["central"]; ok {
		return url
	}
	return ""
}

// excludeFailedRegion holds the region that just failed out of the candidate set and
// points region discovery at a different host.
//
// The exclusion has to be a cooldown rather than a delete from cfg.Regions, because
// resolveServerURL refreshes that map from the gateway and would restore the entry
// (issue #1121). Discovery has to move off the failed edge too, or that edge is the
// source of truth for the region list it is supposed to be excluded from.
//
// afterFailback makes the whole thing a no-op. A failed failback returns the client to
// the region it was already serving from, which never failed -- the session was
// cancelled deliberately to try the primary. Cooling it down there abandoned a healthy
// edge, and with only two regions configured the "everything excluded" fallback in
// regionCooldowns.filter could then re-elect the primary whose failback had just
// failed -- the loop #1121 exists to prevent (issue #1137).
// plannedShutdownCooldown holds a gateway out of the candidate set after moving off it
// ahead of a scheduled stop (#1246). Far longer than regionFailoverCooldown, because that
// one is sized for a transient fault whereas this gateway is about to be deliberately
// unreachable for hours -- a 90s hold-off would re-elect it while it is still powered off,
// fail to register, and start the cycle again.
//
// A fixed value rather than the real downtime: the warning carries shutdown_at but no return
// time, so the client cannot yet know how long the stop lasts. An hour covers a typical
// window well enough to stop the churn; carrying the return time would let this be exact.
const plannedShutdownCooldown = time.Hour

func excludeFailedRegion(cfg *config.ClientConfig, primaryRegions map[string]string, failedURL string, afterFailback bool, planned bool) {
	if afterFailback {
		return
	}
	cooldown := regionFailoverCooldown
	if planned {
		cooldown = plannedShutdownCooldown
	}
	cooldowns.exclude(failedURL, cooldown)
	if discoveryURL := regionDiscoveryURL(cfg, primaryRegions, failedURL); discoveryURL != "" {
		cfg.ServerURL = discoveryURL
	}
}

// regionDiscoveryURL picks a host to fetch the region list from during failover, in
// preference order: the central control plane, then any gateway other than the one that
// just failed. Returns "" when no better host than the current one is known.
//
// Compared on host rather than region name. Names are not unique per gateway -- 'central'
// and 'eu' are the same host, as are 'in' and 'edge-in' -- so a name comparison happily
// returned the failed gateway under its other name, which is the very host being
// excluded (issue #1166).
func regionDiscoveryURL(cfg *config.ClientConfig, primaryRegions map[string]string, failedURL string) string {
	failedHost := gatewayHostKey(failedURL)
	for _, regions := range []map[string]string{cfg.Regions, primaryRegions} {
		if u, ok := regions["central"]; ok && u != "" && gatewayHostKey(u) != failedHost {
			return u
		}
	}
	for _, regions := range []map[string]string{cfg.Regions, primaryRegions} {
		for _, u := range regions {
			if u != "" && gatewayHostKey(u) != failedHost {
				return u
			}
		}
	}
	return ""
}

// reregisterAcrossRegions re-elects a region and registers on it, moving on to the
// next-best region when a registration fails for a reason another region could
// satisfy. Returns false only when every candidate has been tried, or when the failure
// is one no region can fix.
func reregisterAcrossRegions(cfg *config.ClientConfig, portMappings []client.PortMapping, sub string, addedHeaders map[string]string) (*client.RegisterResponse, bool) {
	backoff := failoverRetryBackoff
	for attempt := 1; attempt <= maxFailoverAttempts; attempt++ {
		cfg.Region = ""
		resolveServerURL(cfg, false)
		if cfg.ServerURL == "" {
			return nil, false
		}

		regResp, failure := attemptRegistration(cfg, portMappings, sub, addedHeaders)
		if failure == nil {
			return regResp, true
		}

		slog.Info(fmt.Sprintf("[Warning] Registration on region '%s' failed (attempt %d/%d): %v",
			cfg.Region, attempt, maxFailoverAttempts, failure.err))
		for _, line := range failure.advice {
			slog.Info(line)
		}

		if failure.terminal {
			// Another region would reject this identically.
			return nil, false
		}

		// Take this region out of the running and let the next pass elect a different
		// one. Without the cooldown, resolveServerURL would re-elect the same fastest
		// region every time and the retries would all hit the same broken gateway.
		cooldowns.exclude(cfg.ServerURL, regionFailoverCooldown)

		if attempt < maxFailoverAttempts {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return nil, false
}

// registrationFailure describes why a registration attempt failed and whether trying
// a different region could possibly help.
type registrationFailure struct {
	err error
	// terminal marks a failure no region can satisfy -- a reservation or account-limit
	// problem the user has to resolve in the portal. Retrying elsewhere just produces
	// the same rejection from a different host.
	terminal bool
	// advice is the operator-facing guidance to print, if any.
	advice []string
}

func (f *registrationFailure) Error() string { return f.err.Error() }

// attemptRegistration performs the registration call and classifies any failure. It
// never exits the process; callers decide what a failure means in their context. The
// initial startup registration has nowhere to fall back to and treats everything as
// fatal, whereas failover and failback are recovery paths that must survive a
// transient error and keep trying -- see issue #1120.
func attemptRegistration(cfg *config.ClientConfig, portMappings []client.PortMapping, sub string, addedHeaders map[string]string) (*client.RegisterResponse, *registrationFailure) {
	clientOS := runtime.GOOS
	if client.IsDocker() {
		clientOS += " (Docker)"
	}
	regResp, err := client.RegisterTunnel(cfg.ServerURL, cfg.AuthToken, sub, cfg.CustomDomain, portMappings, cfg.RateLimit, cfg.BasicAuth, addedHeaders, clientOS, cfg.Passcode, cfg.WhitelistIPs)
	if err != nil {
		if regErr, ok := err.(*client.RegistrationError); ok && regErr.StatusCode == 403 {
			portalURL := regErr.PortalURL
			if portalURL == "" {
				portalURL = strings.Replace(cfg.ServerURL, "tunnel.", "portal.", 1)
				if !strings.Contains(portalURL, "portal.") {
					portalURL = cfg.ServerURL + "/portal"
				}
			}
			return nil, &registrationFailure{
				err:      err,
				terminal: true,
				advice: []string{
					"[Client] Subdomain reservation or limit issue detected.",
					"[Client] Please visit the User Portal to resolve it:",
					fmt.Sprintf("         👉 %s (Cmd/Ctrl+Click to open)\n", portalURL),
				},
			}
		}

		errStr := err.Error()
		if strings.Contains(errStr, "registration request failed") ||
			strings.Contains(errStr, "gateway error (5") ||
			strings.Contains(errStr, "gateway returned status 5") {
			// A 5xx or transport error is the gateway's problem, not the user's, and
			// another region may well be healthy.
			return nil, &registrationFailure{
				err:    err,
				advice: append([]string{"[Client] Gateway appears to be offline or undergoing maintenance."}, statusPageAdvice()...),
			}
		}

		return nil, &registrationFailure{err: err}
	}

	if regResp.Warning != "" {
		slog.Info(fmt.Sprintf("\n[WARNING] %s\n\n", regResp.Warning))
	}
	return regResp, nil
}

// performRegistrationHandshake registers and exits the process on any failure. Only
// correct for the initial registration at startup, where there is no established
// tunnel to preserve and nothing to fall back to.
func performRegistrationHandshake(cfg *config.ClientConfig, portMappings []client.PortMapping, sub string, addedHeaders map[string]string) *client.RegisterResponse {
	regResp, failure := attemptRegistration(cfg, portMappings, sub, addedHeaders)
	if failure != nil {
		slog.Info(fmt.Sprintf("[Error] Failed to register: %v\n", failure.err))
		for _, line := range failure.advice {
			slog.Info(line)
		}
		os.Exit(1)
	}
	return regResp
}

func getPIDFilePath(subdomain string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".lfr-tunnel")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	safeSub := strings.ReplaceAll(subdomain, "/", "-")
	safeSub = strings.ReplaceAll(safeSub, "\\", "-")
	return filepath.Join(dir, fmt.Sprintf("lfr-tunnel-%s.pid", safeSub)), nil
}

func writePID(subdomain string, pid int) error {
	path, err := getPIDFilePath(subdomain)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0600)
}

func readPID(subdomain string) (int, error) {
	path, err := getPIDFilePath(subdomain)
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pidStr := strings.TrimSpace(string(data))
	return strconv.Atoi(pidStr)
}

func isPIDRunning(pid int) bool {
	return client.IsPIDRunning(pid)
}

func getActiveSubdomains() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".lfr-tunnel")
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var subs []string
	for _, f := range files {
		if !f.IsDir() && strings.HasPrefix(f.Name(), "lfr-tunnel-") && strings.HasSuffix(f.Name(), ".pid") {
			sub := strings.TrimPrefix(f.Name(), "lfr-tunnel-")
			sub = strings.TrimSuffix(sub, ".pid")
			subs = append(subs, sub)
		}
	}
	return subs, nil
}

func handleBackground(sub string) {
	isGUI := false
	var childArgs []string
	for _, arg := range os.Args[1:] {
		if arg == "-background" || arg == "--background" {
			continue
		}
		if arg == "-gui" || arg == "--gui" {
			isGUI = true
		}
		childArgs = append(childArgs, arg)
	}

	execPath, err := os.Executable()
	if err != nil {
		log.Fatalf("[Client] Failed to get executable path: %v\n", err)
	}

	if isGUI {
		cmd := osutil.BackgroundCommand(execPath, childArgs...)
		cmd.Dir = "."
		if err := cmd.Start(); err != nil {
			log.Fatalf("[Client] Failed to start GUI in background: %v\n", err)
		}
		fmt.Println("[Client] GUI started in background.")
		return
	}

	pid, err := readPID(sub)
	if err == nil && pid > 0 && isPIDRunning(pid) {
		log.Fatalf("[Client] A background tunnel for subdomain '%s' is already running (PID: %d). Stop it first using: lfr-tunnel -stop -subdomain %s\n", sub, pid, sub)
	}

	logPath, err := client.ClientLogPath(sub)
	if err != nil {
		log.Fatalf("[Client] Failed to resolve the log directory: %v\n", err)
	}

	// Rotated rather than truncated: this previously opened O_TRUNC, so every restart
	// destroyed the log of the run that had just ended -- the one worth reading after
	// an unexplained exit.
	//
	// Rotation is done separately from opening so that logFile stays a real *os.File.
	// os/exec passes an *os.File to the child as a file descriptor, but wraps any other
	// io.Writer in a pipe serviced by a goroutine in *this* process -- and this process
	// exits immediately after Start() for a detached background run, which would tear
	// the child's stdout down with it.
	if rerr := client.RotateFile(logPath, client.DefaultLogGenerations); rerr != nil {
		slog.Info(fmt.Sprintf("[Warning] Could not rotate the previous log: %v", rerr))
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		log.Fatalf("[Client] Failed to create log file: %v\n", err)
	}
	defer logFile.Close() //nolint:errcheck

	cmd := osutil.BackgroundCommand(execPath, childArgs...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Dir = "."

	if err := cmd.Start(); err != nil {
		log.Fatalf("[Client] Failed to start background process: %v\n", err)
	}

	if err := writePID(sub, cmd.Process.Pid); err != nil {
		slog.Info(fmt.Sprintf("[Warning] Failed to write PID file: %v\n", err))
	}

	slog.Info(fmt.Sprintf("[Client] Tunnel started in background for subdomain '%s' (PID: %d).\n", sub, cmd.Process.Pid))
	slog.Info(fmt.Sprintf("[Client] Logs: %s\n", logPath))
	slog.Info(fmt.Sprintf("[Client] To stop this tunnel, run: lfr-tunnel -stop -subdomain %s\n", sub))
}

func handleStop(sub string, targetSpecific bool) {
	var subsToStop []string
	if targetSpecific {
		subsToStop = []string{sub}
	} else {
		var err error
		subsToStop, err = getActiveSubdomains()
		if err != nil {
			log.Fatalf("[Error] Failed to read active subdomains: %v\n", err)
		}
		if len(subsToStop) == 0 {
			slog.Info("[Client] No active background tunnels found.")
			return
		}
	}

	for _, s := range subsToStop {
		pid, err := readPID(s)
		if err != nil || pid <= 0 {
			if targetSpecific {
				slog.Info(fmt.Sprintf("[Client] No background tunnel is active for subdomain '%s'.\n", s))
			}
			continue
		}
		if !isPIDRunning(pid) {
			slog.Info(fmt.Sprintf("[Client] Stale PID file found for subdomain '%s'. Process %d is not running. Cleaning up...\n", s, pid))
			pidPath, _err := getPIDFilePath(s)
			_ = _err               //nolint:errcheck
			_ = os.Remove(pidPath) //nolint:errcheck
			client.DeleteState(s)
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			slog.Info(fmt.Sprintf("[Warning] Failed to find process for subdomain '%s': %v\n", s, err))
			continue
		}

		slog.Info(fmt.Sprintf("[Client] Stopping background tunnel for subdomain '%s' (PID: %d)...\n", s, pid))
		_ = proc.Signal(syscall.SIGINT) //nolint:errcheck

		for i := 0; i < 10; i++ {
			time.Sleep(200 * time.Millisecond)
			if !isPIDRunning(pid) {
				break
			}
		}

		if isPIDRunning(pid) {
			slog.Info(fmt.Sprintf("[Client] Process %d did not respond to SIGINT. Force terminating...\n", pid))
			_ = proc.Kill() //nolint:errcheck
		}
		pidPath, _err := getPIDFilePath(s)
		_ = _err               //nolint:errcheck
		_ = os.Remove(pidPath) //nolint:errcheck
		client.DeleteState(s)
		slog.Info(fmt.Sprintf("[Client] Tunnel for subdomain '%s' stopped.\n", s))
	}
}

func handleStatus(sub string, targetSpecific bool) {
	var subsToCheck []string
	if targetSpecific {
		subsToCheck = []string{sub}
	} else {
		var err error
		subsToCheck, err = getActiveSubdomains()
		if err != nil {
			log.Fatalf("[Error] Failed to read active subdomains: %v\n", err)
		}
		if len(subsToCheck) == 0 {
			slog.Info("[Client] No active background tunnels found.")
			return
		}
	}

	for _, s := range subsToCheck {
		pid, err := readPID(s)
		if err != nil || pid <= 0 {
			if targetSpecific {
				slog.Info(fmt.Sprintf("[Client] No background tunnel is active for subdomain '%s'.\n", s))
			}
			continue
		}
		if isPIDRunning(pid) {
			slog.Info(fmt.Sprintf("[Client] Background tunnel for subdomain '%s' is active (PID: %d).\n", s, pid))
			home, _ := os.UserHomeDir()
			slog.Info(fmt.Sprintf("[Client] Logs: %s\n", filepath.Join(home, ".lfr-tunnel", fmt.Sprintf("client-%s.log", s))))
		} else {
			slog.Info(fmt.Sprintf("[Client] No background tunnel is active for subdomain '%s' (found stale PID file). Cleaning up...\n", s))
			pidPath, _err := getPIDFilePath(s)
			_ = _err               //nolint:errcheck
			_ = os.Remove(pidPath) //nolint:errcheck
			client.DeleteState(s)
		}
	}
}

func handleStatusJSON(sub string, targetSpecific bool) {
	if targetSpecific {
		statePath, err := client.GetStateFilePath(sub)
		if err != nil {
			fmt.Println(`{"running":false}`)
			return
		}
		bytes, err := client.QueryStatusJSON(statePath, isPIDRunning)
		if err != nil {
			fmt.Println(`{"running":false}`)
			return
		}
		fmt.Println(string(bytes))
		return
	}

	// Global query: print aggregated status list
	subs, err := getActiveSubdomains()
	if err != nil || len(subs) == 0 {
		fmt.Println(`{"tunnels":[]}`)
		return
	}

	type Response struct {
		Tunnels []json.RawMessage `json:"tunnels"`
	}
	resp := Response{Tunnels: []json.RawMessage{}}

	for _, s := range subs {
		statePath, err := client.GetStateFilePath(s)
		if err != nil {
			continue
		}
		bytes, err := client.QueryStatusJSON(statePath, isPIDRunning)
		if err == nil {
			resp.Tunnels = append(resp.Tunnels, json.RawMessage(bytes))
		}
	}

	outputBytes, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		fmt.Println(`{"tunnels":[]}`)
		return
	}
	fmt.Println(string(outputBytes))
}

type RegionCacheData struct {
	BestRegion string    `json:"best_region"`
	ServerURL  string    `json:"server_url"`
	Timestamp  time.Time `json:"timestamp"`
	// Provisional marks an election made while one or more advertised regions did not
	// answer. That choice is the best of what was reachable, not the best available, so
	// it expires quickly rather than pinning the client for a day (issue #1148).
	Provisional bool `json:"provisional,omitempty"`
}

// regionCacheTTL is how long a complete election is trusted.
const regionCacheTTL = 24 * time.Hour

// provisionalRegionCacheTTL is how long an election made with regions missing is trusted.
// Short enough that a client started while an edge sat in its power-off window re-probes
// within the working day instead of staying on a distant region until tomorrow.
const provisionalRegionCacheTTL = 30 * time.Minute

func getRegionCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".lfr-tunnel")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "region_cache.json"), nil
}

func loadRegionCache() *RegionCacheData {
	path, err := getRegionCachePath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cache RegionCacheData
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil
	}
	ttl := regionCacheTTL
	if cache.Provisional {
		ttl = provisionalRegionCacheTTL
	}
	if time.Since(cache.Timestamp) > ttl {
		return nil
	}
	return &cache
}

func saveRegionCache(bestRegion, serverURL string, provisional bool) {
	path, err := getRegionCachePath()
	if err != nil {
		return
	}
	cache := RegionCacheData{
		BestRegion:  bestRegion,
		ServerURL:   serverURL,
		Timestamp:   time.Now(),
		Provisional: provisional,
	}
	bytes, err := json.Marshal(cache)
	if err == nil {
		_ = os.WriteFile(path, bytes, 0600) //nolint:errcheck
	}
}

// Indirection seams for resolveServerURL's two side-effecting dependencies: a live
// HTTP call to the gateway, and a write to the user's real home directory. Tests
// swap these so region resolution can be exercised without production being reachable
// (or being mutated). Production always runs the real implementations.
var (
	fetchRemoteRegionsFn = fetchRemoteRegions
	saveRegionCacheFn    = saveRegionCache
)

// regionFailoverCooldown is how long a region we have just failed away from stays out
// of the candidate set. It has to outlast the control plane's own lease cleanup sweep
// (documented at 10s in docs/architecture.md section 5), otherwise the edge can still
// be advertising a stale lease for this subdomain when we reconsider it.
const regionFailoverCooldown = 90 * time.Second

// gatewayHostKey reduces a gateway URL to the host that identifies it, so aliases collapse
// onto one entry.
//
// The gateway advertises every region under more than one name -- 'central' and 'eu' are
// both https://tunnel.lfr-demo.se, 'in' and 'edge-in' are both the Mumbai edge. Keying
// anything on the region *name* therefore treats one gateway as several places: a cooldown
// on 'in' left 'edge-in' electable, and a failover from 'in' to 'eu' and then to 'central'
// looked like two moves while never leaving the same host (issue #1166).
func gatewayHostKey(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	u, err := url.Parse(strings.TrimRight(trimmed, "/"))
	if err != nil || u.Hostname() == "" {
		return strings.ToLower(trimmed)
	}

	host := strings.ToLower(u.Hostname())
	// The port is part of the identity: two gateways can share a hostname and differ
	// only by port, which is the normal shape for local and test deployments. A default
	// port is dropped so https://x and https://x:443 are recognised as one gateway.
	port := u.Port()
	if port == "" || (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		return host
	}
	return host + ":" + port
}

// cooldowns records gateways that recently failed, keyed by host. Exclusion cannot be done
// by deleting from cfg.Regions, because resolveServerURL refreshes that map from the
// gateway on every call and would restore the entry -- see issue #1121.
var cooldowns = &regionCooldowns{until: make(map[string]time.Time)}

type regionCooldowns struct {
	mu    sync.Mutex
	until map[string]time.Time
}

// exclude puts the gateway at serverURL into cooldown for d, covering every region name
// that resolves to it.
func (c *regionCooldowns) exclude(serverURL string, d time.Duration) {
	host := gatewayHostKey(serverURL)
	if host == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.until[host] = time.Now().Add(d)
}

// coolingDown reports whether a gateway is still being held out of the candidate set, and for
// how much longer.
//
// Failover set cooldowns from the start; failback never consulted them, so a client moved off
// its primary by a shutdown warning was dragged straight back the moment that gateway answered
// (#1310). For a scheduled stop the node is genuinely powered off, so the probe failed for
// hours and the hour-long plannedShutdownCooldown looked like it was doing the work -- it was
// not, the node being down was. A drain (#1303) brings the gateway back in seconds, and the
// return trip costs a re-registration and a chisel reconnect: exactly the interruption the
// drain exists to avoid.
//
// Expired entries are left for filter() to reap rather than deleted here, so this stays a
// read and can be called from the probe loop without contending on writes.
func (c *regionCooldowns) coolingDown(serverURL string) (bool, time.Duration) {
	host := gatewayHostKey(serverURL)
	if host == "" {
		return false, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	deadline, ok := c.until[host]
	if !ok {
		return false, 0
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return false, 0
	}
	return true, remaining
}

// clear drops any cooldown on a gateway, used once we are successfully connected to it
// again so a later failure starts from a clean slate.
func (c *regionCooldowns) clear(serverURL string) {
	host := gatewayHostKey(serverURL)
	if host == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.until, host)
}

// filter returns regions whose gateway is still in cooldown removed. If that would leave
// nothing to connect to, the original map is returned unchanged: retrying a gateway that
// recently failed is strictly better than having none at all.
func (c *regionCooldowns) filter(regions map[string]string) map[string]string {
	if len(regions) == 0 {
		return regions
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	out := make(map[string]string, len(regions))
	for name, regionURL := range regions {
		host := gatewayHostKey(regionURL)
		if deadline, ok := c.until[host]; ok {
			if now.Before(deadline) {
				continue
			}
			delete(c.until, host)
		}
		out[name] = regionURL
	}
	if len(out) == 0 {
		return regions
	}
	return out
}

// firstNonEmpty returns the first value that isn't blank, which is how every client
// setting picks between flag, environment variable and config file.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// statusPageAdvice returns the "check the status page" lines, or nothing at all when this
// build has no status page configured (#1188).
//
// Pointing someone at a status page for an unrelated service while they debug their own
// outage is worse than staying quiet, so a build with no DefaultStatusPageURL simply omits
// the suggestion rather than printing a bare arrow.
func statusPageAdvice() []string {
	if config.DefaultStatusPageURL == "" {
		return nil
	}
	return []string{
		"[Client] Check the service status page for active outages:",
		fmt.Sprintf("         👉 %s (Cmd/Ctrl+Click to open)", config.DefaultStatusPageURL),
	}
}

func resolveServerURL(cfg *config.ClientConfig, isExplicitServer bool) {
	if *refreshRegion {
		if path, err := getRegionCachePath(); err == nil {
			_ = os.Remove(path) //nolint:errcheck
		}
	}

	fetchRemoteRegionsFn(cfg)

	// Applied after the refresh, not before: fetchRemoteRegions replaces cfg.Regions
	// wholesale, so anything removed beforehand comes straight back.
	cfg.Regions = cooldowns.filter(cfg.Regions)

	if cfg.Region == "" {
		if !isExplicitServer && len(cfg.Regions) > 0 {
			if !*refreshRegion {
				if cached := loadRegionCache(); cached != nil && cached.BestRegion != "" {
					if url, ok := cfg.Regions[cached.BestRegion]; ok {
						cfg.Region = cached.BestRegion
						cfg.ServerURL = url
						slog.Info(fmt.Sprintf("[Client] Using cached best region: '%s' -> %s (cached for 24h, use -refresh-region to re-probe)", cfg.Region, cfg.ServerURL))
						return
					}
				}
			}

			slog.Info(fmt.Sprintf("[Client] No region specified. Performing latency auto-probing across %d regions...", len(cfg.Regions)))
			bestRegion, unreachable := probeFastestRegion(cfg.Regions)
			if bestRegion != "" {
				cfg.Region = bestRegion
				cfg.ServerURL = cfg.Regions[bestRegion]
				saveRegionCacheFn(bestRegion, cfg.ServerURL, len(unreachable) > 0)
				if len(unreachable) > 0 {
					slog.Info(fmt.Sprintf("[Client] Auto-detected best reachable region: '%s' -> %s. %d region(s) did not answer (%s), so this choice is provisional and is re-probed within %s.",
						bestRegion, cfg.ServerURL, len(unreachable), strings.Join(unreachable, ", "), provisionalRegionCacheTTL))
				} else {
					slog.Info(fmt.Sprintf("[Client] Auto-detected best region: '%s' -> %s (cached for %s)", bestRegion, cfg.ServerURL, regionCacheTTL))
				}
			}
		}
		return
	}

	regionLower := strings.TrimSpace(strings.ToLower(cfg.Region))
	if url, ok := cfg.Regions[regionLower]; ok {
		cfg.ServerURL = url
		slog.Info(fmt.Sprintf("[Client] Selected region '%s' -> %s", regionLower, url))
	} else {
		if len(cfg.Regions) > 0 {
			slog.Info(fmt.Sprintf("[Client] Specified region '%s' is currently unavailable or offline. Performing latency auto-probing across %d active regions...", regionLower, len(cfg.Regions)))
			bestRegion, unreachable := probeFastestRegion(cfg.Regions)
			if bestRegion != "" {
				cfg.Region = bestRegion
				cfg.ServerURL = cfg.Regions[bestRegion]
				saveRegionCacheFn(bestRegion, cfg.ServerURL, len(unreachable) > 0)
				slog.Info(fmt.Sprintf("[Client] Auto-selected next best online region: '%s' -> %s", bestRegion, cfg.ServerURL))
				return
			}
		}
		slog.Info(fmt.Sprintf("[Client] Warning: Region '%s' unavailable and no active edge nodes found. Using default server URL: %s", regionLower, cfg.ServerURL))
	}
}

func fetchRemoteRegions(cfg *config.ClientConfig) {
	client := &http.Client{
		Timeout: 2 * time.Second,
	}
	apiURL := strings.TrimRight(cfg.ServerURL, "/") + "/api/version"
	resp, err := client.Get(apiURL)
	if err != nil {
		return // Silently fall back to built-in defaults
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	if resp.StatusCode == http.StatusOK {
		var payload struct {
			Regions map[string]string `json:"regions"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil && len(payload.Regions) > 0 {
			cfg.Regions = payload.Regions
		}
	}
}

// dedupeRegionsByHost collapses region names that resolve to the same gateway, keeping one
// name per host.
//
// Which name survives is user-visible -- it becomes the region shown in the TUI and written
// to the state file -- so the choice is deliberate rather than map-order luck: prefer the
// shorter name, and the alphabetically earlier one to break ties. That keeps the familiar
// 'in' and 'central' rather than 'edge-in' and 'eu'.
func dedupeRegionsByHost(regions map[string]string) map[string]string {
	if len(regions) < 2 {
		return regions
	}
	best := make(map[string]string, len(regions)) // host -> chosen name
	for name, regionURL := range regions {
		host := gatewayHostKey(regionURL)
		if host == "" {
			continue
		}
		current, seen := best[host]
		if !seen || len(name) < len(current) || (len(name) == len(current) && name < current) {
			best[host] = name
		}
	}

	out := make(map[string]string, len(best))
	for _, name := range best {
		out[name] = regions[name]
	}
	return out
}

// probeFastestRegion elects the lowest-RTT region and reports which regions did not
// answer at all. The unreachable list matters: an election made while some regions were
// down is provisional, and caching it for the full 24h strands the client on a worse
// region long after the better one returns (issue #1148).
func probeFastestRegion(regions map[string]string) (string, []string) {
	// One probe per gateway, not per name. The gateway advertises each region twice
	// ('in' and 'edge-in' are the same host), so probing by name doubled the health
	// requests and ranked a host against itself on RTT noise (issue #1166).
	regions = dedupeRegionsByHost(regions)

	type probeResult struct {
		region string
		url    string
		rtt    time.Duration
	}
	ch := make(chan probeResult, len(regions))
	// Named to leave the imported client package reachable inside the probe goroutine.
	probeClient := &http.Client{
		Timeout: 1500 * time.Millisecond,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	var wg sync.WaitGroup
	for reg, u := range regions {
		wg.Add(1)
		go func(r, targetURL string) {
			defer wg.Done()
			start := time.Now()
			resp, err := probeClient.Get(targetURL + "/api/healthz")
			if err == nil {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 512)) //nolint:errcheck
				_ = resp.Body.Close()                                 //nolint:errcheck
				// A 200 alone is not enough: an edge that cannot reach central still
				// answers healthz but will evict any session that lands there. Electing
				// on the status code is how a client ends up on an edge that throws it
				// off five seconds later (issue #1165).
				if client.GatewayCanCarrySession(resp.StatusCode, body) {
					rtt := time.Since(start)
					ch <- probeResult{region: r, url: targetURL, rtt: rtt}
				}
			}
		}(reg, u)
	}

	wg.Wait()
	close(ch)

	var results []probeResult
	for res := range ch {
		results = append(results, res)
	}

	answered := make(map[string]bool, len(results))
	for _, r := range results {
		answered[r.region] = true
	}
	var unreachable []string
	for reg := range regions {
		if !answered[reg] {
			unreachable = append(unreachable, reg)
		}
	}
	sort.Strings(unreachable)

	if len(results) == 0 {
		return "", unreachable
	}

	best := results[0]
	fmt.Println("[Client] Region latency probe results:")
	for _, r := range results {
		fmt.Printf("  - %s: %v\n", r.region, r.rtt)
		if r.rtt < best.rtt {
			best = r
		}
	}
	for _, reg := range unreachable {
		fmt.Printf("  - %s: no response\n", reg)
	}

	return best.region, unreachable
}

func rewriteRemotes(regResp *client.RegisterResponse, portMap map[int]int) {
	for idx, remote := range regResp.Remotes {
		parts := strings.Split(remote, ":")
		if len(parts) >= 4 {
			lastPart := parts[len(parts)-1]
			if targetP, err := strconv.Atoi(lastPart); err == nil {
				if intP, exists := portMap[targetP]; exists {
					parts[len(parts)-1] = strconv.Itoa(intP)
					regResp.Remotes[idx] = strings.Join(parts, ":")
				}
			}
		}
	}
}

func printAndCollectPublicURLs(cfg *config.ClientConfig, regResp *client.RegisterResponse, portMappings []client.PortMapping, subHost string) []string {
	var publicURLs []string
	slog.Info("[Client] Registration successful! Your public tunnel URLs are:")
	for _, domain := range regResp.Domains {
		for _, pm := range portMappings {
			var urlStr string
			if subHost == "" {
				if pm.NameSuffix == "" {
					urlStr = fmt.Sprintf("https://%s", domain)
				} else {
					cleanSuffix := strings.TrimPrefix(pm.NameSuffix, "-")
					urlStr = fmt.Sprintf("https://%s.%s", cleanSuffix, domain)
				}
			} else {
				var fullSubdomain string
				if pm.NameSuffix == "" {
					fullSubdomain = subHost
				} else {
					fullSubdomain = fmt.Sprintf("%s-%s", subHost, pm.NameSuffix)
				}
				urlStr = fmt.Sprintf("https://%s.%s", fullSubdomain, domain)
			}
			publicURLs = append(publicURLs, urlStr)
			slog.Info(fmt.Sprintf("  %s -> local port %d", urlStr, pm.LocalPort))
		}
	}
	return publicURLs
}
