import re

with open('pkg/client/dashboard.html', 'r') as f:
    html = f.read()

# 1. Extract settings innerHTML
settings_match = re.search(r'main\.innerHTML\s*=\s*`(.*?)`;\n\s*fetchConfig\(\);', html, re.DOTALL)
settings_html = settings_match.group(1) if settings_match else ''

# Clean up renderSettingsView
html = re.sub(
    r'async function renderSettingsView\(\) \{.*?\};?\n\s*fetchConfig\(\);\n\s*\}',
    'async function renderSettingsView() { fetchConfig(); }',
    html,
    flags=re.DOTALL
)

# 2. Inject settings_html into view-settings
html = html.replace(
    '<div id="view-settings" class="view-section" style="display: none; padding: 20px; overflow-y: auto;"></div>',
    f'<div id="view-settings" class="view-section" style="display: none; padding: 20px; overflow-y: auto;">{settings_html}</div>'
)

# 3. Rewrite switchView
new_switch_view = """
        function switchView(view) {
            activeView = view;
            
            const trafficTab = document.getElementById('tab-traffic');
            const settingsTab = document.getElementById('tab-settings');
            const logsTab = document.getElementById('tab-logs');
            
            const viewInspector = document.getElementById('view-inspector');
            const viewSettings = document.getElementById('view-settings');
            const viewLogs = document.getElementById('view-logs');
            
            if (trafficTab) trafficTab.classList.remove('active');
            if (settingsTab) settingsTab.classList.remove('active');
            if (logsTab) logsTab.classList.remove('active');
            
            if (viewInspector) viewInspector.style.display = 'none';
            if (viewSettings) viewSettings.style.display = 'none';
            if (viewLogs) viewLogs.style.display = 'none';
            
            if (view === 'settings') {
                if (history.pushState) history.pushState(null, null, '/settings');
                if (settingsTab) settingsTab.classList.add('active');
                if (viewSettings) viewSettings.style.display = 'block';
                renderSettingsView();
            } else if (view === 'logs') {
                if (history.pushState) history.pushState(null, null, '/logs');
                if (logsTab) logsTab.classList.add('active');
                if (viewLogs) viewLogs.style.display = 'flex';
            } else {
                if (history.pushState) history.pushState(null, null, '/');
                if (trafficTab) trafficTab.classList.add('active');
                if (viewInspector) viewInspector.style.display = 'flex';
            }
        }
"""
html = re.sub(
    r'function switchView\(view\) \{.*?\}\n        \}\n',
    new_switch_view,
    html,
    flags=re.DOTALL
)

# 4. Update initRouter
new_init_router = """
        function initRouter() {
            const path = window.location.pathname;
            if (path === '/settings') {
                switchView('settings');
            } else if (path === '/logs') {
                switchView('logs');
            } else {
                switchView('traffic');
            }
        }
"""
html = re.sub(
    r'function initRouter\(\) \{.*?\n        \}\n',
    new_init_router,
    html,
    flags=re.DOTALL
)

# 5. Add Logs Tab to the sidebar navigation
logs_tab_html = """
                <li id="tab-logs" onclick="switchView('logs')">
                    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path><polyline points="14 2 14 8 20 8"></polyline><line x1="16" y1="13" x2="8" y2="13"></line><line x1="16" y1="17" x2="8" y2="17"></line><polyline points="10 9 9 9 8 9"></polyline></svg>
                    <span data-i18n="client_nav_logs">Logs</span>
                </li>
"""
html = html.replace(
    '<li id="tab-settings" onclick="switchView(\'settings\')">',
    logs_tab_html + '\n                <li id="tab-settings" onclick="switchView(\'settings\')">'
)

with open('pkg/client/dashboard.html', 'w') as f:
    f.write(html)

print("dashboard.html refactored successfully.")
