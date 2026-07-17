import sys

with open('pkg/client/dashboard.html', 'r') as f:
    content = f.read()

# Fix matchMedia null issue
old_match = """        if (window.matchMedia) {
            window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', e => {
                const currentTheme = localStorage.getItem('inspector_theme') || serverThemePref || 'system';
                if (currentTheme === 'system') applyTheme('system');
            });
        }"""
new_match = """        if (window.matchMedia) {
            const mq = window.matchMedia('(prefers-color-scheme: dark)');
            if (mq) {
                const handler = e => {
                    const currentTheme = localStorage.getItem('inspector_theme') || serverThemePref || 'system';
                    if (currentTheme === 'system') applyTheme('system');
                };
                if (mq.addEventListener) mq.addEventListener('change', handler);
                else if (mq.addListener) mq.addListener(handler);
            }
        }"""
content = content.replace(old_match, new_match)

# Fix event listener null issues
content = content.replace("themeBtn.addEventListener('click', () => {", "if (themeBtn) themeBtn.addEventListener('click', () => {")
content = content.replace("autoScrollCheckbox.addEventListener('change', (e) => {", "if (autoScrollCheckbox) autoScrollCheckbox.addEventListener('change', (e) => {")
content = content.replace("logsContainer.addEventListener('scroll', () => {", "if (logsContainer) logsContainer.addEventListener('scroll', () => {")
content = content.replace("filterSearch.addEventListener('input', renderLogs);", "if (filterSearch) filterSearch.addEventListener('input', renderLogs);")
content = content.replace("filterLevel.addEventListener('change', renderLogs);", "if (filterLevel) filterLevel.addEventListener('change', renderLogs);")
content = content.replace("document.getElementById('btn-refresh').addEventListener('click', fetchLogs);", "const btnRefresh = document.getElementById('btn-refresh');\n        if (btnRefresh) btnRefresh.addEventListener('click', fetchLogs);")
content = content.replace("document.getElementById('btn-clear').addEventListener('click', () => {", "const btnClear = document.getElementById('btn-clear');\n        if (btnClear) btnClear.addEventListener('click', () => {")

with open('pkg/client/dashboard.html', 'w') as f:
    f.write(content)

print("Dashboard HTML fixed successfully")
