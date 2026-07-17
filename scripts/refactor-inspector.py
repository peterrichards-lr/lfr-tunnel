import re

with open('pkg/client/inspector.go', 'r') as f:
    go = f.read()

# Replace //go:embed inspector.html and logs.html
go = re.sub(r'//go:embed inspector\.html\nvar InspectorHTML \[\]byte\n\n//go:embed logs\.html\nvar LogsHTML \[\]byte', '//go:embed dashboard.html\nvar DashboardHTML []byte', go)

# Replace the mux handlers
go = re.sub(
    r'mux\.HandleFunc\("/", func\(w http\.ResponseWriter, r \*http\.Request\) \{.*?w\.Write\(InspectorHTML\)\n\t\}\)',
    '''mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/settings" && r.URL.Path != "/logs" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(DashboardHTML)
	})''',
    go,
    flags=re.DOTALL
)

# Remove the /logs handler
go = re.sub(
    r'\s*mux\.HandleFunc\("/logs", func\(w http\.ResponseWriter, r \*http\.Request\) \{.*?w\.Write\(LogsHTML\)\n\t\}\)',
    '',
    go,
    flags=re.DOTALL
)

with open('pkg/client/inspector.go', 'w') as f:
    f.write(go)
print("inspector.go updated.")
