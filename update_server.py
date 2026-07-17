import re

with open('pkg/server/server.go', 'r') as f:
    content = f.read()

# Add dashboard.html string variable
content = content.replace(
    '//go:embed static/*',
    '//go:embed dashboard.html\nvar dashboardHTML string\n\n//go:embed static/*'
)

# Update routing block
old_routing = """		// Catch-all SPA routing for Control Plane GET requests
		if r.Method == http.MethodGet {
			subFS, err := fs.Sub(uiDistFS, "ui-dist")
			if err == nil {
				cleanPath := strings.TrimPrefix(r.URL.Path, "/")
				if cleanPath == "" {
					cleanPath = "index.html"
				}

				// Check if the file exists in the embedded FS
				f, err := subFS.Open(cleanPath)
				if err != nil {
					// SPA Fallback: Serve index.html for client-side routing
					r.URL.Path = "/"
				} else {
					_ = f.Close() //nolint:errcheck
				}

				if strings.HasSuffix(cleanPath, ".html") || cleanPath == "index.html" {
					w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
				}

				http.FileServer(http.FS(subFS)).ServeHTTP(w, r)
				return
			}

			http.Error(w, "UI not built. Run 'make build' first.", http.StatusInternalServerError)
			return
		}"""

new_routing = """		if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/admin" || r.URL.Path == "/portal") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			htmlContent := strings.ReplaceAll(dashboardHTML, "static/dashboard.js", "static/dashboard.js?v="+config.Version)
			htmlContent = strings.ReplaceAll(htmlContent, "/static/dashboard.css", "/static/dashboard.css?v="+config.Version)
			if _, err := w.Write([]byte(htmlContent)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
			return
		}

		// Catch-all SPA routing for Control Plane GET requests under /portalv2
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/portalv2") {
			subFS, err := fs.Sub(uiDistFS, "ui-dist")
			if err == nil {
				cleanPath := strings.TrimPrefix(r.URL.Path, "/portalv2")
				cleanPath = strings.TrimPrefix(cleanPath, "/")
				if cleanPath == "" {
					cleanPath = "index.html"
				}

				// Check if the file exists in the embedded FS
				f, err := subFS.Open(cleanPath)
				if err != nil {
					// SPA Fallback: Serve index.html for client-side routing
					r.URL.Path = "/"
				} else {
					_ = f.Close() //nolint:errcheck
				}

				if strings.HasSuffix(cleanPath, ".html") || cleanPath == "index.html" {
					w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
				}

				// Use StripPrefix to strip /portalv2 from the request path before serving from subFS
				http.StripPrefix("/portalv2", http.FileServer(http.FS(subFS))).ServeHTTP(w, r)
				return
			}

			http.Error(w, "UI not built. Run 'make build' first.", http.StatusInternalServerError)
			return
		}"""

content = content.replace(old_routing, new_routing)

with open('pkg/server/server.go', 'w') as f:
    f.write(content)
