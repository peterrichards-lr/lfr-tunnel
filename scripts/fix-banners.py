import re

# 1. Update dashboard.html
with open('pkg/server/dashboard.html', 'r') as f:
    html = f.read()

# Replace the two banners with a single banner that is absolutely positioned at the top of main-content
single_banner = '<div id="global-notification-banner" style="display:none; position: absolute; top: 0; left: 0; right: 0; z-index: 1000; padding: 12px 24px; text-align: center; font-weight: 500; box-shadow: 0 4px 12px rgba(0,0,0,0.2);"></div>'

html = re.sub(
    r'<div id="global-broadcast-banner".*?</div>\s*<div id="global-maintenance-banner".*?</div>',
    single_banner,
    html,
    flags=re.DOTALL
)

with open('pkg/server/dashboard.html', 'w') as f:
    f.write(html)

# 2. Update dashboard.js
with open('pkg/server/static/dashboard.js', 'r') as f:
    js = f.read()

banner_logic = """
            const notifyBanner = document.getElementById('global-notification-banner');
            let bannerActive = false;

            if (data.maintenance_mode === "pending" || data.maintenance_mode === "active") {
                bannerActive = true;
                if (data.maintenance_mode === "pending") {
                    window.maintenanceSecondsLeft = data.maintenance_seconds_left;
                    const updateCountdown = () => {
                        const secs = window.maintenanceSecondsLeft;
                        if (secs <= 0) {
                            notifyBanner.innerHTML = `🛠️ <strong>Gateway is currently undergoing Scheduled Maintenance!</strong> Tunnels are paused.`;
                            notifyBanner.style.backgroundColor = '#ef4444';
                            notifyBanner.style.color = 'white';
                            if (window.maintenanceTimerInterval) clearInterval(window.maintenanceTimerInterval);
                        } else {
                            notifyBanner.innerHTML = `⚠️ <strong>Gateway is restarting for updates in ${secs}s.</strong> Active sessions will be temporarily suspended.`;
                            notifyBanner.style.backgroundColor = '#f59e0b';
                            notifyBanner.style.color = 'white';
                        }
                    };
                    updateCountdown();
                    if (!window.maintenanceTimerInterval) {
                        window.maintenanceTimerInterval = setInterval(() => {
                            window.maintenanceSecondsLeft--;
                            updateCountdown();
                        }, 1000);
                    }
                } else if (data.maintenance_mode === "active") {
                    notifyBanner.innerHTML = `🛠️ <strong>Gateway is currently undergoing Scheduled Maintenance!</strong> Tunnels are paused.`;
                    notifyBanner.style.backgroundColor = '#ef4444';
                    notifyBanner.style.color = 'white';
                }
            } else {
                if (window.maintenanceTimerInterval) {
                    clearInterval(window.maintenanceTimerInterval);
                    window.maintenanceTimerInterval = null;
                }
                if (data.broadcast_message) {
                    bannerActive = true;
                    notifyBanner.innerText = data.broadcast_message;
                    notifyBanner.style.backgroundColor = 'var(--danger)';
                    notifyBanner.style.color = 'white';
                }
            }

            if (bannerActive) {
                notifyBanner.style.display = 'block';
            } else {
                notifyBanner.style.display = 'none';
            }
"""

js = re.sub(
    r'const banner = document\.getElementById\(\'global-broadcast-banner\'\);.*?maintBanner\.style\.display = \'none\';\n\s*\}\n\s*\}',
    banner_logic.strip(),
    js,
    flags=re.DOTALL
)

with open('pkg/server/static/dashboard.js', 'w') as f:
    f.write(js)

print("Banners fixed.")
