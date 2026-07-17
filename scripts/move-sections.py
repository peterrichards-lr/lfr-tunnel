import re

with open('pkg/server/dashboard.html', 'r') as f:
    html = f.read()

# Grab Test Integrations Card
test_int_match = re.search(r'<!-- Test Integrations Card -->.*?</div>\s*</div>', html, re.DOTALL)
test_int_card = test_int_match.group(0) if test_int_match else ''

# Grab Server Gateway Configuration Card
server_cfg_match = re.search(r'<!-- Server Gateway Configuration Card -->.*?</div>\s*</div>\s*</div>', html, re.DOTALL)
server_cfg_card = server_cfg_match.group(0) if server_cfg_match else ''

# Remove from Maintenance Tab
if test_int_card:
    html = html.replace(test_int_card, '')
if server_cfg_card:
    html = html.replace(server_cfg_card, '')

# Add to System Settings Tab (#tab-system) before the Save Settings button section
system_settings_insertion_point = r'<div style="border-top: 1px solid var\(--border-color\); padding-top: 24px; text-align: right;">\s*<button class="btn btn-primary" onclick="saveSystemSettings\(\)"'

if test_int_card or server_cfg_card:
    insertion = f"\n</div>\n{test_int_card}\n{server_cfg_card}\n<div>\n"
    # Wait, the structure is:
    # </div> <!-- end of settings grid -->
    # <div style="border-top:..."> <!-- save button -->
    # So we should insert it right before the save button div
    html = re.sub(
        r'(<div style="border-top: 1px solid var\(--border-color\); padding-top: 24px; text-align: right;">\s*<button class="btn btn-primary" onclick="saveSystemSettings\(\)")',
        f'{test_int_card}\n{server_cfg_card}\n\\1',
        html
    )

with open('pkg/server/dashboard.html', 'w') as f:
    f.write(html)
print("Sections moved to System Settings.")
