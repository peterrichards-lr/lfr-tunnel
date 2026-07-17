import re

with open('pkg/client/dashboard.html', 'r') as f:
    html = f.read()

# CSS injection
css_injection = """
        /* RESPONSIVE & LAYOUT STYLES */
        body { flex-direction: row; }
        .sidebar { width: 260px; height: 100vh; border-right: 1px solid var(--border); background: var(--bg-card); display: flex; flex-direction: column; flex-shrink: 0; z-index: 100; transition: transform 0.3s ease; }
        .main-view { flex-grow: 1; height: 100vh; overflow: hidden; position: relative; background: var(--bg-base); }
        
        /* Nav Placement: Top */
        body.nav-top { flex-direction: column; }
        body.nav-top .sidebar { width: 100%; height: auto; flex-direction: row; border-right: none; border-bottom: 1px solid var(--border); align-items: center; justify-content: space-between; padding: 0 20px; }
        body.nav-top .sidebar .header-panel { border-bottom: none; flex-direction: row; align-items: center; gap: 20px; padding: 15px 0; background: transparent; }
        body.nav-top .sidebar ul { padding: 0; display: flex; flex-direction: row; gap: 10px; margin: 0; }
        body.nav-top .sidebar ul li { margin-bottom: 0; padding: 8px 16px; }
        body.nav-top .sidebar-footer { padding: 15px 0; border-top: none; }
        
        /* Mobile Slide-Over for Inspector Details */
        @media (max-width: 768px) {
            body { flex-direction: column; }
            .sidebar { width: 100%; height: auto; flex-direction: column; border-right: none; border-bottom: 1px solid var(--border); padding: 10px; }
            body.nav-top .sidebar { flex-direction: column; align-items: stretch; padding: 10px; }
            body.nav-top .sidebar .header-panel { flex-direction: column; align-items: flex-start; gap: 10px; }
            body.nav-top .sidebar ul { flex-direction: column; }
            
            .traffic-split { flex-direction: column; }
            .traffic-list { width: 100% !important; border-right: none; }
            
            /* Slide over */
            .traffic-details {
                position: fixed;
                top: 0; left: 100%; width: 100%; height: 100vh;
                z-index: 200;
                background: var(--bg-base);
                transition: left 0.3s ease;
                border-left: none;
            }
            .traffic-details.slide-in { left: 0; }
            
            /* Back button for mobile details */
            .details-back-btn { display: block !important; margin-right: 10px; cursor: pointer; color: var(--primary); }
            
            /* Settings grid */
            #view-settings .panel-body > div > div { grid-template-columns: 1fr !important; }
        }
        .details-back-btn { display: none; }
"""

html = html.replace('</style>', css_injection + '\n    </style>')

# Ensure sidebar tag exists. Wait, inspector.html has `<sidebar>` tag. Let's rename it to `<div class="sidebar">` for standard HTML5 or just keep `<sidebar>` and apply class.
html = html.replace('<sidebar>', '<div class="sidebar" id="app-nav">')
html = html.replace('</sidebar>', '</div>')

# Update NavPlacement in init()
js_injection = """
        function applyNavPlacement() {
            fetch('/api/config').then(r => r.json()).then(data => {
                if (data.nav_placement === 'sidebar') {
                    document.body.classList.remove('nav-top');
                } else {
                    document.body.classList.add('nav-top');
                }
            }).catch(e => console.error(e));
        }
"""

html = html.replace('function switchView', js_injection + '\n        function switchView')

# Call applyNavPlacement in initRouter or body onload
html = html.replace('initRouter();', 'initRouter();\n        applyNavPlacement();')

# Inject Nav Placement into Settings HTML
nav_placement_html = """
                                <!-- Column: Layout -->
                                <div style="display: flex; flex-direction: column; gap: 15px;">
                                    <h3 style="margin-top: 0; margin-bottom: 5px; font-size: 14px; color: var(--text-color); border-bottom: 1px solid var(--border); padding-bottom: 8px;">Appearance</h3>
                                    
                                    <div style="display: flex; flex-direction: column; gap: 6px;">
                                        <label style="font-weight: 500; color: var(--text-muted); font-size: 11px;" data-i18n="client_nav_placement">Navigation Layout</label>
                                        <select id="config-nav-placement" class="form-control" style="width: 100%; padding: 10px; border-radius: 6px; border: 1px solid var(--border); background: var(--bg-card); color: var(--text-main);">
                                            <option value="top">Top Navigation (Default)</option>
                                            <option value="sidebar">Left Sidebar</option>
                                        </select>
                                    </div>
                                </div>
"""

# Insert into settings innerHTML string
html = html.replace('<!-- Column 1: Connection & Authentication -->', nav_placement_html + '\n                                <!-- Column 1: Connection & Authentication -->')

# Update saveConfig
html = html.replace("const rateLimit = document.getElementById('config-rate-limit').value;", "const rateLimit = document.getElementById('config-rate-limit').value;\n            const navPlacement = document.getElementById('config-nav-placement').value;")
html = html.replace("maintenance_path: maintPath", "maintenance_path: maintPath,\n                nav_placement: navPlacement")

# Update fetchConfig
html = html.replace("if (data.maintenance_path !== undefined) { document.getElementById('config-maintenance-path').value = data.maintenance_path; }", "if (data.maintenance_path !== undefined) { document.getElementById('config-maintenance-path').value = data.maintenance_path; }\n                if (data.nav_placement !== undefined) { document.getElementById('config-nav-placement').value = data.nav_placement || 'top'; }")

# Mobile slide-over close button injection
header_replace = """<h2 style="display: flex; align-items: center;"><span class="details-back-btn" onclick="closeDetailsMobile()" title="Back">←</span> Request Details</h2>"""
html = html.replace('<h2>Request Details</h2>', header_replace)
html = html.replace('<h2 data-i18n="client_req_details">Request Details</h2>', header_replace)

close_details_js = """
        function closeDetailsMobile() {
            document.querySelector('.traffic-details').classList.remove('slide-in');
        }
        
        // Intercept viewDetails to slide in
        const originalViewDetails = viewDetails;
        viewDetails = function(id) {
            originalViewDetails(id);
            document.querySelector('.traffic-details').classList.add('slide-in');
        };
"""
html = html.replace('function viewDetails(id) {', close_details_js + '\n        function viewDetails(id) {')


with open('pkg/client/dashboard.html', 'w') as f:
    f.write(html)
print("dashboard.html made responsive.")
