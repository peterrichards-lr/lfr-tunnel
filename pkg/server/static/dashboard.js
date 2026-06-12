// Setup initial theme based on local storage or system preference
        const savedTheme = localStorage.getItem('theme');
        if (savedTheme) {
            document.documentElement.setAttribute('data-theme', savedTheme);
        } else if (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) {
            document.documentElement.setAttribute('data-theme', 'light');
        } else {
            document.documentElement.setAttribute('data-theme', 'dark');
        }

        function toggleTheme() {
            let currentTheme = document.documentElement.getAttribute('data-theme');
            let newTheme = currentTheme === 'light' ? 'dark' : 'light';
            document.documentElement.setAttribute('data-theme', newTheme);
            localStorage.setItem('theme', newTheme);
        }

        let currentUser = null;
        let generatedRawToken = "";

        
        function showToast(message, type = 'info') {
            const container = document.getElementById('toast-container');
            const toast = document.createElement('div');
            toast.className = `toast toast-${type}`;
            toast.innerHTML = `<span>${message}</span>`;
            container.appendChild(toast);
            
            // Trigger animation
            setTimeout(() => toast.classList.add('show'), 10);
            
            // Remove after 4 seconds
            setTimeout(() => {
                toast.classList.remove('show');
                setTimeout(() => toast.remove(), 300);
            }, 4000);
        }

        async function init() {
            const urlParams = new URLSearchParams(window.location.search);
            const magicToken = urlParams.get('token');
            if (magicToken) {
                window.history.replaceState({}, document.title, window.location.pathname);
                const vRes = await fetch('/api/auth/verify', {
                    method: 'POST',
                    headers: {'Content-Type': 'application/json'},
                    body: JSON.stringify({ token: magicToken })
                });
                if (!vRes.ok) {
                    const err = await vRes.json();
                    showToast("Magic link error: " + (err.error || "Invalid or expired"));
                }
            }

            try {
                const res = await fetch('/api/me');
                if (res.ok) {
                    currentUser = await res.json();
                    showDashboard();
                } else {
                    showLogin();
                }
            } catch (e) {
                showLogin();
            }
        }

        async function showLogin() {
            document.getElementById('loader').style.display = 'none';
            document.getElementById('login-screen').style.display = 'flex';
            
            // Fetch SSO providers
            const res = await fetch('/api/auth/providers');
            if (res.ok) {
                const data = await res.json();
                const container = document.getElementById('sso-container');
                const divider = document.getElementById('sso-divider');
                container.innerHTML = '';
                if (data.providers && data.providers.length > 0) {
                    data.providers.forEach(p => {
                        const a = document.createElement('a');
                        a.href = `/api/auth/login?provider=${p.id}`;
                        a.className = 'btn btn-primary';
                        a.innerHTML = `<span class="btn-icon">⚡</span> Continue with ${p.name}`;
                        container.appendChild(a);
                    });
                    divider.style.display = 'flex';
                } else {
                    divider.style.display = 'none';
                }
            } else {
                document.getElementById('sso-divider').style.display = 'none';
            }
        }

        function showDashboard() {
            document.getElementById('loader').style.display = 'none';
            document.getElementById('login-screen').style.display = 'none';
            document.getElementById('dashboard-screen').style.display = 'flex';

            let greetingName = currentUser.preferred_name || currentUser.first_name;
            let welcomeGreeting = greetingName ? `Welcome Back, ${escapeHTML(greetingName)}!` : "Welcome Back!";
            let firstGreeting = greetingName ? `Welcome to Liferay Tunnel, ${escapeHTML(greetingName)}!` : "Welcome to Liferay Tunnel!";
            if (currentUser.last_login_at && !currentUser.last_login_at.startsWith('0001')) {
                document.getElementById('last-login-banner').style.display = 'flex';
                document.getElementById('last-login-text').innerHTML = `<strong>${welcomeGreeting}</strong> Your last login was ${formatLocalTime(currentUser.last_login_at)} from IP <code>${escapeHTML(currentUser.last_login_ip || 'Unknown')}</code>.`;
            } else {
                document.getElementById('last-login-banner').style.display = 'flex';
                document.getElementById('last-login-text').innerHTML = `<strong>${firstGreeting}</strong> We're glad you're here. This appears to be your first time logging in.`;
            }

            try {
                const vRes = await fetch('/api/version');
                if (vRes.ok) {
                    const vData = await vRes.json();
                    const latestVer = vData.latest_version;
                    const userVer = currentUser.last_client_version || '';
                    
                    if (!userVer || userVer !== latestVer) {
                        let os = 'Unknown OS';
                        let dlSuffix = '';
                        const ua = navigator.userAgent;
                        if (ua.includes('Mac OS X')) {
                            os = 'macOS';
                            dlSuffix = '-darwin-arm64'; // Default to ARM for modern Macs
                            if (ua.includes('Intel')) dlSuffix = '-darwin-amd64';
                        } else if (ua.includes('Windows')) {
                            os = 'Windows';
                            dlSuffix = '-windows-amd64.exe';
                        } else if (ua.includes('Linux')) {
                            os = 'Linux';
                            dlSuffix = '-linux-amd64';
                        }

                        const dlUrl = `https://github.com/peterrichards-lr/lfr-tunnel/releases/download/${latestVer}/lfr-tunnel${dlSuffix}`;
                        const otherUrl = `https://github.com/peterrichards-lr/lfr-tunnel/releases/tag/${latestVer}`;

                        const bannerDiv = document.createElement('div');
                        bannerDiv.className = 'alert alert-info';
                        bannerDiv.style.marginTop = '1rem';
                        bannerDiv.style.display = 'flex';
                        bannerDiv.style.alignItems = 'center';
                        bannerDiv.style.justifyContent = 'space-between';

                        const titleText = (!userVer) ? `Get started with the CLI` : `Update Available (v${latestVer})`;
                        const subText = (!userVer) ? `Download the tunnel client for ${os} to begin.` : `You are using an older client (${userVer}). Please update to the latest release for ${os}.`;

                        bannerDiv.innerHTML = `
                            <div><strong>${titleText}</strong> <br/> ${subText}</div>
                            <div style="display: flex; gap: 10px;">
                                ${os !== 'Unknown OS' ? `<a href="${dlUrl}" class="btn btn-primary" style="white-space: nowrap;">⬇️ Download for ${os}</a>` : ''}
                                <a href="${otherUrl}" target="_blank" class="btn btn-secondary" style="white-space: nowrap;">Other OSs</a>
                            </div>
                        `;
                        document.getElementById('last-login-banner').after(bannerDiv);
                    }
                }
            } catch (e) {
                console.error("Failed to check version", e);
            }


            if (currentUser.role === 'admin' || currentUser.role === 'owner') {
                document.getElementById('admin-sidebar-group').classList.remove('hidden');
                loadUsers(); // This updates the registration badge count
            }

            // Populate account fields
            document.getElementById('acc-first-name').value = currentUser.first_name || '';
            document.getElementById('acc-last-name').value = currentUser.last_name || '';
            document.getElementById('acc-preferred-name').value = currentUser.preferred_name || '';
            document.getElementById('acc-theme').value = currentUser.theme_preference || 'system';
            document.getElementById('acc-notifications').checked = (currentUser.notification_prefs === 'enabled' || !currentUser.notification_prefs);

            // Apply theme from preference if not system
            applyTheme(currentUser.theme_preference);

            loadTokens();
            loadTunnels();
        }

        function applyTheme(pref) {
            let themeToApply = pref;
            if (themeToApply === 'time') {
                const hour = new Date().getHours();
                themeToApply = (hour >= 6 && hour < 18) ? 'light' : 'dark';
            } else if (!themeToApply || themeToApply === 'system') {
                themeToApply = (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) ? 'light' : 'dark';
            }
            document.documentElement.setAttribute('data-theme', themeToApply);
            
            if (window.Chart && Object.keys(charts).length > 0) {
                const isLight = themeToApply === 'light';
                const textColor = isLight ? '#475569' : '#94a3b8';
                const gridColor = isLight ? '#e2e8f0' : '#334155';
                
                Chart.defaults.color = textColor;
                Object.values(charts).forEach(chart => {
                    if (chart.options.plugins && chart.options.plugins.legend) chart.options.plugins.legend.labels.color = textColor;
                    if (chart.options.scales && chart.options.scales.x) {
                        chart.options.scales.x.grid.color = gridColor;
                        chart.options.scales.x.ticks.color = textColor;
                    }
                    if (chart.options.scales && chart.options.scales.y) {
                        chart.options.scales.y.grid.color = gridColor;
                        chart.options.scales.y.ticks.color = textColor;
                    }
                    chart.update();
                });
            }
        }

        // Global debug function to test Time of Day locally in the browser console
        window.testTimeTheme = function(mockHour) {
            if (currentUser && currentUser.theme_preference === 'time') {
                const isLight = (mockHour >= 6 && mockHour < 18);
                document.documentElement.setAttribute('data-theme', isLight ? 'light' : 'dark');
                console.log(`Mocking time as ${mockHour}:00 - Theme is now ${isLight ? 'light' : 'dark'}`);
            } else {
                console.log("Please set your theme to 'Time of Day' in Account settings first.");
            }
        };

        // Listen for system theme changes in real-time
        if (window.matchMedia) {
            window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', e => {
                const pref = currentUser ? currentUser.theme_preference : localStorage.getItem('theme');
                if (!pref || pref === 'system') {
                    applyTheme('system');
                }
            });
        }

        // Periodically check the time of day theme if enabled
        setInterval(() => {
            if (currentUser && currentUser.theme_preference === 'time') {
                applyTheme('time');
            }
        }, 60000); // Check every minute

        document.getElementById('account-form').addEventListener('submit', async (e) => {
            e.preventDefault();
            const btn = document.getElementById('btn-save-account');
            btn.disabled = true;
            btn.innerText = "Saving...";

            const payload = {
                first_name: document.getElementById('acc-first-name').value,
                last_name: document.getElementById('acc-last-name').value,
                preferred_name: document.getElementById('acc-preferred-name').value,
                theme_preference: document.getElementById('acc-theme').value,
                notification_prefs: document.getElementById('acc-notifications').checked ? 'enabled' : 'disabled',
            };

            const res = await fetch('/api/me', {
                method: 'PUT',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify(payload)
            });

            if (res.ok) {
                applyTheme(payload.theme_preference);
                btn.innerText = "Saved!";
                setTimeout(() => {
                    btn.disabled = false;
                    btn.innerText = "Save Changes";
                }, 2000);
            } else {
                showToast("Failed to save account settings.");
                btn.disabled = false;
                btn.innerText = "Save Changes";
            }
        });

        function formatBytes(bytes, decimals = 2) {
            if (!+bytes) return '0 Bytes';
            const k = 1024;
            const dm = decimals < 0 ? 0 : decimals;
            const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
            const i = Math.floor(Math.log(bytes) / Math.log(k));
            return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`;
        }

        async function loadTunnels() {
            // Already fetched in /api/me
            const tunnels = currentUser.tunnels || [];
            const tbody = document.getElementById('tunnels-table-body');
            tbody.innerHTML = tunnels.map(t => `
                <tr>
                    <td style="font-weight: 500;">${escapeHTML(t.subdomain_prefix)}</td>
                    <td><a href="https://${escapeHTML(t.full_host)}" target="_blank" style="color: var(--primary); text-decoration: none;">${escapeHTML(t.full_host)}</a></td>
                    <td><span class="badge ${t.status === 'up' ? 'success' : ''}">${escapeHTML(t.status)}</span></td>
                    <td>${formatBytes(t.bytes_in)}</td>
                    <td>${formatBytes(t.bytes_out)}</td>
                    <td>${formatLocalTime(t.created_at)}</td>
                </tr>
            `).join('');
            if (tunnels.length === 0) {
                tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; color: var(--text-muted);">No active tunnels.</td></tr>`;
            }
        }

        function showTab(tabName) {
            document.querySelectorAll('.main-content > div').forEach(el => el.classList.add('hidden'));
            document.querySelectorAll('.nav-item').forEach(el => el.classList.remove('active'));
            
            document.getElementById(`tab-${tabName}`).classList.remove('hidden');
            document.getElementById(`nav-${tabName}`).classList.add('active');

            if (tabName === 'users') loadUsers();
            if (tabName === 'registrations') loadRegistrations();
            if (tabName === 'blacklist') loadBlacklist();
            if (tabName === 'audit') loadAudit();
            if (tabName === 'magic') loadAdminMagicLinks();
            if (tabName === 'tokens') loadTokens();
            if (tabName === 'tunnels') loadTunnels();
            if (tabName === 'analytics') loadAnalytics();
        }

        let charts = {};

        async function loadAnalytics() {
            const res = await fetch('/api/analytics');
            if (res.ok) {
                const data = await res.json();
                
                const isLight = document.documentElement.getAttribute('data-theme') === 'light';
                const textColor = isLight ? '#475569' : '#94a3b8';
                const gridColor = isLight ? '#e2e8f0' : '#334155';
                
                Chart.defaults.color = textColor;
                Chart.defaults.font.family = 'Inter, system-ui, sans-serif';

                const getOptions = (isBar) => ({
                    responsive: true,
                    maintainAspectRatio: false,
                    plugins: {
                        legend: { position: 'top', labels: { color: textColor } },
                        tooltip: { callbacks: { label: function(context) { return context.dataset.label + ': ' + formatBytes(context.raw); } } }
                    },
                    scales: {
                        x: { grid: { color: gridColor }, ticks: { color: textColor } },
                        y: { 
                            grid: { color: gridColor }, 
                            ticks: { color: textColor, callback: function(value) { return formatBytes(value); } } 
                        }
                    }
                });

                if (data.personal && data.personal.daily) {
                    const ctx = document.getElementById('myBandwidthChart').getContext('2d');
                    if (charts['myBandwidth']) charts['myBandwidth'].destroy();
                    charts['myBandwidth'] = new Chart(ctx, {
                        type: 'line',
                        data: {
                            labels: data.personal.daily.map(d => d.date),
                            datasets: [
                                { label: 'Data In', data: data.personal.daily.map(d => d.bytes_in), borderColor: '#3b82f6', backgroundColor: '#3b82f620', fill: true, tension: 0.4 },
                                { label: 'Data Out', data: data.personal.daily.map(d => d.bytes_out), borderColor: '#10b981', backgroundColor: '#10b98120', fill: true, tension: 0.4 }
                            ]
                        },
                        options: getOptions(false)
                    });
                }

                if (data.personal && data.personal.tunnels && data.personal.tunnels.length > 0) {
                    const ctx = document.getElementById('myTunnelsChart').getContext('2d');
                    if (charts['myTunnels']) charts['myTunnels'].destroy();
                    charts['myTunnels'] = new Chart(ctx, {
                        type: 'doughnut',
                        data: {
                            labels: data.personal.tunnels.map(t => t.full_host),
                            datasets: [{
                                label: 'Total Bandwidth',
                                data: data.personal.tunnels.map(t => t.bytes_in + t.bytes_out),
                                backgroundColor: ['#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#ec4899'],
                                borderWidth: 0
                            }]
                        },
                        options: {
                            responsive: true, maintainAspectRatio: false,
                            plugins: {
                                legend: { position: 'right', labels: { color: textColor } },
                                tooltip: { callbacks: { label: function(context) { return formatBytes(context.raw); } } }
                            }
                        }
                    });
                }

                if (data.global) {
                    document.getElementById('admin-analytics-section').style.display = 'block';

                    if (data.global.daily) {
                        const ctx = document.getElementById('globalBandwidthChart').getContext('2d');
                        if (charts['globalBandwidth']) charts['globalBandwidth'].destroy();
                        charts['globalBandwidth'] = new Chart(ctx, {
                            type: 'line',
                            data: {
                                labels: data.global.daily.map(d => d.date),
                                datasets: [
                                    { label: 'Total Data In', data: data.global.daily.map(d => d.bytes_in), borderColor: '#6366f1', backgroundColor: '#6366f120', fill: true, tension: 0.4 },
                                    { label: 'Total Data Out', data: data.global.daily.map(d => d.bytes_out), borderColor: '#f43f5e', backgroundColor: '#f43f5e20', fill: true, tension: 0.4 }
                                ]
                            },
                            options: getOptions(false)
                        });
                    }

                    if (data.global.top_users) {
                        const ctx = document.getElementById('topUsersChart').getContext('2d');
                        if (charts['topUsers']) charts['topUsers'].destroy();
                        charts['topUsers'] = new Chart(ctx, {
                            type: 'bar',
                            data: {
                                labels: data.global.top_users.map(u => u.email.split('@')[0]),
                                datasets: [{
                                    label: 'Total Bandwidth',
                                    data: data.global.top_users.map(u => u.bytes_in + u.bytes_out),
                                    backgroundColor: '#8b5cf6',
                                    borderRadius: 4
                                }]
                            },
                            options: getOptions(true)
                        });
                    }

                    // Load Client Stats
                    try {
                        const cRes = await fetch('/api/admin/analytics/clients');
                        if (cRes.ok) {
                            const cData = await cRes.json() || [];
                            const tbody = document.getElementById('client-stats-table-body');
                            tbody.innerHTML = cData.map(s => `
                                <tr>
                                    <td><span class="badge" style="background: var(--primary); color: white;">${escapeHTML(s.version)}</span></td>
                                    <td>${escapeHTML(s.os)}</td>
                                    <td style="font-weight: bold;">${s.count}</td>
                                </tr>
                            `).join('');
                            if (cData.length === 0) {
                                tbody.innerHTML = `<tr><td colspan="3" style="text-align: center; color: var(--text-muted);">No client telemetry data available yet.</td></tr>`;
                            }
                        }
                    } catch(e) {
                        console.error('Failed to load client stats', e);
                    }

                }
            }
        }

        async function loadTokens() {
            const res = await fetch('/api/tokens');
            if (res.ok) {
                const tokens = await res.json() || [];
                const tbody = document.getElementById('tokens-table-body');
                tbody.innerHTML = tokens.map(t => `
                    <tr>
                        <td style="font-weight: 500;">${escapeHTML(t.name)}</td>
                        <td style="font-family: monospace;">${t.token_prefix}...</td>
                        <td>${formatLocalTime(t.created_at)}</td>
                        <td>${t.expires_at ? formatLocalTime(t.expires_at) : 'Never'}</td>
                        <td>
                            <button class="btn" style="padding: 6px 12px; margin: 0; border-color: var(--danger); color: var(--danger);" onclick="revokeToken(${t.id})">Revoke</button>
                        </td>
                    </tr>
                `).join('');
                if (tokens.length === 0) {
                    tbody.innerHTML = `<tr><td colspan="5" style="text-align: center; color: var(--text-muted);">No tokens generated yet.</td></tr>`;
                }
            }
        }

        document.getElementById('btn-show-email').addEventListener('click', () => {
            document.getElementById('email-form').classList.remove('hidden');
            document.getElementById('register-form').classList.add('hidden');
        });

        document.getElementById('btn-show-register').addEventListener('click', () => {
            document.getElementById('register-form').classList.remove('hidden');
            document.getElementById('email-form').classList.add('hidden');
        });

        document.getElementById('register-form').addEventListener('submit', async (e) => {
            e.preventDefault();
            const btn = document.getElementById('btn-register');
            const msg = document.getElementById('reg-msg');
            btn.disabled = true;
            msg.textContent = "Processing...";
            msg.style.color = "var(--text)";
            
            const payload = {
                email: document.getElementById('reg-email').value
            };
            
            try {
                const res = await fetch('/api/register-request', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(payload)
                });
                
                const data = await res.json();
                
                if (res.ok) {
                    msg.textContent = data.message || "Registration request submitted. Please check your email.";
                    msg.style.color = "var(--success)";
                } else {
                    msg.textContent = data.error || "Registration failed.";
                    msg.style.color = "var(--danger)";
                    btn.disabled = false;
                }
            } catch (err) {
                msg.textContent = "A network error occurred.";
                msg.style.color = "var(--danger)";
                btn.disabled = false;
            }
        });

        document.getElementById('email-form').addEventListener('submit', async (e) => {
            e.preventDefault();
            const email = document.getElementById('email-input').value;
            const btn = document.getElementById('btn-magic-link');
            const msg = document.getElementById('email-msg');
            btn.disabled = true;
            btn.innerText = "Sending...";
            
            try {
                const res = await fetch('/api/auth/magic-link', {
                    method: 'POST',
                    headers: {'Content-Type': 'application/json'},
                    body: JSON.stringify({ email })
                });
                if (res.ok) {
                    msg.style.color = "var(--success)";
                    msg.innerText = "Magic link sent! Check your email.";
                    
                    let secondsLeft = 60;
                    btn.innerText = `Resend in ${secondsLeft}s`;
                    
                    const interval = setInterval(() => {
                        secondsLeft--;
                        if (secondsLeft <= 0) {
                            clearInterval(interval);
                            btn.disabled = false;
                            btn.innerText = "Send Magic Link";
                        } else {
                            btn.innerText = `Resend in ${secondsLeft}s`;
                        }
                    }, 1000);
                } else {
                    const err = await res.json();
                    msg.style.color = "var(--danger)";
                    msg.innerText = err.error || "Failed to send link.";
                    btn.disabled = false;
                    btn.innerText = "Send Magic Link";
                }
            } catch (e) {
                msg.innerText = "Network error";
                btn.disabled = false;
            }
        });

        async function logout() {
            await fetch('/api/auth/logout', { method: 'POST' });
            window.location.reload();
        }

        async function submitBlacklist() {
            const ip = document.getElementById('blacklist-ip').value;
            const reason = document.getElementById('blacklist-reason').value;
            const res = await fetch('/api/admin/blacklist', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({ ip_address: ip, reason: reason })
            });
            if (res.ok) {
                closeBlacklistModal();
                loadBlacklist();
            } else {
                showToast("Failed to ban IP");
            }
        }


        async function loadUsers() {
            const res = await fetch('/api/admin/users');
            if (res.ok) {
                const allUsers = await res.json() || [];
                const pendingUsers = allUsers.filter(u => (u.status === 'pending' || u.status === 'unverified'));
                const badge = document.getElementById('reg-badge');
                if (pendingUsers.length > 0) {
                    badge.style.display = 'inline-block';
                    badge.innerText = pendingUsers.length;
                } else {
                    badge.style.display = 'none';
                }

                const users = allUsers.filter(u => (u.status !== 'pending' && u.status !== 'unverified'));
                const tbody = document.getElementById('users-table-body');
                tbody.innerHTML = users.map(u => {

                    const isSelf = currentUser && u.email === currentUser.email;
                    const rowStyle = isSelf ? 'opacity: 0.6;' : '';
                    return `
                    <tr style="${rowStyle}">
                        <td style="font-weight: 500;">${escapeHTML(u.email)} ${isSelf ? '<span style="font-size: 12px; color: var(--text-muted);">(You)</span>' : ''}</td>
                        <td>${escapeHTML(u.first_name)} ${escapeHTML(u.last_name)}</td>
                        <td><span class="badge ${u.role === 'admin' ? 'success' : ''}">${escapeHTML(u.role)}</span></td>
                        <td><span class="badge ${u.status === 'approved' ? 'success' : (u.status === 'revoked' ? 'danger' : 'warning')}">${escapeHTML(u.status)}</span></td>
                        <td>${formatLocalTime(u.created_at)}</td>
                        <td>
                            ${(!isSelf && u.status !== 'approved') ? `<button class="btn" style="padding: 4px 8px; margin: 0 4px 0 0;" onclick="approveUser('${u.id}')">Approve</button>` : ''}
                            ${(!isSelf && u.status !== 'revoked') ? `<button class="btn" style="padding: 4px 8px; margin: 0 4px 0 0; color: var(--danger); border-color: var(--danger);" onclick="revokeUser('${u.id}')">Revoke</button>` : ''}
                            ${isSelf ? '<span style="font-size: 12px; color: var(--text-muted);">No actions</span>' : ''}
                        </td>
                    </tr>
                    `;
                }).join('');
                if (users.length === 0) tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; color: var(--text-muted);">No users found.</td></tr>`;
            }
        }

        
        async function loadRegistrations() {
            const res = await fetch('/api/admin/users');
            if (res.ok) {
                const allUsers = await res.json() || [];
                const pendingUsers = allUsers.filter(u => (u.status === 'pending' || u.status === 'unverified'));
                const badge = document.getElementById('reg-badge');
                if (pendingUsers.length > 0) {
                    badge.style.display = 'inline-block';
                    badge.innerText = pendingUsers.length;
                } else {
                    badge.style.display = 'none';
                }

                const tbody = document.getElementById('registrations-table-body');
                tbody.innerHTML = pendingUsers.map(u => {
                    return `
                    <tr>
                        <td style="font-weight: 500;">${escapeHTML(u.email)} <span class="badge ${u.status === 'pending' ? 'warning' : ''}">${escapeHTML(u.status)}</span></td>
                        <td>${escapeHTML(u.first_name)} ${escapeHTML(u.last_name)}</td>
                        <td>${formatLocalTime(u.created_at)}</td>
                        <td>
                            <button class="btn btn-primary" style="padding: 4px 8px; margin: 0 4px 0 0;" onclick="approveRegistration('${u.id}')">Approve</button>
                            <button class="btn" style="padding: 4px 8px; margin: 0; color: var(--danger); border-color: var(--danger);" onclick="denyRegistration('${u.id}')">Deny</button>
                        </td>
                    </tr>
                    `;
                }).join('');
                if (pendingUsers.length === 0) tbody.innerHTML = `<tr><td colspan="4" style="text-align: center; color: var(--text-muted);">No pending registrations.</td></tr>`;
            }
        }

        
        async function denyRegistration(id) {
            const res = await fetch(`/api/admin/users/${id}`, { method: 'PATCH', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({ status: 'revoked' }) });
            if (res.ok) {
                showToast('Registration denied', 'success');
                loadRegistrations();
                if (document.getElementById('nav-users').classList.contains('active')) loadUsers();
            } else {
                showToast('Failed to deny', 'error');
            }
        }

        async function approveRegistration(id) {
            const res = await fetch(`/api/admin/users/${id}`, { method: 'PATCH', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({ status: 'approved' }) });
            if (res.ok) {
                showToast('Registration approved', 'success');
                loadRegistrations();
                if (document.getElementById('nav-users').classList.contains('active')) loadUsers();
            } else {
                showToast('Failed to approve', 'error');
            }
        }

        async function approveUser(id) {
            await fetch(`/api/admin/users/${id}`, { method: 'PATCH', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({ status: 'approved' }) });
            loadUsers();
        }

        async function revokeUser(id) {
            await fetch(`/api/admin/users/${id}`, { method: 'PATCH', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({ status: 'revoked' }) });
            loadUsers();
        }

        async function loadBlacklist() {
            const res = await fetch('/api/admin/blacklist');
            if (res.ok) {
                const list = await res.json() || [];
                const tbody = document.getElementById('blacklist-table-body');
                tbody.innerHTML = list.map(b => `
                    <tr>
                        <td style="font-family: monospace;">${escapeHTML(b.ip_address)}</td>
                        <td>${escapeHTML(b.reason)}</td>
                        <td>${formatLocalTime(b.banned_at)}</td>
                        <td>
                            <button class="btn" style="padding: 4px 8px; margin: 0; color: var(--danger); border-color: var(--danger);" onclick="deleteBlacklist('${b.ip_address}')">Remove</button>
                        </td>
                    </tr>
                `).join('');
                if (list.length === 0) tbody.innerHTML = `<tr><td colspan="4" style="text-align: center; color: var(--text-muted);">No IPs on blacklist.</td></tr>`;
            }
        }

        async function deleteBlacklist(ip) {
            await fetch(`/api/admin/blacklist/${encodeURIComponent(ip)}`, { method: 'DELETE' });
            loadBlacklist();
        }

        async function loadAudit() {
            const res = await fetch('/api/admin/audit?limit=100');
            if (res.ok) {
                const logs = await res.json() || [];
                const tbody = document.getElementById('audit-table-body');
                tbody.innerHTML = logs.map(l => `
                    <tr>
                        <td>${formatLocalTime(l.timestamp)}</td>
                        <td><span style="font-family: monospace; font-size: 13px; background: rgba(0,0,0,0.1); padding: 2px 6px; border-radius: 4px;">${escapeHTML(l.event_type)}</span></td>
                        <td>${escapeHTML(l.actor_id)}</td>
                        <td>${escapeHTML(l.target_id)}</td>
                        <td>${escapeHTML(l.ip_address)}</td>
                        <td>${escapeHTML(l.details)}</td>
                    </tr>
                `).join('');
                if (logs.length === 0) tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; color: var(--text-muted);">No audit logs.</td></tr>`;
            }
        }

        async function loadAdminMagicLinks() {
            const res = await fetch('/api/admin/magic-links');
            if (res.ok) {
                const links = await res.json() || [];
                const tbody = document.getElementById('magic-table-body');
                tbody.innerHTML = links.map(l => `
                    <tr>
                        <td>${escapeHTML(l.email)}</td>
                        <td>${escapeHTML(l.client_ip)}</td>
                        <td>${formatLocalTime(l.expires_at)}</td>
                        <td>${l.used_at ? formatLocalTime(l.used_at) : 'Unused'}</td>
                    </tr>
                `).join('');
                if (links.length === 0) tbody.innerHTML = `<tr><td colspan="4" style="text-align: center; color: var(--text-muted);">No magic links found.</td></tr>`;
            }
        }

        function openBlacklistModal() {
            document.getElementById('blacklist-ip').value = '';
            document.getElementById('blacklist-reason').value = '';
            document.getElementById('blacklist-modal').style.display = 'flex';
        }

        function closeBlacklistModal() {
            document.getElementById('blacklist-modal').style.display = 'none';
        }

        function openInviteModal() {
            document.getElementById('invite-email').value = '';
            document.getElementById('invite-first-name').value = '';
            document.getElementById('invite-last-name').value = '';
            document.getElementById('invite-modal').style.display = 'flex';
        }

        function closeInviteModal() {
            document.getElementById('invite-modal').style.display = 'none';
        }

        async function submitInviteUser() {
            const payload = {
                email: document.getElementById('invite-email').value,
                first_name: document.getElementById('invite-first-name').value,
                last_name: document.getElementById('invite-last-name').value
            };
            const res = await fetch('/api/admin/invite', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify(payload)
            });
            if (res.ok) {
                closeInviteModal();
                loadUsers();
            } else if (res.status === 401) {
                showToast("Your session has expired. Please log in again.");
                window.location.reload();
            } else {
                const data = await res.json();
                showToast("Failed to invite user: " + (data.error || "Unknown error"));
            }
        }

        function openTokenModal() {
            document.getElementById('token-name').value = '';
            document.getElementById('token-form-step').classList.remove('hidden');
            document.getElementById('token-result-step').classList.add('hidden');
            document.getElementById('token-modal').style.display = 'flex';
        }

        function closeModal() {
            document.getElementById('token-modal').style.display = 'none';
        }

        async function generateToken() {
            const name = document.getElementById('token-name').value;
            const expiry = parseInt(document.getElementById('token-expiry').value);
            
            if (!name) return showToast("Please enter a name");

            const res = await fetch('/api/tokens', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({ name: name, expires_in_days: expiry })
            });

            if (res.ok) {
                const data = await res.json();
                generatedRawToken = data.raw_token;
                
                document.getElementById('token-form-step').classList.add('hidden');
                document.getElementById('token-result-step').classList.remove('hidden');
                document.getElementById('raw-token-display').innerText = generatedRawToken;
                
                // Attempt Magic Handoff
                const alertBox = document.getElementById('handoff-alert');
                alertBox.innerText = "Attempting to send token to CLI...";
                alertBox.className = "alert";
                
                try {
                    const handoff = await fetch('http://127.0.0.1:4444/handoff', {
                        method: 'POST',
                        mode: 'no-cors', // We use no-cors to blindly fire it
                        body: generatedRawToken
                    });
                    // Because of no-cors, we can't reliably read the response status locally, 
                    // but if it didn't throw a network error, the CLI likely received it.
                    alertBox.innerText = "Token successfully delivered to your CLI! You may close this window.";
                    alertBox.className = "alert alert-success";
                } catch (e) {
                    alertBox.innerText = "Heads up: If you started this from your terminal using 'lfr-tunnel login', the CLI would auto-configure. Since it isn't running, please manually copy your token below:";
                    alertBox.className = "alert alert-warning";
                }
            } else {
                showToast("Failed to create token.");
            }
        }

        function copyToken() {
            navigator.clipboard.writeText(generatedRawToken);
            showToast("Copied to clipboard!");
        }

        async function revokeToken(id) {
            if (confirm("Are you sure you want to revoke this token?")) {
                const res = await fetch(`/api/tokens/${id}`, { method: 'DELETE' });
                if (!res.ok) {
                    const errorBody = await res.text();
                    showToast("Failed to revoke token: " + errorBody);
                }
                loadTokens();
            }
        }

                function formatLocalTime(utcDateStr) {
            if (!utcDateStr) return 'Never';
            const date = new Date(utcDateStr);
            const tz = document.getElementById('acc-tz')?.value || 'UTC';
            return date.toLocaleString(undefined, { timeZone: tz });
        }

        function escapeHTML(str) {
            return str.replace(/[&<>'"]/g, tag => ({
                '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;'
            }[tag]));
        }

        init();