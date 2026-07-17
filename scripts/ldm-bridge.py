import re

with open('pkg/gui/gui.go', 'r') as f:
    go = f.read()

# Add LDM Project tracking struct and global
struct_injection = """
// LDM Integration Bridge
type LDMProject struct {
	Name       string `json:"project"`
	Status     string `json:"status"`
	TargetPort int    `json:"target_port"`
	PublicURL  string `json:"public_url"`
	MenuItem   *systray.MenuItem `json:"-"`
}

var (
	ldmProjects   = make(map[string]*LDMProject)
	ldmMenuParent *systray.MenuItem
	ldmMutex      sync.Mutex
)

// StartLDMBridgeServer starts a local HTTP server on 127.0.0.1:4141 to receive updates from LDM
func StartLDMBridgeServer() {
	mux := http.NewServeMux()
	
	mux.HandleFunc("/api/tunnels/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var payload struct {
				Source     string `json:"source"`
				Project    string `json:"project"`
				Status     string `json:"status"`
				TargetPort int    `json:"target_port"`
				PublicURL  string `json:"public_url"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			
			ldmMutex.Lock()
			defer ldmMutex.Unlock()
			
			if ldmMenuParent == nil {
				// Should have been initialized in onReady, but safety check
				http.Error(w, "GUI not ready", http.StatusServiceUnavailable)
				return
			}
			
			proj, exists := ldmProjects[payload.Project]
			if !exists {
				proj = &LDMProject{
					Name:       payload.Project,
					Status:     payload.Status,
					TargetPort: payload.TargetPort,
					PublicURL:  payload.PublicURL,
				}
				
				// Create sub-menu item
				title := fmt.Sprintf("LDM: %s (%s)", payload.Project, payload.PublicURL)
				item := ldmMenuParent.AddSubMenuItem(title, "Manage LDM Project")
				proj.MenuItem = item
				
				go func(p *LDMProject) {
					for {
						<-p.MenuItem.ClickedCh
						// Execute LDM CLI Subprocess instead of local Go binary tunnel
						cmd := exec.Command("ldm", "share", "start", p.Name, "--provider", "lfr-tunnel")
						if err := cmd.Start(); err != nil {
							slog.Error("[LDM Bridge] Failed to execute LDM CLI", "project", p.Name, "error", err)
						}
					}
				}(proj)
				
				ldmProjects[payload.Project] = proj
				ldmMenuParent.Show()
			} else {
				proj.Status = payload.Status
				proj.TargetPort = payload.TargetPort
				proj.PublicURL = payload.PublicURL
				title := fmt.Sprintf("LDM: %s (%s)", payload.Project, payload.PublicURL)
				proj.MenuItem.SetTitle(title)
			}
			
			// If at least one active, set icon green
			systray.SetIcon(getIcon("active"))
			w.WriteHeader(http.StatusOK)
			
		} else if r.Method == http.MethodDelete {
			project := r.URL.Query().Get("project")
			if project == "" {
				http.Error(w, "Missing project parameter", http.StatusBadRequest)
				return
			}
			
			ldmMutex.Lock()
			defer ldmMutex.Unlock()
			
			if proj, exists := ldmProjects[project]; exists {
				proj.MenuItem.Hide()
				delete(ldmProjects, project)
			}
			
			if len(ldmProjects) == 0 && currentTunnelState != "active" {
				systray.SetIcon(getIcon("idle"))
				ldmMenuParent.Hide()
			}
			w.WriteHeader(http.StatusOK)
			
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	server := &http.Server{
		Addr:    "127.0.0.1:4141",
		Handler: mux,
	}
	
	slog.Info("[LDM Bridge] Starting local API server on 127.0.0.1:4141")
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("[LDM Bridge] Server failed", "error", err)
		}
	}()
}
"""

go = re.sub(r'(var \(\n\tcurrentTunnelState = "idle"\n\))', r'\1\n\n' + struct_injection, go, count=1)
if struct_injection not in go: # Fallback if regex fails
    go = go.replace('var currentTunnelState = "idle"', 'var currentTunnelState = "idle"\n\n' + struct_injection)

# Add init logic in onReady
on_ready_injection = """
	// Initialize LDM Projects Menu
	ldmMutex.Lock()
	ldmMenuParent = systray.AddMenuItem("Active Managed Projects", "Projects managed by LDM")
	ldmMenuParent.Hide()
	ldmMutex.Unlock()
	systray.AddSeparator()

	// Start Local API Bridge
	StartLDMBridgeServer()
"""

go = go.replace('systray.SetTooltip("Liferay Tunnel")', 'systray.SetTooltip("Liferay Tunnel")\n' + on_ready_injection)

with open('pkg/gui/gui.go', 'w') as f:
    f.write(go)
print("LDM bridge injected into gui.go")
