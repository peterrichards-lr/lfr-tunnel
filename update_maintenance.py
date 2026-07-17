import re
import json

def update_file(path, replacements):
    with open(path, 'r') as f:
        content = f.read()
    
    for old, new in replacements:
        if old not in content:
            print(f"Warning: {old[:50]}... not found in {path}")
        content = content.replace(old, new)
        
    with open(path, 'w') as f:
        f.write(content)
    print(f"Updated {path}")

# 1. Update config.go
config_go_old = """	Ports              []int             `yaml:"ports"`
	TokenFile          string            `yaml:"token_file"`"""
config_go_new = """	Ports              []int             `yaml:"ports"`
	TokenFile          string            `yaml:"token_file"`
	MaintenancePath    string            `yaml:"maintenance_path"`"""
update_file('pkg/config/config.go', [(config_go_old, config_go_new)])


# 2. Update interceptor.go
interceptor_go_old = """	DestPort           int
	LatencyHistory     []int64
	mu                 sync.RWMutex
}"""
interceptor_go_new = """	DestPort           int
	LatencyHistory     []int64
	MaintenancePath    string
	mu                 sync.RWMutex
}"""

interceptor_go_func_old = """func serveMaintenancePage(w http.ResponseWriter) {
	w.WriteHeader(http.StatusServiceUnavailable)
	w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
	<title>Maintenance Mode</title>
	<style>
		body { font-family: -apple-system, sans-serif; background: #0f172a; color: white; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; }
		.card { background: rgba(30, 41, 59, 0.7); padding: 40px; border-radius: 12px; border: 1px solid rgba(255,255,255,0.1); text-align: center; }
		h1 { margin-top: 0; color: #f59e0b; }
	</style>
</head>
<body>
	<div class="card">
		<h1>Down for Maintenance</h1>
		<p>The developer has temporarily paused this tunnel for maintenance. Please check back shortly.</p>
	</div>
</body>
</html>`))
}"""

interceptor_go_func_new = """func serveMaintenancePage(w http.ResponseWriter, path string) {
	if path != "" {
		if content, err := os.ReadFile(path); err == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write(content)
			return
		}
	}
	
	w.WriteHeader(http.StatusServiceUnavailable)
	w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
	<title>Developer Maintenance Mode</title>
	<style>
		body { font-family: -apple-system, sans-serif; background: linear-gradient(135deg, #0f172a 0%, #1e1b4b 50%, #311042 100%); color: white; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; }
		.card { background: rgba(30, 41, 59, 0.7); padding: 48px 32px; border-radius: 24px; border: 1px solid rgba(255,255,255,0.08); text-align: center; max-width: 520px; box-shadow: 0 20px 40px rgba(0, 0, 0, 0.3); }
		h1 { margin-top: 0; color: #38bdf8; font-size: 28px; font-weight: 800; }
		p { color: #94a3b8; font-size: 16px; line-height: 1.6; }
		.logo-container { margin-bottom: 24px; display: inline-flex; align-items: center; justify-content: center; width: 80px; height: 80px; border-radius: 20px; background: rgba(255, 255, 255, 0.03); border: 1px solid rgba(255, 255, 255, 0.05); }
	</style>
</head>
<body>
	<div class="card">
		<div class="logo-container">
			<svg width="44" height="44" viewBox="0 0 24 24" fill="white"><path d="M12 2L2 22h20L12 2zm0 3.8l7.5 14.2H4.5L12 5.8z"/></svg>
		</div>
		<h1>Developer Maintenance</h1>
		<p>The developer has temporarily paused this tunnel for maintenance. Please check back shortly.</p>
	</div>
</body>
</html>`))
}"""

update_file('pkg/client/interceptor.go', [
    (interceptor_go_old, interceptor_go_new),
    ("serveMaintenancePage(w)", "serveMaintenancePage(w, e.MaintenancePath)"),
    (interceptor_go_func_old, interceptor_go_func_new)
])

# Wait, `serveMaintenancePage(w)` might be called multiple times.
# Let's fix that. Oh, I did replace "serveMaintenancePage(w)" with "serveMaintenancePage(w, e.MaintenancePath)".

# 3. Update main.go
main_go_old = """	engine := client.NewInterceptorEngine(cfg.TargetHost, addHeaders)"""
main_go_new = """	engine := client.NewInterceptorEngine(cfg.TargetHost, addHeaders)
	engine.MaintenancePath = cfg.MaintenancePath"""
