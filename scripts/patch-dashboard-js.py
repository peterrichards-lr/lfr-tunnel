import re
import os

js_path = 'pkg/server/static/dashboard.js'
with open(js_path, 'r', encoding='utf-8') as f:
    content = f.read()

def t(key, fallback):
    return f'(window.translations && window.translations["{key}"]) ? window.translations["{key}"] : `{fallback}`'

# Replace Soft active text
content = content.replace(
    '`Status: <span style="color: #ef4444; font-weight: 600;">ACTIVE (Bouncer checking IDs) 🔴</span>`',
    t("maint_status_active", "Status: <span style=\\\"color: #ef4444; font-weight: 600;\\\">ACTIVE 🔴</span>")
)
content = content.replace(
    'toggleBtn.innerText = "Disable Soft Maintenance";',
    f'toggleBtn.innerHTML = {t("maint_btn_disable_soft", "Disable Soft Maintenance")};'
)

# Replace Soft pending text
content = content.replace(
    '`Status: <span style="color: #f59e0b; font-weight: 600;">PENDING COUNTDOWN ⏳</span>`',
    t("maint_status_scheduled", "Status: <span style=\\\"color: #f59e0b; font-weight: 600;\\\">SCHEDULED 🟡</span>")
)
content = content.replace(
    'toggleBtn.innerText = "Cancel Soft Maintenance";',
    f'toggleBtn.innerHTML = {t("maint_btn_disable_soft", "Disable Soft Maintenance")};'
)

# Replace Soft inactive text
content = content.replace(
    '`Status: <span style="color: var(--text-muted);">INACTIVE (All welcome) 🟢</span>`',
    t("maint_status_inactive", "Status: <span style=\\\"color: var(--text-muted);\\\">INACTIVE (All welcome) 🟢</span>")
)
content = content.replace(
    'toggleBtn.innerText = "Enable Soft Maintenance";',
    f'toggleBtn.innerHTML = {t("maint_btn_enable_soft", "Enable Soft Maintenance")};'
)

# Replace Hard active text
content = content.replace(
    '`Status: <span style="color: #ef4444; font-weight: 600;">ACTIVE (Fire Curtain down) 🔴</span>`',
    t("maint_iron_active", "Status: <span style=\\\"color: #ef4444; font-weight: 600;\\\">ACTIVE 🔴</span>")
)
content = content.replace(
    'hardToggleBtn.innerText = "Disable Iron Curtain";',
    f'hardToggleBtn.innerHTML = {t("maint_btn_disable_hard", "Disable Iron Curtain")};'
)

# Replace Hard inactive text
content = content.replace(
    '`Status: <span style="color: var(--text-muted);">INACTIVE (Open gate) 🟢</span>`',
    t("maint_iron_inactive", "Status: <span style=\\\"color: var(--text-muted);\\\">INACTIVE 🟢</span>")
)
content = content.replace(
    'hardToggleBtn.innerText = "Enable Iron Curtain";',
    f'hardToggleBtn.innerHTML = {t("maint_btn_enable_hard", "Enable Iron Curtain")};'
)

with open(js_path, 'w', encoding='utf-8') as f:
    f.write(content)

print("dashboard.js updated successfully!")
