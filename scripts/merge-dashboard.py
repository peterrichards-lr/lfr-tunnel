import re
import sys

def read_file(path):
    with open(path, 'r') as f:
        return f.read()

insp = read_file('pkg/client/templates/inspector.html')
logs = read_file('pkg/client/templates/logs.html')

# Extract logs style
logs_style_match = re.search(r'<style>(.*?)</style>', logs, re.DOTALL)
logs_style = logs_style_match.group(1) if logs_style_match else ''

# Extract logs container
logs_container_match = re.search(r'(<div class="logs-container".*?</div>\s*</div>)', logs, re.DOTALL)
logs_container = logs_container_match.group(1) if logs_container_match else ''
if not logs_container:
    # fallback
    logs_container_match = re.search(r'(<div class="header-panel">.*?</button>\s*</div>\s*</div>\s*<div class="logs-container".*?</div>)', logs, re.DOTALL)
    logs_container = logs_container_match.group(1) if logs_container_match else ''

# Extract logs scripts
logs_scripts = re.findall(r'<script>(.*?)</script>', logs, re.DOTALL)

# Insert styles
insp = re.sub(r'(</style>)', r'\n/* LOGS STYLE */\n' + logs_style.replace('\\', '\\\\') + r'\n\1', insp, count=1)

# Modify the layout
# We need to wrap traffic-split in view-inspector
insp = insp.replace('<div class="traffic-split">', '<div id="view-inspector" class="view-section" style="display: flex; height: 100%;"><div class="traffic-split">')
insp = insp.replace('<!-- End Traffic Split -->', '</div><!-- End Traffic Split -->')

# Now add view-settings and view-logs right after view-inspector
append_html = f'''
        <div id="view-settings" class="view-section" style="display: none; padding: 20px; overflow-y: auto;"></div>
        <div id="view-logs" class="view-section" style="display: none; height: 100%; flex-direction: column;">
            {logs_container}
        </div>
'''
insp = insp.replace('</div><!-- End Traffic Split -->', '</div><!-- End Traffic Split -->\n' + append_html)

# Append logs scripts at the end before </body>
logs_js = "\n".join(logs_scripts)
insp = insp.replace('</body>', f'<script>\n/* LOGS SCRIPT */\n{logs_js}\n</script>\n</body>')

with open('pkg/client/dashboard.html', 'w') as f:
    f.write(insp)
print("dashboard.html generated successfully.")