update_file('cmd/lfr-tunnel/main.go', [(main_go_old, main_go_new)])

# 4. Update inspector.go
inspector_go_get_old = """				"passcode":             cfg.Passcode,
				"rate_limit":           cfg.RateLimit,"""
inspector_go_get_new = """				"passcode":             cfg.Passcode,
				"rate_limit":           cfg.RateLimit,
				"maintenance_path":     cfg.MaintenancePath,"""
inspector_go_post_struct_old = """				Passcode           string `json:"passcode"`
				RateLimit          int    `json:"rate_limit"`
			}"""
inspector_go_post_struct_new = """				Passcode           string `json:"passcode"`
				RateLimit          int    `json:"rate_limit"`
				MaintenancePath    string `json:"maintenance_path"`
			}"""
inspector_go_post_assign_old = """			cfg.PreserveHost = req.PreserveHost
			cfg.InsecureSkipVerify = req.InsecureSkipVerify
			cfg.Passcode = req.Passcode
			cfg.RateLimit = req.RateLimit

			err = config.SaveClientConfig("", cfg)"""
inspector_go_post_assign_new = """			cfg.PreserveHost = req.PreserveHost
			cfg.InsecureSkipVerify = req.InsecureSkipVerify
			cfg.Passcode = req.Passcode
			cfg.RateLimit = req.RateLimit
			cfg.MaintenancePath = req.MaintenancePath
			engine.MaintenancePath = req.MaintenancePath

			err = config.SaveClientConfig("", cfg)"""
update_file('pkg/client/inspector.go', [
    (inspector_go_get_old, inspector_go_get_new),
    (inspector_go_post_struct_old, inspector_go_post_struct_new),
    (inspector_go_post_assign_old, inspector_go_post_assign_new)
])


# 5. Update inspector.html
inspector_html_input_old = """                                        <label style="font-weight: 500; color: var(--text-muted); font-size: 11px;" data-i18n="client_insecure_ssl" data-i18n-help="client_insecure_ssl_help">Allow insecure local SSL (Skip TLS Verification) <span class="help-icon" title="If checking against an HTTPS port (e.g. 443), this bypasses strict certificate checks. Required for most self-signed localhost certificates.">?</span></label>
                                        </div>
                                    </div>"""
inspector_html_input_new = """                                        <label style="font-weight: 500; color: var(--text-muted); font-size: 11px;" data-i18n="client_insecure_ssl" data-i18n-help="client_insecure_ssl_help">Allow insecure local SSL (Skip TLS Verification) <span class="help-icon" title="If checking against an HTTPS port (e.g. 443), this bypasses strict certificate checks. Required for most self-signed localhost certificates.">?</span></label>
                                        </div>
                                    </div>
                                    
                                    <div style="display: flex; flex-direction: column; gap: 6px; margin-top: 5px;">
                                        <label style="font-weight: 500; color: var(--text-muted); font-size: 11px;">Custom Maintenance Page <span class="help-icon" title="Optional path to a custom HTML file to show when maintenance mode is enabled.">?</span></label>
                                        <input type="text" id="cfg-maintenance-path" placeholder="/path/to/custom-maintenance.html" style="background: rgba(0,0,0,0.3); border: 1px solid var(--border); color: white; padding: 10px; border-radius: 6px; font-size: 13px; outline: none; width: 100%;">
                                    </div>"""
inspector_html_set_old = """                document.getElementById('cfg-preserve-host').checked = cfg.preserve_host || false;
                document.getElementById('cfg-insecure-skip-verify').checked = cfg.insecure_skip_verify || false;"""
inspector_html_set_new = """                document.getElementById('cfg-preserve-host').checked = cfg.preserve_host || false;
                document.getElementById('cfg-insecure-skip-verify').checked = cfg.insecure_skip_verify || false;
                document.getElementById('cfg-maintenance-path').value = cfg.maintenance_path || '';"""
inspector_html_save_old = """                passcode: "",
                rate_limit: 0
            };"""
inspector_html_save_new = """                passcode: "",
                rate_limit: 0,
                maintenance_path: document.getElementById('cfg-maintenance-path').value
            };"""

update_file('pkg/client/inspector.html', [
    (inspector_html_input_old, inspector_html_input_new),
    (inspector_html_set_old, inspector_html_set_new),
    (inspector_html_save_old, inspector_html_save_new)
])


