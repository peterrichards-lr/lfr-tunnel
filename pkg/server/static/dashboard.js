// Setup initial theme based on local storage or system preference
        const savedTheme = localStorage.getItem('theme');
        if (savedTheme) {
            document.documentElement.setAttribute('data-theme', savedTheme);
        } else if (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) {
            document.documentElement.setAttribute('data-theme', 'light');
        } else {
            document.documentElement.setAttribute('data-theme', 'dark');
        }

        const supportedLocales = [
            { code: 'ar', name: 'العربية' },
            { code: 'de', name: 'Deutsch' },
            { code: 'en', name: 'English' },
            { code: 'es', name: 'Español' },
            { code: 'fr', name: 'Français' },
            { code: 'ja', name: '日本語' },
            { code: 'ko', name: '한국어' },
            { code: 'pt', name: 'Português' },
            { code: 'ro', name: 'Română' },
            { code: 'zh', name: '简体中文' }
        ];

        function getFlagSVG(lang) {
            const flags = {
                ar: `<svg viewBox="0 0 640 480" width="16" height="12" style="border-radius: 2px;"><rect width="640" height="480" fill="#006C35"/><path d="M190 280h260M190 280l40-20M320 180c-20 0-30 20-30 30s10 30 30 30s30-20 30-30s-10-30-30-30" stroke="#fff" stroke-width="12" fill="none"/></svg>`, // Saudi Arabia flag (Standard DXP compliant!)
                en: `<svg viewBox="0 0 640 480" width="16" height="12" style="border-radius: 2px;"><rect width="640" height="480" fill="#012169"/><path d="M0 0l640 480M640 0L0 480" stroke="#fff" stroke-width="80"/><path d="M0 0l640 480M640 0L0 480" stroke="#C8102E" stroke-width="48"/><path d="M320 0v480M0 240h640" stroke="#fff" stroke-width="133.3"/><path d="M320 0v480M0 240h640" stroke="#C8102E" stroke-width="80"/></svg>`,
                fr: `<svg viewBox="0 0 640 480" width="16" height="12" style="border-radius: 2px;"><g fill-rule="evenodd" stroke-width="1pt"><rect width="213.3" height="480" fill="#00209F"/><rect width="213.3" height="480" x="213.3" fill="#FFF"/><rect width="213.3" height="480" x="426.7" fill="#F42A38"/></g></svg>`,
                es: `<svg viewBox="0 0 640 480" width="16" height="12" style="border-radius: 2px;"><rect width="640" height="480" fill="#c60b1e"/><rect width="640" height="240" y="120" fill="#ffc400"/><rect width="640" height="480" fill="none"/></svg>`,
                de: `<svg viewBox="0 0 640 480" width="16" height="12" style="border-radius: 2px;"><rect width="640" height="480" fill="#ffce00"/><rect width="640" height="320" fill="#dd0000"/><rect width="640" height="160" fill="#000000"/></svg>`,
                pt: `<svg viewBox="0 0 640 480" width="16" height="12" style="border-radius: 2px;"><rect width="640" height="480" fill="#ff0000"/><rect width="256" height="480" fill="#006600"/><circle cx="256" cy="240" r="80" fill="#ffcc00"/></svg>`,
                ro: `<svg viewBox="0 0 640 480" width="16" height="12" style="border-radius: 2px;"><g fill-rule="evenodd" stroke-width="1pt"><rect width="213.3" height="480" fill="#002B7F"/><rect width="213.3" height="480" x="213.3" fill="#FCD116"/><rect width="213.3" height="480" x="426.7" fill="#CE1126"/></g></svg>`,
                ko: `<svg viewBox="0 0 640 480" width="16" height="12" style="border-radius: 2px;"><rect width="640" height="480" fill="#FFF"/><circle cx="320" cy="240" r="120" fill="#CD2E3A"/><path d="M320 240a120 120 0 010-240 120 120 0 010 240z" fill="#0047A0"/><path d="M120 90l36 48M484 90l36 48M120 342l36 48M484 342l36 48" stroke="#000" stroke-width="24"/></svg>`,
                ja: `<svg viewBox="0 0 640 480" width="16" height="12" style="border-radius: 2px;"><rect width="640" height="480" fill="#FFF"/><circle cx="320" cy="240" r="144" fill="#BC002D"/></svg>`,
                zh: `<svg viewBox="0 0 640 480" width="16" height="12" style="border-radius: 2px;"><rect width="640" height="480" fill="#EE1C25"/><path d="M115 120L78 214l97-58H78l97 58z" fill="#FFFF00"/></svg>`
            };
            return flags[lang] || flags['en'];
        }

        function renderCustomDropdown() {
            const menu = document.getElementById('custom-dropdown-menu');
            if (!menu) return;
            
            menu.innerHTML = '';
            supportedLocales.forEach(loc => {
                const item = document.createElement('div');
                item.style.display = 'flex';
                item.style.alignItems = 'center';
                item.style.gap = '8px';
                item.style.padding = '8px 12px';
                item.style.cursor = 'pointer';
                item.style.fontSize = '13px';
                item.style.color = 'var(--text-muted)';
                item.style.transition = '0.2s';
                
                item.onmouseover = () => {
                    item.style.background = 'rgba(255,255,255,0.05)';
                    item.style.color = 'var(--text-color)';
                };
                item.onmouseout = () => {
                    item.style.background = 'transparent';
                    item.style.color = 'var(--text-muted)';
                };
                
                item.onclick = () => {
                    selectCustomLanguage(loc.code);
                };
                
                item.innerHTML = `
                    ${getFlagSVG(loc.code)}
                    <span>${loc.name}</span>
                `;
                menu.appendChild(item);
            });
        }

        function selectCustomLanguage(lang, skipSync = false) {
            const loc = supportedLocales.find(l => l.code === lang) || supportedLocales[1];
            const flagSpan = document.getElementById('custom-dropdown-flag');
            const labelSpan = document.getElementById('custom-dropdown-label');
            if (flagSpan) flagSpan.innerHTML = getFlagSVG(lang);
            if (labelSpan) labelSpan.innerText = loc.name;
            
            const menu = document.getElementById('custom-dropdown-menu');
            if (menu) menu.style.display = 'none';
            
            if (!skipSync) {
                selectAccLanguage(lang, true);
            }
            
            changePortalLanguage(lang);
        }

        function toggleCustomDropdown() {
            const menu = document.getElementById('custom-dropdown-menu');
            if (!menu) return;
            menu.style.display = menu.style.display === 'none' ? 'block' : 'none';
        }

        function renderAccCustomDropdown() {
            const menu = document.getElementById('acc-custom-menu');
            if (!menu) return;
            
            menu.innerHTML = '';
            supportedLocales.forEach(loc => {
                const item = document.createElement('div');
                item.style.display = 'flex';
                item.style.alignItems = 'center';
                item.style.gap = '8px';
                item.style.padding = '8px 12px';
                item.style.cursor = 'pointer';
                item.style.fontSize = '14px';
                item.style.color = 'var(--text-muted)';
                item.style.transition = '0.2s';
                
                item.onmouseover = () => {
                    item.style.background = 'rgba(255,255,255,0.05)';
                    item.style.color = 'var(--text-color)';
                };
                item.onmouseout = () => {
                    item.style.background = 'transparent';
                    item.style.color = 'var(--text-muted)';
                };
                
                item.onclick = () => {
                    selectAccLanguage(loc.code);
                };
                
                item.innerHTML = `
                    ${getFlagSVG(loc.code)}
                    <span>${loc.name}</span>
                `;
                menu.appendChild(item);
            });
        }

        function selectAccLanguage(lang, skipSync = false) {
            const loc = supportedLocales.find(l => l.code === lang) || supportedLocales[1];
            const flagSpan = document.getElementById('acc-custom-flag');
            const labelSpan = document.getElementById('acc-custom-label');
            const hiddenInput = document.getElementById('acc-language');
            if (flagSpan) flagSpan.innerHTML = getFlagSVG(lang);
            if (labelSpan) labelSpan.innerText = loc.name;
            if (hiddenInput) hiddenInput.value = lang;
            
            const menu = document.getElementById('acc-custom-menu');
            if (menu) menu.style.display = 'none';
            
            if (!skipSync) {
                selectCustomLanguage(lang, true);
            }
        }

        function toggleAccDropdown() {
            const menu = document.getElementById('acc-custom-menu');
            if (!menu) return;
            menu.style.display = menu.style.display === 'none' ? 'block' : 'none';
        }

        document.addEventListener('click', (e) => {
            const customTrigger = document.getElementById('portal-custom-dropdown');
            if (customTrigger && !customTrigger.contains(e.target)) {
                const menu = document.getElementById('custom-dropdown-menu');
                if (menu) menu.style.display = 'none';
            }
            const accTrigger = document.getElementById('acc-language-custom-dropdown');
            if (accTrigger && !accTrigger.contains(e.target)) {
                const menu = document.getElementById('acc-custom-menu');
                if (menu) menu.style.display = 'none';
            }
        });

        let tableInstances = {};

function renderTable(tbodyId, data, renderRowFn) {
    if (!tableInstances[tbodyId]) {
        const tbody = document.getElementById(tbodyId);
        const container = tbody.closest('.table-container');
        
        // Filter input
        const filterInput = document.createElement('input');
        filterInput.id = tbodyId + '-search';
        filterInput.type = 'text';
        filterInput.className = 'input-field';
        filterInput.placeholder = 'Search...';
        filterInput.style.maxWidth = '250px';
        filterInput.style.marginBottom = '12px';
        
        const controlsDiv = document.createElement('div');
        controlsDiv.style.display = 'flex';
        controlsDiv.style.justifyContent = 'space-between';
        controlsDiv.appendChild(filterInput);
        container.parentNode.insertBefore(controlsDiv, container);
        
        // Pagination controls
        const paginationDiv = document.createElement('div');
        paginationDiv.id = tbodyId + '-pagination';
        paginationDiv.style.display = 'flex';
        paginationDiv.style.justifyContent = 'flex-end';
        paginationDiv.style.alignItems = 'center';
        paginationDiv.style.marginTop = '12px';
        paginationDiv.style.gap = '8px';
        container.parentNode.insertBefore(paginationDiv, container.nextSibling);

        tableInstances[tbodyId] = {
            data: [],
            filteredData: [],
            currentPage: 1,
            pageSize: 10,
            tbody: tbody,
            filterInput: filterInput,
            paginationDiv: paginationDiv,
            renderRowFn: renderRowFn,
            sortCol: null,
            sortAsc: true
        };
        
        // Sorting Headers
        const headers = container.querySelectorAll('th');
        headers.forEach((th, index) => {
            const sortKey = th.getAttribute('data-sort');
            if (sortKey) {
                th.style.cursor = 'pointer';
                th.title = 'Click to sort';
                th.addEventListener('click', () => {
                    const inst = tableInstances[tbodyId];
                    if (inst.sortCol === sortKey) {
                        inst.sortAsc = !inst.sortAsc;
                    } else {
                        inst.sortCol = sortKey;
                        inst.sortAsc = true;
                    }
                    // Update header styling
                    headers.forEach(h => h.innerText = h.innerText.replace(/ [↑↓]$/, ''));
                    th.innerText += inst.sortAsc ? ' ↑' : ' ↓';
                    
                    updateTableView(tbodyId);
                });
            }
        });

        filterInput.addEventListener('input', (e) => {
            tableInstances[tbodyId].currentPage = 1;
            updateTableView(tbodyId);
        });
    }

    const inst = tableInstances[tbodyId];
    inst.data = data;
    inst.renderRowFn = renderRowFn;
    updateTableView(tbodyId);
}

function updateTableView(tbodyId) {
    if (window.closeAllActionMenus) {
        window.closeAllActionMenus();
    }
    const inst = tableInstances[tbodyId];
    const term = inst.filterInput.value.toLowerCase();
    
    // Filter
    inst.filteredData = inst.data.filter(item => {
        if (!term) return true;
        return Object.values(item).some(val => String(val).toLowerCase().includes(term));
    });

    // Sort
    if (inst.sortCol) {
        inst.filteredData.sort((a, b) => {
            const valA = String(a[inst.sortCol] || '').toLowerCase();
            const valB = String(b[inst.sortCol] || '').toLowerCase();
            if (valA < valB) return inst.sortAsc ? -1 : 1;
            if (valA > valB) return inst.sortAsc ? 1 : -1;
            return 0;
        });
    }
    
    // Paginate
    const totalPages = Math.ceil(inst.filteredData.length / inst.pageSize) || 1;
    if (inst.currentPage > totalPages) inst.currentPage = totalPages;
    
    const start = (inst.currentPage - 1) * inst.pageSize;
    const pageData = inst.filteredData.slice(start, start + inst.pageSize);
    
    // Render
    inst.tbody.innerHTML = pageData.map(inst.renderRowFn).join('');
    if (pageData.length === 0) {
        inst.tbody.innerHTML = `<tr><td colspan="10" style="text-align: center; color: var(--text-muted);">No results found.</td></tr>`;
    }
    
    // Render Pagination
    inst.paginationDiv.innerHTML = '';
    if (totalPages > 1) {
        const firstBtn = document.createElement('button');
        firstBtn.className = 'btn btn-secondary';
        firstBtn.style.padding = '4px 8px';
        firstBtn.style.margin = '0';
        firstBtn.style.width = 'auto';
        firstBtn.innerHTML = '&laquo; First';
        firstBtn.disabled = inst.currentPage === 1;
        firstBtn.onclick = () => { inst.currentPage = 1; updateTableView(tbodyId); };
        
        const prevBtn = document.createElement('button');
        prevBtn.className = 'btn btn-secondary';
        prevBtn.style.padding = '4px 8px';
        prevBtn.style.margin = '0';
        prevBtn.style.width = 'auto';
        prevBtn.innerText = 'Prev';
        prevBtn.disabled = inst.currentPage === 1;
        prevBtn.onclick = () => { inst.currentPage--; updateTableView(tbodyId); };
        
        const pageSelect = document.createElement('select');
        pageSelect.className = 'form-control';
        pageSelect.style.width = 'auto';
        pageSelect.style.padding = '2px 8px';
        pageSelect.style.margin = '0';
        pageSelect.style.display = 'inline-block';
        pageSelect.style.fontSize = '14px';
        for (let i = 1; i <= totalPages; i++) {
            const opt = document.createElement('option');
            opt.value = i;
            opt.innerText = `Page ${i} of ${totalPages}`;
            if (i === inst.currentPage) opt.selected = true;
            pageSelect.appendChild(opt);
        }
        pageSelect.onchange = (e) => { inst.currentPage = parseInt(e.target.value); updateTableView(tbodyId); };
        
        const nextBtn = document.createElement('button');
        nextBtn.className = 'btn btn-secondary';
        nextBtn.style.padding = '4px 8px';
        nextBtn.style.margin = '0';
        nextBtn.style.width = 'auto';
        nextBtn.innerText = 'Next';
        nextBtn.disabled = inst.currentPage === totalPages;
        nextBtn.onclick = () => { inst.currentPage++; updateTableView(tbodyId); };

        const lastBtn = document.createElement('button');
        lastBtn.className = 'btn btn-secondary';
        lastBtn.style.padding = '4px 8px';
        lastBtn.style.margin = '0';
        lastBtn.style.width = 'auto';
        lastBtn.innerHTML = 'Last &raquo;';
        lastBtn.disabled = inst.currentPage === totalPages;
        lastBtn.onclick = () => { inst.currentPage = totalPages; updateTableView(tbodyId); };
        
        inst.paginationDiv.appendChild(firstBtn);
        inst.paginationDiv.appendChild(prevBtn);
        inst.paginationDiv.appendChild(pageSelect);
        inst.paginationDiv.appendChild(nextBtn);
        inst.paginationDiv.appendChild(lastBtn);
    }
}


function toggleTheme() {
            let currentTheme = document.documentElement.getAttribute('data-theme');
            let newTheme = currentTheme === 'light' ? 'dark' : 'light';
            document.documentElement.setAttribute('data-theme', newTheme);
            localStorage.setItem('theme', newTheme);
        }

        let currentUser = null;
        let currentLanguage = "en";
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

        async function loadVersionDetails() {
            try {
                const vRes = await fetch('/api/version');
                if (vRes.ok) {
                    const vData = await vRes.json();
                    if (vData.privacy_policy_url) {
                        const pl = document.getElementById('footer-privacy-link');
                        if (pl) pl.href = vData.privacy_policy_url;
                    }
                    if (vData.cookie_policy_url) {
                        const cl = document.getElementById('footer-cookie-link');
                        if (cl) cl.href = vData.cookie_policy_url;
                    }
                    const displayVer = vData.server_version || vData.latest_version;
                    if (displayVer) {
                        const versionDisplays = document.querySelectorAll('.server-version-display');
                        versionDisplays.forEach(el => {
                            el.textContent = displayVer;
                        });
                    }
                    if (vData.latest_version) {
                        const clientDisplays = document.querySelectorAll('.client-version-display');
                        clientDisplays.forEach(el => {
                            el.textContent = vData.latest_version;
                        });
                    }
                    const box = document.getElementById('docker-container-box');
                    if (vData.docker_image) {
                        if (box) box.style.display = 'block';
                        const text = document.getElementById('docker-pull-text');
                        if (text) text.textContent = `docker pull ${vData.docker_image}`;
                        const link = document.getElementById('docker-hub-link');
                        if (link) {
                            const repoOnly = vData.docker_image.split(':')[0];
                            link.href = `https://hub.docker.com/r/${repoOnly}`;
                        }
                        const bypassLink = document.getElementById('docker-bypass-link');
                        if (bypassLink && vData.docker_bypass_url) {
                            bypassLink.href = vData.docker_bypass_url;
                            bypassLink.style.display = 'inline-block';
                        }
                    } else {
                        if (box) box.style.display = 'none';
                    }
                }
            } catch (e) {
                console.error("Failed to load policy links / version", e);
            }
        }

        async function init() {
            const urlParams = new URLSearchParams(window.location.search);
            const magicToken = urlParams.get('token');
            const langParam = urlParams.get('lang') || '';
            if (magicToken) {
                window.history.replaceState({}, document.title, window.location.pathname);
                const vRes = await fetch('/api/auth/verify', {
                    method: 'POST',
                    headers: {'Content-Type': 'application/json'},
                    body: JSON.stringify({ token: magicToken, lang: langParam })
                });
                if (vRes.ok) {
                    const data = await vRes.json();
                    if (data.status === 'mfa_required') {
                        showLogin();
                        document.getElementById('email-form').classList.add('hidden');
                        document.getElementById('register-form').classList.add('hidden');
                        document.getElementById('btn-show-email').classList.add('hidden');
                        document.getElementById('btn-show-register').classList.add('hidden');
                        if (document.getElementById('sso-container')) document.getElementById('sso-container').style.display = 'none';
                        if (document.getElementById('sso-divider')) document.getElementById('sso-divider').style.display = 'none';

                        document.getElementById('mfa-temp-token').value = data.temp_token;
                        document.getElementById('mfa-form').classList.remove('hidden');
                        document.getElementById('mfa-code-input').focus();
                        return;
                    }
                } else {
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

            await loadVersionDetails();

            // Auto-detect browser language on first load and translate unauthenticated portal UI
            try {
                const res = await fetch('/api/i18n');
                if (res.ok) {
                    const bundle = await res.json();
                    
                    // Deduce resolved language by scanning typical strings
                    let resolvedLang = "en";
                    if (bundle.portal_welcome === "Bienvenido") resolvedLang = "es";
                    else if (bundle.portal_welcome === "Bienvenue") resolvedLang = "fr";
                    else if (bundle.portal_welcome === "Willkommen") resolvedLang = "de";
                    else if (bundle.portal_welcome === "Bem-vindo") resolvedLang = "pt";
                    else if (bundle.portal_welcome === "환영합니다") resolvedLang = "ko";
                    else if (bundle.portal_welcome === "ようこそ") resolvedLang = "ja";
                    else if (bundle.portal_welcome === "欢迎") resolvedLang = "zh";
                    else if (bundle.portal_welcome === "Bine ai venit") resolvedLang = "ro";

                    currentLanguage = resolvedLang;
                    const selector = document.getElementById('portal-language-selector');
                    if (selector) selector.value = resolvedLang;

                    // Set HTML direction (RTL support for Arabic/Hebrew)
                    const dir = (resolvedLang === 'ar' || resolvedLang === 'he') ? 'rtl' : 'ltr';
                    document.documentElement.dir = dir;

                    // Apply translations to data-i18n tagged elements
                    document.querySelectorAll('[data-i18n]').forEach(el => {
                        const key = el.getAttribute('data-i18n');
                        if (bundle[key]) el.innerText = bundle[key];
                    });

                    // Dynamically update the footer privacy/cookie links with ?lang=...
                    const pl = document.getElementById('footer-privacy-link');
                    if (pl && pl.getAttribute('href').startsWith('/privacy')) {
                        pl.href = '/privacy?lang=' + encodeURIComponent(resolvedLang);
                    }
                    const cl = document.getElementById('footer-cookie-link');
                    if (cl && cl.getAttribute('href').startsWith('/cookies')) {
                        cl.href = '/cookies?lang=' + encodeURIComponent(resolvedLang);
                    }
                }
            } catch (e) {
                console.error("Failed to auto-detect and translate portal language", e);
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

        async function showDashboard() {
            document.getElementById('loader').style.display = 'none';
            document.getElementById('login-screen').style.display = 'none';
            document.getElementById('dashboard-screen').style.display = 'flex';

            if (currentUser.killed_previous_session) {
                setTimeout(() => showToast("Warning: You were previously logged in elsewhere. That session has been invalidated."), 1000);
            }

            let greetingName = currentUser.preferred_name;
            let welcomeGreeting = greetingName ? `Welcome Back, ${escapeHTML(greetingName)}!` : "Welcome Back!";
            let firstGreeting = greetingName ? `Welcome to Liferay Tunnel, ${escapeHTML(greetingName)}!` : "Welcome to Liferay Tunnel!";
            if (currentUser.last_login_at && !currentUser.last_login_at.startsWith('0001')) {
                document.getElementById('last-login-banner').style.display = 'flex';
                document.getElementById('last-login-text').innerHTML = `<strong>${welcomeGreeting}</strong> Your last login was ${renderTimestamp(currentUser.last_login_at)} from IP <code>${escapeHTML(currentUser.last_login_ip || 'Unknown')}</code>.`;
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
                    
                    if (vData.documentation_url) {
                        const dl = document.getElementById('docs-link');
                        if (dl) dl.href = vData.documentation_url;
                    }
                    
                    let os = 'Unknown OS';
                    let platformKey = '';
                    const ua = navigator.userAgent;
                    if (ua.includes('Mac OS X')) {
                        os = 'macOS';
                        platformKey = ua.includes('Intel') ? 'macos_amd64' : 'macos_arm64';
                    } else if (ua.includes('Windows')) {
                        os = 'Windows';
                        platformKey = 'windows_amd64';
                    } else if (ua.includes('Linux')) {
                        os = 'Linux';
                        platformKey = 'linux_amd64';
                    }

                    const repoUrl = vData.repository_url || 'https://github.com/peterrichards-lr/lfr-tunnel';
                    const otherUrl = `${repoUrl}/releases/latest`;
                    const rawUrl = repoUrl.replace('github.com', 'raw.githubusercontent.com') + '/master';
                    const checksumsUrl = repoUrl.replace('github.com', 'raw.githubusercontent.com') + '/checksums/checksums.txt';

                    const defaults = {
                        "macos_arm64": {
                            "url": `${repoUrl}/releases/latest/download/lfr-tunnel-darwin-arm64`,
                            "cmd": "brew tap peterrichards-lr/tap && brew trust peterrichards-lr/tap && brew install lfr-tunnel",
                            "cmd_fallback": `curl -sSfL ${rawUrl}/scripts/install.sh | sh`
                        },
                        "macos_amd64": {
                            "url": `${repoUrl}/releases/latest/download/lfr-tunnel-darwin-amd64`,
                            "cmd": "brew tap peterrichards-lr/tap && brew trust peterrichards-lr/tap && brew install lfr-tunnel",
                            "cmd_fallback": `curl -sSfL ${rawUrl}/scripts/install.sh | sh`
                        },
                        "windows_amd64": {
                            "url": `${repoUrl}/releases/latest/download/lfr-tunnel-windows-amd64.exe`,
                            "cmd": "scoop bucket add peterrichards-lr https://github.com/peterrichards-lr/scoop-bucket && scoop install lfr-tunnel",
                            "cmd_fallback": `iwr ${rawUrl}/scripts/install.ps1 | iex`
                        },
                        "linux_amd64": {
                            "url": `${repoUrl}/releases/latest/download/lfr-tunnel-linux-amd64`,
                            "cmd": `curl -sSfL ${rawUrl}/scripts/install.sh | sh`,
                            "cmd_fallback": ""
                        }
                    };

                    let config = (vData.client_platforms && vData.client_platforms[platformKey]) || defaults[platformKey];
                    let dlUrl = config ? config.url : `${repoUrl}/releases/latest`;
                    let recommendedCmd = config ? config.cmd : '';
                    let fallbackCmd = config ? config.cmd_fallback : '';

                    let binaryName = config && config.binary_name ? config.binary_name : '';
                    if (!binaryName) {
                        if (platformKey === 'macos_arm64') binaryName = 'lfr-tunnel-darwin-arm64';
                        else if (platformKey === 'macos_amd64') binaryName = 'lfr-tunnel-darwin-amd64';
                        else if (platformKey === 'windows_amd64') binaryName = 'lfr-tunnel-windows-amd64.exe';
                        else if (platformKey === 'linux_amd64') binaryName = 'lfr-tunnel-linux-amd64';
                    }

                    let staticSHA = config && config.sha256 ? config.sha256 : '';

                    // Determine visibility of download button
                    let showDownload = true;
                    if (config && config.show_download !== undefined && config.show_download !== null) {
                        showDownload = config.show_download;
                    }
                    let downloadLabel = config && config.download_label ? config.download_label : '⬇️ Download Binary';

                    // Custom labels
                    let cmdLabel = config && config.cmd_label ? config.cmd_label : '🚀 Recommended (Package Manager):';
                    let cmdFallbackLabel = config && config.cmd_fallback_label ? config.cmd_fallback_label : '🛠️ Direct Script Fallback:';

                    // Recommended flag positioning
                    if (config && config.recommended) {
                        // If fallback is recommended, adjust default labels if not overridden
                        if (config.recommended === 'cmd_fallback') {
                            if (!config.cmd_fallback_label) cmdFallbackLabel = '🚀 Recommended (Direct Script):';
                            if (!config.cmd_label) cmdLabel = '🛠️ Package Manager Option:';
                        }
                    }

                    const bannerDiv = document.createElement('div');
                    bannerDiv.className = 'alert alert-info';
                    bannerDiv.style.marginTop = '1rem';
                    bannerDiv.style.display = 'flex';
                    bannerDiv.style.alignItems = 'center';
                    bannerDiv.style.justifyContent = 'space-between';

                    let titleText = 'CLI Client Installation';
                    let subText = `Run this command in your terminal to install the client for ${os}.`;
                    if (!userVer) {
                        titleText = 'Get started with the CLI';
                        subText = `Run this command in your terminal to install the client for ${os}.`;
                    } else if (userVer !== latestVer) {
                        titleText = `Update Available (v${latestVer})`;
                        subText = `You are using an older client (${userVer}). Please update to the latest release for ${os}.`;
                    } else {
                        subText = `Your client is up to date (v${userVer}) for ${os}.`;
                    }

                    if (os === 'Unknown OS') {
                        bannerDiv.innerHTML = `
                            <div>
                                <strong>${titleText}</strong> <br/>
                                <span style="font-size: 0.9rem; color: var(--text-muted);">${subText}</span>
                                <div style="font-size: 0.8rem; margin-top: 8px; display: flex; flex-wrap: wrap; gap: 16px; color: var(--text-muted); opacity: 0.85;">
                                    <span>🖥️ <strong>Server Gateway:</strong> ${vData.server_version || latestVer}</span>
                                    <span>🔌 <strong>Your Client:</strong> ${userVer || 'Never Connected'}</span>
                                    <span>🏷️ <strong>Latest Client Target:</strong> ${latestVer}</span>
                                </div>
                            </div>
                            <div style="display: flex; gap: 10px;">
                                <a href="${otherUrl}" target="_blank" class="btn btn-secondary" style="white-space: nowrap;">Releases / Other OSs</a>
                            </div>
                        `;
                    } else {
                        const hashSpanId = 'hash-' + Math.random().toString(36).substr(2, 9);
                        bannerDiv.innerHTML = `
                            <div style="flex-grow: 1; overflow: hidden; padding-right: 20px;">
                                <strong>${titleText}</strong> <br/>
                                <span style="font-size: 0.9rem; color: var(--text-muted);">${subText}</span>
                                <div style="font-size: 0.8rem; margin-top: 8px; margin-bottom: 8px; display: flex; flex-wrap: wrap; gap: 16px; color: var(--text-muted); opacity: 0.85;">
                                    <span>🖥️ <strong>Server Gateway:</strong> ${vData.server_version || latestVer}</span>
                                    <span>🔌 <strong>Your Client:</strong> ${userVer || 'Never Connected'}</span>
                                    <span>🏷️ <strong>Latest Client Target:</strong> ${latestVer}</span>
                                </div>
                                
                                ${recommendedCmd ? `
                                <div style="margin-top: 10px; font-size: 0.8rem; font-weight: bold; color: var(--text);">${cmdLabel}</div>
                                <div style="margin-top: 4px; margin-bottom: 8px; position: relative; background: #0d1117; color: #e6edf3; border-radius: 6px; border: 1px solid rgba(255,255,255,0.1); padding: 10px 40px 10px 12px; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 0.8rem; overflow-x: auto;">
                                    <span style="user-select: all;">${recommendedCmd}</span>
                                    <button onclick="navigator.clipboard.writeText('${recommendedCmd}'); this.innerHTML='<span style=\\'font-size:12px;\\'>✓</span>'; setTimeout(() => this.innerHTML='📋', 2000);" style="position: absolute; top: 6px; right: 6px; background: transparent; border: 1px solid rgba(255,255,255,0.2); color: #8b949e; border-radius: 4px; width: 22px; height: 22px; display: flex; align-items: center; justify-content: center; cursor: pointer; transition: 0.2s;" onmouseover="this.style.color='#c9d1d9'; this.style.borderColor='rgba(255,255,255,0.4)';" onmouseout="this.style.color='#8b949e'; this.style.borderColor='rgba(255,255,255,0.2)';">📋</button>
                                </div>
                                ` : ''}
 
                                ${fallbackCmd ? `
                                <div style="margin-top: 10px; font-size: 0.8rem; font-weight: bold; color: var(--text-muted);">${cmdFallbackLabel}</div>
                                <div style="margin-top: 4px; margin-bottom: 8px; position: relative; background: #0d1117; color: #e6edf3; border-radius: 6px; border: 1px solid rgba(255,255,255,0.1); padding: 10px 40px 10px 12px; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 0.8rem; overflow-x: auto;">
                                    <span style="user-select: all;">${fallbackCmd}</span>
                                    <button onclick="navigator.clipboard.writeText('${fallbackCmd}'); this.innerHTML='<span style=\\'font-size:12px;\\'>✓</span>'; setTimeout(() => this.innerHTML='📋', 2000);" style="position: absolute; top: 6px; right: 6px; background: transparent; border: 1px solid rgba(255,255,255,0.2); color: #8b949e; border-radius: 4px; width: 22px; height: 22px; display: flex; align-items: center; justify-content: center; cursor: pointer; transition: 0.2s;" onmouseover="this.style.color='#c9d1d9'; this.style.borderColor='rgba(255,255,255,0.4)';" onmouseout="this.style.color='#8b949e'; this.style.borderColor='rgba(255,255,255,0.2)';">📋</button>
                                </div>
                                ` : ''}
 
                                ${(staticSHA || binaryName) ? `<div style="font-size: 0.75rem; color: var(--text-muted); font-family: monospace;">SHA256: <span id="${hashSpanId}">loading...</span></div>` : ''}
                            </div>
                            <div style="display: flex; flex-direction: column; gap: 10px; align-items: stretch; min-width: 140px;">
                                ${showDownload ? `<a href="${dlUrl}" class="btn btn-primary" style="white-space: nowrap; text-align: center;">${downloadLabel}</a>` : ''}
                                <a href="${otherUrl}" target="_blank" class="btn btn-secondary" style="white-space: nowrap; text-align: center;">Other OSs</a>
                            </div>
                        `;

                        if (staticSHA) {
                            setTimeout(() => {
                                const span = document.getElementById(hashSpanId);
                                if (span) span.innerText = staticSHA;
                            }, 0);
                        } else if (binaryName) {
                            fetch(checksumsUrl)
                                .then(res => res.text())
                                .then(text => {
                                    const lines = text.split('\n');
                                    let foundHash = 'not found';
                                    for (let line of lines) {
                                        if (line.includes(binaryName)) {
                                            foundHash = line.split(' ')[0];
                                            break;
                                        }
                                    }
                                    const span = document.getElementById(hashSpanId);
                                    if (span) span.innerText = foundHash;
                                })
                                .catch(err => {
                                    const span = document.getElementById(hashSpanId);
                                    if (span) span.innerText = 'error fetching hash';
                                    console.error("Failed to fetch checksums", err);
                                });
                        }
                    }
                    document.getElementById('last-login-banner').after(bannerDiv);
                }
            } catch (e) {
                console.error("Failed to check version", e);
            }


            if (currentUser.role === 'admin' || currentUser.role === 'owner') {
                document.getElementById('admin-sidebar-group').classList.remove('hidden');
                loadUsers(); // This updates the registration badge count
                updateMaintenanceModeUI(currentUser.maintenance_mode || 'false', currentUser.iron_curtain || false);
            }

            // Populate account fields
            document.getElementById('acc-first-name').value = currentUser.first_name || '';
            document.getElementById('acc-last-name').value = currentUser.last_name || '';
            document.getElementById('acc-preferred-name').value = currentUser.preferred_name || '';
            document.getElementById('acc-theme').value = currentUser.theme_preference || 'system';

            // Hide Danger Zone GDPR Self-Deletion for the Platform Owner
            const dz = document.getElementById('danger-zone-container');
            if (dz) {
                if (currentUser.role === 'owner') {
                    dz.style.display = 'none';
                } else {
                    dz.style.display = 'block';
                }
            }

            // Show Iron Curtain Maintenance Card only for Owner
            const hardCard = document.getElementById('maint-iron-curtain-card');
            if (hardCard) {
                if (currentUser.role === 'owner') {
                    hardCard.style.display = 'block';
                } else {
                    hardCard.style.display = 'none';
                }
            }
            
            // Render and initialize our high-fidelity custom SVG flag selectors
            renderCustomDropdown();
            renderAccCustomDropdown();
            selectCustomLanguage(currentUser.language_preference || 'en');
            selectAccLanguage(currentUser.language_preference || 'en');
            document.getElementById('acc-notifications').checked = (currentUser.notification_prefs === 'enabled' || !currentUser.notification_prefs);

            // Apply theme from preference if not system
            applyTheme(currentUser.theme_preference);

            loadTokens();
            loadTunnels();
            renderMFAPanel();
            
            // Route to initial tab based on URL hash
            const initialTab = window.location.hash ? window.location.hash.slice(1) : 'overview';
            showTab(initialTab, true);
            
            startPolling();
        }

        let pollingInterval = null;
        function startPolling() {
            if (pollingInterval) clearInterval(pollingInterval);
            pollingInterval = setInterval(async () => {
                try {
                    const res = await fetch('/api/me');
                    if (res.status === 401) {
                        clearInterval(pollingInterval);
                        showToast("Session expired or logged in from another device.");
                        showLogin();
                        return;
                    }
                    if (res.ok) {
                        const data = await res.json();
                        const banner = document.getElementById('global-broadcast-banner');
                        if (data.broadcast_message) {
                            banner.innerText = data.broadcast_message;
                            banner.style.display = 'block';
                        } else {
                            banner.style.display = 'none';
                        }

                        const maintBanner = document.getElementById('global-maintenance-banner');
                        if (data.maintenance_mode === "pending") {
                            const secs = data.maintenance_seconds_left;
                            const mins = Math.floor(secs / 60);
                            const remSecs = secs % 60;
                            const timeStr = `${mins}:${remSecs < 10 ? '0' : ''}${remSecs}`;
                            maintBanner.innerHTML = `⚠️ <strong>Scheduled Maintenance starting in ${timeStr} minutes!</strong> All standard tunnels will be paused.`;
                            maintBanner.style.backgroundColor = '#f59e0b';
                            maintBanner.style.display = 'block';
                        } else if (data.maintenance_mode === "true") {
                            maintBanner.innerHTML = `🛠️ <strong>Gateway is currently undergoing Scheduled Maintenance!</strong> Tunnels are paused.`;
                            maintBanner.style.backgroundColor = '#ef4444';
                            maintBanner.style.display = 'block';
                            
                            // If the current user is not admin/owner, force close/logout!
                            if (currentUser && currentUser.role !== 'admin' && currentUser.role !== 'owner') {
                                clearInterval(pollingInterval);
                                showToast("The portal has entered scheduled maintenance. Standard sessions are suspended.", "danger");
                                logout();
                            }
                        } else {
                            maintBanner.style.display = 'none';
                        }

                        if (currentUser && (currentUser.role === 'admin' || currentUser.role === 'owner')) {
                            updateMaintenanceModeUI(data.maintenance_mode, data.iron_curtain);
                        }
                        
                        if (data.targeted_message && window.lastTargetedMessage !== data.targeted_message) {
                            window.lastTargetedMessage = data.targeted_message;
                            const tDiv = document.createElement('div');
                            tDiv.className = 'toast show';
                            tDiv.style.backgroundColor = 'var(--accent)';
                            tDiv.style.borderColor = 'var(--accent)';
                            tDiv.style.zIndex = '999999';
                            tDiv.innerHTML = `
                                <div style="display: flex; flex-direction: column;">
                                    <div style="margin-bottom: 8px;"><strong>Admin Message:</strong> ${escapeHTML(data.targeted_message)}</div>
                                    <div style="text-align: right;">
                                        <button onclick="this.parentElement.parentElement.parentElement.remove(); acknowledgeTargetedMessage(); window.lastTargetedMessage = null;" class="btn" style="background: rgba(0,0,0,0.2); color: white; border: none; padding: 4px 8px; margin: 0; min-width: 0; width: auto; font-size: 12px;">Dismiss</button>
                                    </div>
                                </div>
                            `;
                            document.getElementById('toast-container').appendChild(tDiv);
                        }
                        
                        if (data.killed_previous_session) {
                            showToast("Warning: You were previously logged in elsewhere. That session has been invalidated.");
                        }
                    }
                } catch (e) {
                    console.error("Polling error", e);
                }
            }, 10000); // 10 seconds for rapid testing, normally 30s
        }

        async function setBroadcastMessage() {
            const msg = document.getElementById('admin-broadcast-input').value.trim();
            const res = await fetch('/api/admin/broadcast', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({ message: msg })
            });
            if (res.ok) showToast("Broadcast message sent!");
            else showToast("Failed to send broadcast");
        }

        async function clearBroadcastMessage() {
            document.getElementById('admin-broadcast-input').value = "";
            const res = await fetch('/api/admin/broadcast', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({ message: "" })
            });
            if (res.ok) showToast("Broadcast message cleared!");
            else showToast("Failed to clear broadcast");
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
                language_preference: document.getElementById('acc-language').value,
                notification_prefs: document.getElementById('acc-notifications').checked ? 'enabled' : 'disabled',
            };

            const res = await fetch('/api/me', {
                method: 'PUT',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify(payload)
            });

            if (res.ok) {
                applyTheme(payload.theme_preference);
                currentUser.first_name = payload.first_name;
                currentUser.last_name = payload.last_name;
                currentUser.preferred_name = payload.preferred_name;
                currentUser.theme_preference = payload.theme_preference;
                currentUser.language_preference = payload.language_preference;
                currentUser.notification_prefs = payload.notification_prefs;

                // Update the greeting text immediately
                let greetingName = currentUser.preferred_name;
                let welcomeGreeting = greetingName ? `Welcome Back, ${escapeHTML(greetingName)}!` : "Welcome Back!";
                let firstGreeting = greetingName ? `Welcome to Liferay Tunnel, ${escapeHTML(greetingName)}!` : "Welcome to Liferay Tunnel!";
                if (currentUser.last_login_at && !currentUser.last_login_at.startsWith('0001')) {
                    document.getElementById('last-login-text').innerHTML = `<strong>${welcomeGreeting}</strong> Your last login was ${renderTimestamp(currentUser.last_login_at)} from IP <code>${escapeHTML(currentUser.last_login_ip || 'Unknown')}</code>.`;
                } else {
                    document.getElementById('last-login-text').innerHTML = `<strong>${firstGreeting}</strong> We're glad you're here. This appears to be your first time logging in.`;
                }

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
            if (!+bytes || bytes < 0) return '0 Bytes';
            const k = 1024;
            const dm = decimals < 0 ? 0 : decimals;
            const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
            let i = Math.floor(Math.log(bytes) / Math.log(k));
            if (i < 0) i = 0;
            if (i >= sizes.length) i = sizes.length - 1;
            return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`;
        }

        async function loadTunnels() {
            const isAdmin = currentUser && (currentUser.role === 'admin' || currentUser.role === 'owner');
            const headersEl = document.getElementById('tunnels-table-headers');
            if (headersEl) {
                headersEl.innerHTML = `
                    <th data-sort="subdomain">Subdomain</th>
                    <th data-sort="target">Full Host</th>
                    <th data-sort="status">Status</th>
                    <th>Actions</th>
                `;
            }

            // Already fetched in /api/me
            const tunnels = currentUser.tunnels || [];
            renderTable('tunnels-table-body', tunnels, t => {
                const tunnelJsonEncoded = encodeURIComponent(JSON.stringify(t));
                return `
                    <tr>
                        <td style="font-weight: 500;">${escapeHTML(t.subdomain_prefix)}</td>
                        <td><a href="https://${escapeHTML(t.full_host)}" target="_blank" style="color: var(--primary); text-decoration: none;">${escapeHTML(t.full_host)}</a></td>
                        <td><span class="badge ${t.status === 'up' ? 'success' : ''}">${escapeHTML(t.status)}</span></td>
                        <td>
                            <div class="action-menu">
                                <button class="action-menu-btn" onclick="toggleActionMenu('menu-tunnel-${escapeHTML(t.subdomain_prefix)}', event)">⋮</button>
                                <div id="menu-tunnel-${escapeHTML(t.subdomain_prefix)}" class="action-menu-dropdown">
                                    <button class="action-menu-item" onclick="openTunnelDetailsModal('${tunnelJsonEncoded}')">Details</button>
                                    ${isAdmin ? `<button class="action-menu-item danger" onclick="kickActiveTunnel('${escapeHTML(t.subdomain_prefix)}')">Kick</button>` : ''}
                                </div>
                            </div>
                        </td>
                    </tr>
                `;
            });
        }

        function showTab(tabName, skipHistory = false) {
            document.querySelectorAll('.main-content > div').forEach(el => el.classList.add('hidden'));
            document.querySelectorAll('.nav-item').forEach(el => el.classList.remove('active'));
            
            const tabEl = document.getElementById(`tab-${tabName}`);
            const navEl = document.getElementById(`nav-${tabName}`);
            if (tabEl) tabEl.classList.remove('hidden');
            if (navEl) navEl.classList.add('active');

            if (!skipHistory) {
                history.pushState({ tab: tabName }, '', '#' + tabName);
            }

            if (tabName === 'users') loadUsers();
            if (tabName === 'registrations') loadRegistrations();
            if (tabName === 'blacklist') loadBlacklist();
            if (tabName === 'audit') loadAudit();
            if (tabName === 'magic') loadAdminMagicLinks();
            if (tabName === 'backups') loadBackups();
            if (tabName === 'maintenance') loadMaintenanceStatus();
            if (tabName === 'tokens') loadTokens();
            if (tabName === 'tunnels') loadTunnels();
            if (tabName === 'reservations') loadReservations();
            if (tabName === 'analytics') loadAnalytics();
            if (tabName === 'overview') {
                loadWhatsNew();
                loadVersionDetails();
            }
        }

        window.addEventListener('popstate', (e) => {
            const tabName = (e.state && e.state.tab) ? e.state.tab : (window.location.hash ? window.location.hash.slice(1) : 'overview');
            showTab(tabName, true);
        });


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
                                labels: data.global.top_users.map(u => (u.email || "Anonymous").split('@')[0]),
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
                            renderTable('client-stats-table-body', cData, s => `
                                <tr>
                                    <td><span class="badge" style="background: var(--primary); color: white;">${escapeHTML(s.version || "Unknown")}</span></td>
                                    <td>${escapeHTML(s.os || "Unknown")}</td>
                                    <td style="font-weight: bold;">${s.count || 0}</td>
                                </tr>
                            `);
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
                const tokensHeaders = document.getElementById('tokens-table-headers');
                const isAdminOrOwner = currentUser && (currentUser.role === 'admin' || currentUser.role === 'owner');
                
                if (tokensHeaders) {
                    tokensHeaders.innerHTML = `
                        <tr>
                            <th data-sort="name">Name</th>
                            <th data-sort="token_prefix">Prefix</th>
                            ${isAdminOrOwner ? '<th data-sort="user_id">Owner</th>' : ''}
                            <th data-sort="created_at">Created</th>
                            <th data-sort="expires_at">Expires</th>
                            <th data-sort="expires_at">Expires In</th>
                            <th>Status</th>
                            <th style="text-align: right;">Action</th>
                        </tr>
                    `;
                }

                renderTable('tokens-table-body', tokens, t => {
                    const isRevoked = t.revoked_at != null && !t.revoked_at.startsWith('0001-01-01');
                    const isExpired = t.expires_at && !t.expires_at.startsWith('0001-01-01') && new Date(t.expires_at) < new Date();

                    let statusBadge = '';
                    if (isRevoked) {
                        statusBadge = `<span class="badge danger">Revoked</span>`;
                    } else if (isExpired) {
                        statusBadge = `<span class="badge danger">Expired</span>`;
                    } else {
                        statusBadge = `<span class="badge success">Active</span>`;
                    }

                    const actionMenuHtml = !isRevoked ? `
                        <div class="action-menu">
                            <button class="action-menu-btn" onclick="toggleActionMenu('menu-token-${t.id}', event)">⋮</button>
                            <div id="menu-token-${t.id}" class="action-menu-dropdown">
                                ${isAdminOrOwner ? `
                                    <button class="action-menu-item" onclick="extendToken(${t.id}, 30)">Extend +30d</button>
                                    <button class="action-menu-item" onclick="extendToken(${t.id}, 90)">Extend +90d</button>
                                    <button class="action-menu-item" onclick="extendToken(${t.id}, 0)">Extend Permanent</button>
                                ` : ''}
                                <button class="action-menu-item danger" onclick="revokeToken(${t.id})">Revoke</button>
                            </div>
                        </div>
                    ` : '';

                    return `
                        <tr>
                            <td style="font-weight: 500;">${escapeHTML(t.name)}</td>
                            <td style="font-family: monospace;">${t.token_prefix}...</td>
                            ${isAdminOrOwner ? `<td style="font-family: monospace; font-size: 13px;">${escapeHTML(t.user_id || 'N/A')}</td>` : ''}
                            <td>${renderTimestamp(t.created_at)}</td>
                            <td>${t.expires_at ? renderTimestamp(t.expires_at) : 'Never'}</td>
                            <td>${formatTimeRemaining(t.expires_at, isRevoked)}</td>
                            <td>${statusBadge}</td>
                            <td style="text-align: right;">
                                ${actionMenuHtml}
                            </td>
                        </tr>
                    `;
                });
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
                const res = await fetch('/api/auth/magic-link?lang=' + encodeURIComponent(currentLanguage), {
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
            if (pollingInterval) clearInterval(pollingInterval);
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
                renderTable('users-table-body', users, u => {
                    const isSelf = currentUser && u.email === currentUser.email;
                    const rowStyle = isSelf ? 'opacity: 0.6;' : '';
                    
                    const userJsonStr = encodeURIComponent(JSON.stringify(u));
                    const dotColor = u.portal_active ? '#10b981' : 'rgba(255,255,255,0.2)';
                    const dotShadow = u.portal_active ? 'box-shadow: 0 0 8px #10b981; text-shadow: 0 0 4px #10b981;' : '';
                    const dotTitle = u.portal_active ? 'Active on Portal' : 'Offline';
                    const statusDot = `<span style="color: ${dotColor}; font-size: 14px; font-weight: bold; cursor: default; ${dotShadow}" title="${dotTitle}">●</span>`;

                    const tunnelsCount = u.active_tunnels ? u.active_tunnels.length : 0;
                    const tunnelsBadge = tunnelsCount > 0 ? `<span class="badge" onclick="openUserDetailsModal('${userJsonStr}')" style="cursor: pointer; background: rgba(99,102,241,0.15); color: #818cf8; border: 1px solid rgba(99,102,241,0.3); padding: 2px 6px; font-size: 11px; margin-left: 8px;" title="Click to view tunnels">🔌 ${tunnelsCount} Tunnel${tunnelsCount > 1 ? 's' : ''}</span>` : '';

                    const emailLink = `<a href="#" onclick="openUserDetailsModal('${userJsonStr}'); return false;" style="font-weight: 500; text-decoration: none; color: inherit; cursor: pointer; transition: opacity 0.2s;" onmouseover="this.style.opacity='0.8'" onmouseout="this.style.opacity='1'">${escapeHTML(u.email)}</a>`;

                    const lastSeenText = u.portal_active ? '<span style="color: #10b981; font-weight: 500;">Active Now</span>' : renderTimestamp(u.last_login_at);

                    const quotaCell = `
                        <div style="font-size: 12px; display: flex; flex-direction: column; gap: 2px;">
                            <div><span style="color: var(--text-muted); font-size: 11px;">RPS:</span> <strong>${u.rate_limit ? u.rate_limit : '∞'}</strong></div>
                            <div><span style="color: var(--text-muted); font-size: 11px;">Subdomains:</span> <strong>${u.max_reservations !== undefined && u.max_reservations !== null ? (u.max_reservations < 0 ? '∞' : u.max_reservations) : '3'}</strong></div>
                            <div><span style="color: var(--text-muted); font-size: 11px;">Tunnels:</span> <strong>${u.max_tunnels !== undefined && u.max_tunnels !== null ? (u.max_tunnels < 0 ? '∞' : u.max_tunnels) : '3'}</strong></div>
                        </div>
                    `;

                    return `
                    <tr style="${rowStyle}">
                        <td>
                            <div style="display: flex; align-items: center; gap: 6px;">
                                ${statusDot}
                                ${emailLink}
                                ${isSelf ? '<span style="font-size: 12px; color: var(--text-muted);">(You)</span>' : ''}
                                ${tunnelsBadge}
                            </div>
                        </td>
                        <td>${escapeHTML(u.first_name)} ${escapeHTML(u.last_name)}</td>
                        <td><span class="badge ${u.role === 'admin' ? 'success' : ''}">${escapeHTML(u.role)}</span></td>
                        <td><span class="badge ${u.status === 'approved' ? 'success' : (u.status === 'revoked' ? 'danger' : 'warning')}">${escapeHTML(u.status)}</span></td>
                        <td>${quotaCell}</td>
                        <td>${lastSeenText}</td>
                        <td>
                            ${isSelf ? '<span style="font-size: 12px; color: var(--text-muted);">No actions</span>' : `
                                <div class="action-menu">
                                    <button class="action-menu-btn" onclick="toggleActionMenu('menu-user-${u.id}', event)">⋮</button>
                                    <div id="menu-user-${u.id}" class="action-menu-dropdown">
                                        ${u.status !== 'approved' ? `<button class="action-menu-item" onclick="approveUser('${u.id}')">Approve</button>` : ''}
                                        ${u.status !== 'revoked' ? `<button class="action-menu-item danger" onclick="revokeUser('${u.id}')">Revoke</button>` : ''}
                                        ${u.status === 'approved' ? `
                                            <button class="action-menu-item" onclick="promptTargetedMessage('${u.id}', '${escapeHTML(u.email)}')">Message</button>
                                            <button class="action-menu-item" onclick="openUserQuotaModal('${escapeHTML(u.email)}', ${u.rate_limit || 0})">Set Bandwidth Limit</button>
                                            <button class="action-menu-item" onclick="openUserResLimitModal('${escapeHTML(u.email)}', ${u.max_reservations !== undefined && u.max_reservations !== null ? u.max_reservations : ''})">Set Subdomain Limit</button>
                                            <button class="action-menu-item" onclick="openUserTunnelsLimitModal('${escapeHTML(u.email)}', ${u.max_tunnels !== undefined && u.max_tunnels !== null ? u.max_tunnels : ''})">Set Tunnels Limit</button>
                                            ${u.role === 'admin' ? `
                                                <button class="action-menu-item" onclick="changeUserRole('${escapeHTML(u.email)}', 'user')">Demote to User</button>
                                            ` : `
                                                <button class="action-menu-item" onclick="changeUserRole('${escapeHTML(u.email)}', 'admin')">Promote to Admin</button>
                                            `}
                                        ` : ''}
                                        ${u.totp_enabled ? `<button class="action-menu-item" onclick="adminResetMFA('${escapeHTML(u.email)}')">Reset MFA</button>` : ''}
                                        <button class="action-menu-item danger" onclick="adminDeleteUser('${escapeHTML(u.email)}')">Delete</button>
                                    </div>
                                </div>
                            `}
                        </td>
                    </tr>
                    `;
                });
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

                renderTable('registrations-table-body', pendingUsers, u => {
                    return `
                    <tr>
                        <td style="font-weight: 500;">${escapeHTML(u.email)} <span class="badge ${u.status === 'pending' ? 'warning' : ''}">${escapeHTML(u.status)}</span></td>
                        <td>${escapeHTML(u.first_name)} ${escapeHTML(u.last_name)}</td>
                        <td>${renderTimestamp(u.created_at)}</td>
                        <td>
                            <div class="action-menu">
                                <button class="action-menu-btn" onclick="toggleActionMenu('menu-reg-${u.id}', event)">⋮</button>
                                <div id="menu-reg-${u.id}" class="action-menu-dropdown">
                                    <button class="action-menu-item" onclick="approveRegistration('${u.id}')">Approve</button>
                                    <button class="action-menu-item danger" onclick="denyRegistration('${u.id}')">Deny</button>
                                </div>
                            </div>
                        </td>
                    </tr>
                    `;
                });
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
                renderTable('blacklist-table-body', list, b => `
                    <tr>
                        <td style="font-family: monospace;">${escapeHTML(b.ip_address)}</td>
                        <td>${escapeHTML(b.reason)}</td>
                        <td>${renderTimestamp(b.banned_at)}</td>
                        <td>
                            <div class="action-menu">
                                <button class="action-menu-btn" onclick="toggleActionMenu('menu-blacklist-${b.ip_address.replace(/\./g, '-')}', event)">⋮</button>
                                <div id="menu-blacklist-${b.ip_address.replace(/\./g, '-')}" class="action-menu-dropdown">
                                    <button class="action-menu-item danger" onclick="deleteBlacklist('${b.ip_address}')">Remove</button>
                                </div>
                            </div>
                        </td>
                    </tr>
                `);
            }
        }

        async function deleteBlacklist(ip) {
            await fetch(`/api/admin/blacklist/${encodeURIComponent(ip)}`, { method: 'DELETE' });
            loadBlacklist();
        }

        function exportAuditLog() {
            window.location.href = '/api/admin/audit/export';
        }

        function exportAnalyticsPDF() {
            window.print();
        }

        function copyDockerCmd() {
            const textEl = document.getElementById('docker-pull-text');
            if (!textEl) return;
            navigator.clipboard.writeText(textEl.textContent).then(() => {
                showToast("Docker pull command copied to clipboard!", "success");
            }).catch(err => {
                console.error("Failed to copy command", err);
            });
        }

        async function loadAudit() {
            const res = await fetch('/api/admin/audit?limit=100');
            if (res.ok) {
                const logs = await res.json() || [];
                renderTable('audit-table-body', logs, l => `
                    <tr>
                        <td>${renderTimestamp(l.created_at)}</td>
                        <td><span style="font-family: monospace; font-size: 13px; background: rgba(0,0,0,0.1); padding: 2px 6px; border-radius: 4px;">${escapeHTML(l.action)}</span></td>
                        <td>${escapeHTML(l.actor_id)}</td>
                        <td>${escapeHTML(l.target_id)}</td>
                        <td>${escapeHTML(l.ip_address)}</td>
                        <td>${escapeHTML(l.details)}</td>
                    </tr>
                `);
            }
        }

        async function loadAdminMagicLinks() {
            const res = await fetch('/api/admin/magic-links');
            if (res.ok) {
                const links = await res.json() || [];
                renderTable('magic-table-body', links, l => `
                    <tr>
                        <td style="font-weight: 500;">${escapeHTML(l.email)}</td>
                        <td>${escapeHTML(l.client_ip)}</td>
                        <td>${renderTimestamp(l.expires_at)}</td>
                        <td>${l.used_at ? renderTimestamp(l.used_at) : 'Unused'}</td>
                    </tr>
                `);
            }
        }

        async function loadMaintenanceStatus() {
            try {
                const res = await fetch('/api/admin/maintenance');
                if (res.ok) {
                    const data = await res.json();
                    updateMaintenanceModeUI(data.maintenance_mode, data.iron_curtain);
                }
            } catch (e) {
                console.error("Failed to load maintenance status", e);
            }
        }

        async function loadBackups() {
            const tbody = document.getElementById('backups-table-body');
            const res = await fetch('/api/admin/backups');
            if (!res.ok) {
                tbody.innerHTML = '<tr><td colspan="3" style="text-align:center;opacity:0.6;">Failed to load backups.</td></tr>';
                return;
            }
            const backups = await res.json();
            if (!backups || backups.length === 0) {
                tbody.innerHTML = '<tr><td colspan="3" style="text-align:center;opacity:0.6;">No backups found yet. The first backup runs on server startup.</td></tr>';
                return;
            }
            renderTable('backups-table-body', backups, b => {
                const sizeKB = (b.size_bytes / 1024).toFixed(1);
                return `<tr>
                    <td style="font-family:monospace; font-size:0.85em;">${escapeHTML(b.filename)}</td>
                    <td>${sizeKB} KB</td>
                    <td>${renderTimestamp(b.created_at)}</td>
                </tr>`;
            });
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
            document.getElementById('invite-language').value = 'en';
            document.getElementById('invite-modal').style.display = 'flex';
        }

        function closeInviteModal() {
            document.getElementById('invite-modal').style.display = 'none';
        }

        async function submitInviteUser() {
            const payload = {
                email: document.getElementById('invite-email').value,
                first_name: document.getElementById('invite-first-name').value,
                last_name: document.getElementById('invite-last-name').value,
                language_preference: document.getElementById('invite-language').value
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
            
            const neverOption = document.getElementById('token-expiry-never');
            const tokenExpiry = document.getElementById('token-expiry');
            if (currentUser && currentUser.role !== 'admin' && currentUser.role !== 'owner') {
                if (neverOption) neverOption.style.display = 'none';
                if (tokenExpiry.value === '0') {
                    tokenExpiry.value = '30';
                }
            } else {
                if (neverOption) neverOption.style.display = 'block';
            }
            
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

        function renderTimestamp(utcDateStr) {
            if (!utcDateStr || utcDateStr.startsWith('0001-01-01')) return 'Never';
            // Treat as UTC only if it doesn't already specify a timezone offset
            const hasTimezone = /Z$/i.test(utcDateStr) || /[+-]\d{2}(:?\d{2})?$/.test(utcDateStr);
            const parseStr = hasTimezone ? utcDateStr : (utcDateStr.includes(' ') ? utcDateStr.replace(' ', 'T') + 'Z' : utcDateStr + 'Z');
            const date = new Date(parseStr);
            if (isNaN(date.getTime())) return escapeHTML(utcDateStr);

            // Format neat UTC string: YYYY-MM-DD HH:mm:ss UTC
            const pad = (n) => String(n).padStart(2, '0');
            const utcTimeStr = `${date.getUTCFullYear()}-${pad(date.getUTCMonth() + 1)}-${pad(date.getUTCDate())} ${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())}:${pad(date.getUTCSeconds())} UTC`;

            // Format local browser time and timezone suffix
            let localTimeStr = "";
            try {
                localTimeStr = date.toLocaleString(undefined, { timeZoneName: 'short' });
            } catch(e) {
                localTimeStr = date.toLocaleString();
            }

            return `<span class="timestamp-tooltip" title="Local Browser Time: ${escapeHTML(localTimeStr)}" style="cursor: help; border-bottom: 1px dashed var(--text-muted); padding-bottom: 2px;">${escapeHTML(utcTimeStr)}</span>`;
        }

        function formatTimeRemaining(expiryDateStr, isRevoked) {
            if (isRevoked) return '—';
            if (!expiryDateStr || expiryDateStr.startsWith('0001-01-01')) return 'Never';
            
            const hasTimezone = /Z$/i.test(expiryDateStr) || /[+-]\d{2}(:?\d{2})?$/.test(expiryDateStr);
            const parseStr = hasTimezone ? expiryDateStr : (expiryDateStr.includes(' ') ? expiryDateStr.replace(' ', 'T') + 'Z' : expiryDateStr + 'Z');
            const expiryDate = new Date(parseStr);
            if (isNaN(expiryDate.getTime())) return '—';
            
            const now = new Date();
            const diffMs = expiryDate - now;
            if (diffMs <= 0) return 'Expired';
            
            const diffMins = Math.floor(diffMs / 60000);
            const diffHours = Math.floor(diffMins / 60);
            const diffDays = Math.floor(diffHours / 24);
            
            if (diffDays > 0) {
                const remainingHours = diffHours % 24;
                return `${diffDays}d ${remainingHours}h`;
            }
            if (diffHours > 0) {
                const remainingMins = diffMins % 60;
                return `${diffHours}h ${remainingMins}m`;
            }
            return `${diffMins > 0 ? diffMins : 1}m`;
        }

        function escapeHTML(str) {
            if (!str) return '';
            return String(str).replace(/[&<>'"]/g, tag => ({
                '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;'
            }[tag]));
        }

        init();
        // Targeted Messaging
        function promptTargetedMessage(uid, email) {
            document.getElementById('targeted-message-userid').value = uid;
            document.getElementById('targeted-message-desc').innerText = "Send a direct toast alert to " + email;
            document.getElementById('targeted-message-input').value = "";
            document.getElementById('targeted-message-modal').style.display = 'flex';
        }

        function closeTargetedModal() {
            document.getElementById('targeted-message-modal').style.display = 'none';
        }

        async function sendTargetedMessage() {
            const uid = document.getElementById('targeted-message-userid').value;
            const msg = document.getElementById('targeted-message-input').value.trim();
            if (!msg) return showToast("Please enter a message.");

            const res = await fetch('/api/admin/targeted-message', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({ user_id: uid, message: msg })
            });
            if (res.ok) {
                showToast("Targeted message sent!");
                closeTargetedModal();
            } else {
                showToast("Failed to send message.");
            }
        }

        async function clearTargetedMessage() {
            const uid = document.getElementById('targeted-message-userid').value;
            const res = await fetch('/api/admin/targeted-message', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({ user_id: uid, message: "" })
            });
            if (res.ok) {
                showToast("Targeted message cleared!");
                closeTargetedModal();
            } else {
                showToast("Failed to clear message.");
            }
        }

        async function acknowledgeTargetedMessage() {
            await fetch('/api/me/dismiss-message', { method: 'POST' });
        }

        // ==========================================
        // MULTI-FACTOR AUTHENTICATION (MFA)
        // ==========================================

        function renderMFAPanel() {
            const container = document.getElementById('mfa-status-container');
            if (!container) return;

            if (currentUser.totp_enabled) {
                container.innerHTML = `
                    <div style="display: flex; align-items: center; justify-content: space-between; padding: 12px; background: rgba(46,160,67,0.1); border: 1px solid rgba(46,160,67,0.25); border-radius: 6px; color: #2ea043; font-weight: 500; margin-bottom: 16px;">
                        <span>✓ Multi-Factor Authentication is currently Active</span>
                    </div>
                    <div style="margin-top: 16px;">
                        <p style="font-size: 13px; color: var(--text-muted); margin-bottom: 8px;">To deactivate MFA, please enter your 6-digit authenticator code below:</p>
                        <div style="display: flex; gap: 12px; max-width: 320px; align-items: center;">
                            <input type="text" id="mfa-disable-code" class="input-field" placeholder="123456" maxlength="6" style="text-align: center; letter-spacing: 2px; font-weight: bold; width: 140px; margin: 0;">
                            <button class="btn" style="color: var(--danger); border-color: var(--danger); margin: 0; padding: 8px 16px; width: auto;" onclick="disableMFA()">Disable MFA</button>
                        </div>
                    </div>
                `;
            } else {
                container.innerHTML = `
                    <div style="display: flex; align-items: center; justify-content: space-between; padding: 12px; background: rgba(255,255,255,0.02); border: 1px solid rgba(255,255,255,0.08); border-radius: 6px; color: var(--text-muted);">
                        <span>MFA is currently Disabled</span>
                        <button class="btn btn-primary" style="margin: 0; width: auto; padding: 6px 16px;" onclick="startMFASetup()">Enable MFA</button>
                    </div>
                `;
            }
        }

        let mfaSetupSecret = "";

        async function startMFASetup() {
            try {
                const res = await fetch('/api/mfa/setup');
                if (res.ok) {
                    const data = await res.json();
                    mfaSetupSecret = data.secret;
                    document.getElementById('mfa-secret-display').innerText = data.secret;
                    document.getElementById('mfa-qr-display').src = `https://api.qrserver.com/v1/create-qr-code/?size=180x180&data=${encodeURIComponent(data.otpauth_url)}`;
                    document.getElementById('mfa-verify-code').value = '';
                    document.getElementById('mfa-modal').style.display = 'flex';
                } else {
                    showToast("Failed to fetch MFA setup details.", "danger");
                }
            } catch (err) {
                showToast("Network error initiating MFA setup.", "danger");
            }
        }

        function closeMFAModal() {
            document.getElementById('mfa-modal').style.display = 'none';
        }

        async function confirmEnableMFA() {
            const code = document.getElementById('mfa-verify-code').value.trim();
            if (code.length !== 6) {
                return showToast("Please enter a 6-digit code.", "warning");
            }

            try {
                const res = await fetch('/api/mfa/enable', {
                    method: 'POST',
                    headers: {'Content-Type': 'application/json'},
                    body: JSON.stringify({ secret: mfaSetupSecret, code: code })
                });

                if (res.ok) {
                    currentUser.totp_enabled = true;
                    renderMFAPanel();
                    closeMFAModal();
                    showToast("MFA enabled successfully!", "success");
                } else {
                    const err = await res.json();
                    showToast(err.error || "Failed to verify setup code.", "danger");
                }
            } catch (err) {
                showToast("Network error completing setup.", "danger");
            }
        }

        async function disableMFA() {
            const code = document.getElementById('mfa-disable-code').value.trim();
            if (code.length !== 6) {
                return showToast("Please enter your 6-digit authenticator code.", "warning");
            }

            try {
                const res = await fetch('/api/mfa/disable', {
                    method: 'POST',
                    headers: {'Content-Type': 'application/json'},
                    body: JSON.stringify({ code: code })
                });

                if (res.ok) {
                    currentUser.totp_enabled = false;
                    renderMFAPanel();
                    showToast("MFA disabled successfully.", "success");
                } else {
                    const err = await res.json();
                    showToast(err.error || "Failed to disable MFA.", "danger");
                }
            } catch (err) {
                showToast("Network error deactivating MFA.", "danger");
            }
        }

        // Handle MFA login form submission
        const mfaForm = document.getElementById('mfa-form');
        if (mfaForm) {
            mfaForm.addEventListener('submit', async (e) => {
                e.preventDefault();
                const token = document.getElementById('mfa-temp-token').value;
                const code = document.getElementById('mfa-code-input').value.trim();
                const msg = document.getElementById('mfa-msg');
                const btn = document.getElementById('btn-verify-mfa');

                if (code.length !== 6) {
                    msg.innerText = "Please enter a 6-digit code.";
                    return;
                }

                btn.disabled = true;
                btn.innerText = "Verifying...";
                msg.innerText = "";

                try {
                    const res = await fetch('/api/auth/mfa-verify', {
                        method: 'POST',
                        headers: {'Content-Type': 'application/json'},
                        body: JSON.stringify({ temp_token: token, code: code })
                    });

                    if (res.ok) {
                        // MFA Success -> Load profile & show dashboard
                        const meRes = await fetch('/api/me');
                        if (meRes.ok) {
                            currentUser = await meRes.json();
                            document.getElementById('mfa-form').classList.add('hidden');
                            document.getElementById('login-screen').style.display = 'none';
                            showDashboard();
                        } else {
                            window.location.reload();
                        }
                    } else {
                        const err = await res.json();
                        msg.innerText = err.error || "Invalid verification code.";
                        btn.disabled = false;
                        btn.innerText = "Verify & Log In";
                    }
                } catch (err) {
                    msg.innerText = "A network error occurred.";
                    btn.disabled = false;
                    btn.innerText = "Verify & Log In";
                }
            });
        }

        async function adminResetMFA(id) {
            if (!confirm("Are you sure you want to disable Multi-Factor Authentication for this user? They will only need their magic link to log in.")) return;
            const res = await fetch('/api/admin/users/' + encodeURIComponent(id), { 
                method: 'PATCH', 
                headers: {'Content-Type': 'application/json'}, 
                body: JSON.stringify({ reset_mfa: true }) 
            });
            if (res.ok) {
                showToast("MFA has been disabled for the user.", "success");
                loadUsers();
            } else {
                const err = await res.json();
                showToast(err.error || "Failed to reset MFA", "danger");
            }
        }

        async function changeUserRole(id, role) {
            const verb = role === 'admin' ? 'promote' : 'demote';
            if (!confirm(`Are you sure you want to ${verb} ${id} to ${role}?`)) return;
            const res = await fetch('/api/admin/users/' + encodeURIComponent(id), { 
                method: 'PATCH', 
                headers: {'Content-Type': 'application/json'}, 
                body: JSON.stringify({ role: role }) 
            });
            if (res.ok) {
                showToast(`User successfully ${verb}d to ${role}.`, "success");
                loadUsers();
            } else {
                const err = await res.json();
                showToast(err.error || `Failed to ${verb} user.`, "danger");
            }
        }

        let globalMaintenanceActive = "false";
        let globalHardMaintenanceActive = false;

        function updateMaintenanceModeUI(active, hardActive) {
            globalMaintenanceActive = active;
            globalHardMaintenanceActive = !!hardActive;

            // Update Soft Maintenance UI
            const statusText = document.getElementById('maint-status-text');
            const toggleBtn = document.getElementById('btn-toggle-maint');
            const countdownSelect = document.getElementById('maint-countdown-select');
            const softInputs = document.getElementById('maint-soft-input-fields');

            if (statusText && toggleBtn) {
                if (active === "true") {
                    statusText.innerHTML = `Status: <span style="color: #ef4444; font-weight: 600;">ACTIVE (Bouncer checking IDs) 🔴</span>`;
                    toggleBtn.innerText = "Disable Soft Maintenance";
                    toggleBtn.className = "btn btn-outline";
                    toggleBtn.style.color = "var(--success)";
                    toggleBtn.style.borderColor = "var(--success)";
                    toggleBtn.style.background = "none";
                    if (countdownSelect) countdownSelect.style.display = "none";
                    if (softInputs) softInputs.style.display = "none";
                } else if (active === "pending") {
                    statusText.innerHTML = `Status: <span style="color: #f59e0b; font-weight: 600;">PENDING COUNTDOWN ⏳</span>`;
                    toggleBtn.innerText = "Cancel Soft Maintenance";
                    toggleBtn.className = "btn btn-outline";
                    toggleBtn.style.color = "var(--danger)";
                    toggleBtn.style.borderColor = "var(--danger)";
                    toggleBtn.style.background = "none";
                    if (countdownSelect) countdownSelect.style.display = "none";
                    if (softInputs) softInputs.style.display = "none";
                } else {
                    statusText.innerHTML = `Status: <span style="color: var(--text-muted);">INACTIVE (All welcome) 🟢</span>`;
                    toggleBtn.innerText = "Enable Soft Maintenance";
                    toggleBtn.className = "btn btn-primary";
                    toggleBtn.style.color = "white";
                    toggleBtn.style.borderColor = "var(--primary)";
                    toggleBtn.style.background = "var(--primary)";
                    if (countdownSelect) countdownSelect.style.display = "block";
                    if (softInputs) softInputs.style.display = "flex";
                }
            }

            // Update Hard Maintenance (Iron Curtain / Fire Curtain) UI
            const hardStatusText = document.getElementById('maint-hard-status-text');
            const hardToggleBtn = document.getElementById('btn-toggle-hard-maint');
            const hardInputs = document.getElementById('maint-hard-input-fields');

            if (hardStatusText && hardToggleBtn) {
                if (hardActive) {
                    hardStatusText.innerHTML = `Status: <span style="color: #ef4444; font-weight: 600;">ACTIVE (Fire Curtain down) 🔴</span>`;
                    hardToggleBtn.innerText = "Disable Iron Curtain";
                    hardToggleBtn.className = "btn btn-outline";
                    hardToggleBtn.style.color = "var(--success)";
                    hardToggleBtn.style.borderColor = "var(--success)";
                    hardToggleBtn.style.background = "none";
                    if (hardInputs) hardInputs.style.display = "none";
                } else {
                    hardStatusText.innerHTML = `Status: <span style="color: var(--text-muted);">INACTIVE (Open gate) 🟢</span>`;
                    hardToggleBtn.innerText = "Enable Iron Curtain";
                    hardToggleBtn.className = "btn";
                    hardToggleBtn.style.color = "white";
                    hardToggleBtn.style.borderColor = "var(--danger)";
                    hardToggleBtn.style.background = "var(--danger)";
                    if (hardInputs) hardInputs.style.display = "flex";
                }
            }
        }

        async function toggleSoftMaintenanceMode() {
            let nextState = true;
            if (globalMaintenanceActive === "true" || globalMaintenanceActive === "pending") {
                nextState = false;
            }

            const verb = nextState ? "enable" : "disable/cancel";
            let countdownVal = 0;
            if (nextState) {
                const countdownSelect = document.getElementById('maint-countdown-select');
                if (countdownSelect) {
                    countdownVal = parseInt(countdownSelect.value) || 0;
                }
            }

            const actionVal = document.getElementById('maint-soft-action').value.trim();
            const reasonVal = document.getElementById('maint-soft-reason').value.trim();
            const durationVal = parseInt(document.getElementById('maint-soft-duration').value) || 30;

            const promptMsg = nextState 
                ? (countdownVal > 0 
                    ? `Are you sure you want to schedule Gateway Soft Maintenance Mode to start in ${countdownVal} minutes?\n\nThis will show a warning banner to users and activate when the timer hits 0.`
                    : `Are you sure you want to enable Gateway Soft Maintenance Mode IMMEDIATELY?\n\nThis will instantly close all standard tunnels, reject new connections, and block standard logins!`)
                : "Are you sure you want to disable/cancel Gateway Maintenance Mode?\n\nThis will restore standard gateway routing, logins, and tunnel connections.";

            if (!confirm(promptMsg)) return;

            try {
                const payload = { 
                    enabled: nextState,
                    iron_curtain: false,
                    action: actionVal,
                    reason: reasonVal,
                    duration: durationVal
                };
                if (nextState && countdownVal > 0) {
                    payload.countdown_minutes = countdownVal;
                }

                const res = await fetch('/api/admin/maintenance', {
                    method: 'POST',
                    headers: {'Content-Type': 'application/json'},
                    body: JSON.stringify(payload)
                });

                if (res.ok) {
                    const data = await res.json();
                    updateMaintenanceModeUI(data.maintenance_mode, data.iron_curtain);
                    showToast(`Soft Maintenance Mode successfully updated!`, "success");
                    loadTunnels(); // Refresh tunnels lists in case they were kicked
                } else {
                    const err = await res.json();
                    showToast(err.error || "Failed to update maintenance mode", "danger");
                }
            } catch (e) {
                showToast("Network error toggling maintenance mode", "danger");
            }
        }

        async function toggleHardMaintenanceMode() {
            let nextState = true;
            if (globalHardMaintenanceActive) {
                nextState = false;
            }

            if (nextState) {
                const actionVal = document.getElementById('maint-hard-action').value.trim();
                const reasonVal = document.getElementById('maint-hard-reason').value.trim();
                const durationVal = parseInt(document.getElementById('maint-hard-duration').value) || 60;

                const firstConfirm = confirm(
                    "⚠️ WARNING: Activating Nginx Iron Curtain Mode will completely lock down the server.\n\n" +
                    "This blocks ALL traffic including the Admin Dashboard itself. You will be immediately disconnected " +
                    "and will not be able to turn this off from this website.\n\n" +
                    "To restore service, you MUST log into the VPS via SSH and run the disable-maintenance scripts.\n\n" +
                    "Are you sure you want to proceed?"
                );
                if (!firstConfirm) return;

                const secondConfirm = prompt(
                    "To confirm immediate lockdown, please type 'LOCKOUT' in all caps:"
                );
                if (secondConfirm !== "LOCKOUT") {
                    showToast("Lockdown cancelled: confirmation word did not match.", "warning");
                    return;
                }

                try {
                    const payload = {
                        enabled: true,
                        iron_curtain: true,
                        action: actionVal,
                        reason: reasonVal,
                        duration: durationVal
                    };

                    const res = await fetch('/api/admin/maintenance', {
                        method: 'POST',
                        headers: {'Content-Type': 'application/json'},
                        body: JSON.stringify(payload)
                    });

                    if (res.ok) {
                        const data = await res.json();
                        updateMaintenanceModeUI(data.maintenance_mode, data.iron_curtain);
                        showToast("Nginx Iron Curtain activated. You will be disconnected shortly.", "danger");
                        setTimeout(() => {
                            window.location.reload();
                        }, 1500);
                    } else {
                        const err = await res.json();
                        showToast(err.error || "Failed to activate Iron Curtain", "danger");
                    }
                } catch (e) {
                    showToast("Network error activating Iron Curtain", "danger");
                }
            } else {
                const confirmDisable = confirm(
                    "Are you sure you want to disable Nginx Iron Curtain Mode?\n\n" +
                    "Note: If you are seeing this, either the server is not actually behind the Nginx block or you are accessing it via a bypassed endpoint. Disabling will remove the trigger files."
                );
                if (!confirmDisable) return;

                try {
                    const payload = {
                        enabled: false,
                        iron_curtain: true
                    };

                    const res = await fetch('/api/admin/maintenance', {
                        method: 'POST',
                        headers: {'Content-Type': 'application/json'},
                        body: JSON.stringify(payload)
                    });

                    if (res.ok) {
                        const data = await res.json();
                        updateMaintenanceModeUI(data.maintenance_mode, data.iron_curtain);
                        showToast("Nginx Iron Curtain disabled successfully.", "success");
                    } else {
                        const err = await res.json();
                        showToast(err.error || "Failed to disable Iron Curtain", "danger");
                    }
                } catch (e) {
                    showToast("Network error disabling Iron Curtain", "danger");
                }
            }
        }

        function openDeleteAccountModal() {
            if (!currentUser) return;
            document.getElementById('delete-acc-email-hint').innerText = currentUser.email;
            document.getElementById('delete-acc-confirm-input').value = "";
            document.getElementById('delete-account-modal').style.display = 'flex';
        }

        function closeDeleteAccountModal() {
            document.getElementById('delete-account-modal').style.display = 'none';
        }

        async function submitSelfDeleteAccount() {
            const inputVal = document.getElementById('delete-acc-confirm-input').value.trim();
            if (!inputVal) return showToast("Please type your email to confirm.", "danger");

            if (inputVal.toLowerCase() !== currentUser.email.toLowerCase()) {
                return showToast("Entered email address does not match your account email.", "danger");
            }

            try {
                const res = await fetch('/api/me/delete-account', {
                    method: 'POST',
                    headers: {'Content-Type': 'application/json'},
                    body: JSON.stringify({ confirm_email: inputVal })
                });

                if (res.ok) {
                    alert("Your account has been permanently deleted and anonymised in accordance with your Right to Be Forgotten. You will now be redirected.");
                    window.location.reload();
                } else {
                    const err = await res.json();
                    showToast(err.error || "Failed to delete account", "danger");
                }
            } catch (e) {
                showToast("Network error deleting account", "danger");
            }
        }

        async function adminDeleteUser(email) {
            const promptMsg = `⚠️ GDPR RIGHT TO BE FORGOTTEN REQUEST\n\nAre you sure you want to PERMANENTLY DELETE and ANONYMISE the account for ${email}?\n\nThis will instantly revoke all their tokens, close active tunnels, completely delete their profile, and permanently anonymise their logs and bandwidth metrics! This action is absolutely irreversible.`;
            if (!confirm(promptMsg)) return;

            const secondPrompt = `Type "DELETE" (all caps) to confirm you want to permanently delete and anonymise ${email}:`;
            const confirmation = prompt(secondPrompt);
            if (confirmation !== "DELETE") {
                return showToast("Account deletion cancelled (incorrect confirmation string).", "warning");
            }

            try {
                const res = await fetch('/api/admin/users/' + encodeURIComponent(email), {
                    method: 'DELETE'
                });

                if (res.ok) {
                    showToast(`User ${email} has been permanently deleted and anonymised.`, "success");
                    loadUsers();
                } else {
                    const err = await res.json();
                    showToast(err.error || "Failed to delete user", "danger");
                }
            } catch (e) {
                showToast("Network error deleting user", "danger");
            }
        }

        async function changePortalLanguage(lang) {
            currentLanguage = lang;
            try {
                const res = await fetch('/api/i18n?lang=' + encodeURIComponent(lang));
                if (res.ok) {
                    const bundle = await res.json();
                    document.querySelectorAll('[data-i18n]').forEach(el => {
                        const key = el.getAttribute('data-i18n');
                        if (bundle[key]) {
                            el.innerText = bundle[key];
                        }
                    });

                    // Set HTML direction (RTL support for Arabic/Hebrew)
                    const dir = (lang === 'ar' || lang === 'he') ? 'rtl' : 'ltr';
                    document.documentElement.dir = dir;

                    // Dynamically update the footer privacy/cookie links with ?lang=...
                    const pl = document.getElementById('footer-privacy-link');
                    if (pl && pl.getAttribute('href').startsWith('/privacy')) {
                        pl.href = '/privacy?lang=' + encodeURIComponent(lang);
                    }
                    const cl = document.getElementById('footer-cookie-link');
                    if (cl && cl.getAttribute('href').startsWith('/cookies')) {
                        cl.href = '/cookies?lang=' + encodeURIComponent(lang);
                    }
                }
            } catch (e) {
                console.error("Failed to load language", e);
            }
        }

        // USER QUOTA MODAL CONTROLLERS
        let activeQuotaEmail = '';
        function openUserQuotaModal(email, currentLimit) {
            activeQuotaEmail = email;
            document.getElementById('user-quota-email-hint').innerText = email;
            document.getElementById('user-quota-input').value = currentLimit || '';
            document.getElementById('user-quota-modal').style.display = 'flex';
        }

        function closeUserQuotaModal() {
            document.getElementById('user-quota-modal').style.display = 'none';
        }

        async function submitUserQuota() {
            const limit = parseInt(document.getElementById('user-quota-input').value) || 0;
            const res = await fetch('/api/admin/users/' + encodeURIComponent(activeQuotaEmail), {
                method: 'PATCH',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({ rate_limit: limit })
            });
            if (res.ok) {
                showToast('User rate limit quota updated successfully', 'success');
                closeUserQuotaModal();
                loadUsers();
            } else {
                const err = await res.json();
                showToast('Failed to update quota: ' + (err.error || 'Unknown error'), 'danger');
            }
        }

        // ACTIVE TUNNEL OVERRIDE MODAL CONTROLLERS
        let activeOverrideHost = '';
        function openTunnelOverrideModal(host, currentLimit) {
            activeOverrideHost = host;
            document.getElementById('tunnel-override-host-hint').innerText = host;
            document.getElementById('tunnel-override-input').value = currentLimit || '';
            document.getElementById('tunnel-override-modal').style.display = 'flex';
        }

        function closeTunnelOverrideModal() {
            document.getElementById('tunnel-override-modal').style.display = 'none';
        }

        async function submitTunnelOverride() {
            const limit = parseInt(document.getElementById('tunnel-override-input').value) || 0;
            const res = await fetch('/api/admin/leases/rate-limit', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({ host: activeOverrideHost, rate_limit: limit })
            });
            if (res.ok) {
                showToast('Active tunnel rate limit overridden successfully', 'success');
                closeTunnelOverrideModal();
                
                // Instantly reload active leases to update UI stats
                const meRes = await fetch('/api/me');
                if (meRes.ok) {
                    currentUser = await meRes.json();
                    loadTunnels();
                }
            } else {
                const err = await res.json();
                showToast('Failed to override limit: ' + (err.error || 'Unknown error'), 'danger');
            }
        }

        // USER DETAILS & TUNNELS MODAL CONTROLLERS
        function openUserDetailsModal(userJsonEncoded) {
            const u = JSON.parse(decodeURIComponent(userJsonEncoded));
            
            // Set user email and name
            document.getElementById('detail-user-email').innerText = u.email;
            document.getElementById('detail-user-name').innerText = `${u.first_name || ''} ${u.last_name || ''}`.trim() || 'N/A';
            
            // Set role and status badges
            const roleEl = document.getElementById('detail-user-role');
            roleEl.innerText = u.role;
            roleEl.className = `badge ${u.role === 'admin' ? 'success' : ''}`;
            
            const statusEl = document.getElementById('detail-user-status');
            statusEl.innerText = u.status;
            statusEl.className = `badge ${u.status === 'approved' ? 'success' : (u.status === 'revoked' ? 'danger' : 'warning')}`;
            
            // Account Origin
            const originEl = document.getElementById('detail-user-origin');
            const m = (u.auth_method || 'magic link').toLowerCase();
            let originHTML = '';
            if (m === 'invite') {
                originHTML = '<span class="badge" style="background: rgba(99,102,241,0.15); color: #818cf8; border: 1px solid rgba(99,102,241,0.3);">✉ Invite</span>';
            } else if (m === 'registration') {
                originHTML = '<span class="badge" style="background: rgba(16,185,129,0.15); color: #34d399; border: 1px solid rgba(16,185,129,0.3);">📝 Registration</span>';
            } else if (m.startsWith('sso - liferay') || m === 'liferay') {
                originHTML = '<span class="badge" style="background: rgba(30,120,220,0.15); color: #60a5fa; border: 1px solid rgba(30,120,220,0.3);">🔑 SSO · Liferay</span>';
            } else if (m.startsWith('sso - keycloak') || m === 'keycloak') {
                originHTML = '<span class="badge" style="background: rgba(239,68,68,0.15); color: #f87171; border: 1px solid rgba(239,68,68,0.3);">🔑 SSO · Keycloak</span>';
            } else {
                originHTML = `<span class="badge">${escapeHTML(u.auth_method || 'Magic Link')}</span>`;
            }
            originEl.innerHTML = originHTML;
            
            // Joined Date
            document.getElementById('detail-user-joined').innerHTML = renderTimestamp(u.created_at);
            
            // API Quota
            document.getElementById('detail-user-quota').innerText = u.rate_limit ? `${u.rate_limit} RPS` : 'Unlimited';

            // Connected Tunnels Table
            const tunnels = u.active_tunnels || [];
            document.getElementById('detail-user-tunnels-count').innerText = tunnels.length;
            
            const tbody = document.getElementById('detail-user-tunnels-tbody');
            tbody.innerHTML = '';
            
            if (tunnels.length === 0) {
                tbody.innerHTML = `<tr><td colspan="4" style="text-align: center; padding: 24px; color: var(--text-muted); font-size: 13px;">No active tunnels connected.</td></tr>`;
            } else {
                tunnels.forEach(t => {
                    const tr = document.createElement('tr');
                    tr.style.borderBottom = '1px solid rgba(255,255,255,0.03)';
                    
                    const publicUrl = `https://${t.full_host}`;
                    const connectedTime = renderTimestamp(t.created_at);
                    
                    tr.innerHTML = `
                        <td style="padding: 12px; vertical-align: middle;">
                            <div style="font-weight: 600; font-family: monospace; font-size: 13px; color: var(--text);">${escapeHTML(t.subdomain_prefix)}</div>
                            <div style="font-size: 11px; color: var(--text-muted); margin-top: 2px;">Local Port: ${t.local_port}</div>
                        </td>
                        <td style="padding: 12px; vertical-align: middle;">
                            <a href="${publicUrl}" target="_blank" style="color: var(--primary); text-decoration: none; font-size: 13px; font-family: monospace; word-break: break-all;">${publicUrl}</a>
                            <div style="font-size: 11px; color: var(--text-muted); margin-top: 2px;">IP: ${escapeHTML(t.client_ip)} | Connected: ${connectedTime}</div>
                        </td>
                        <td style="padding: 12px; vertical-align: middle; font-size: 12px; color: var(--text-muted);">
                            <div>📥 In: <strong style="color: var(--text);">${formatBytes(t.bytes_in)}</strong></div>
                            <div style="margin-top: 2px;">📤 Out: <strong style="color: var(--text);">${formatBytes(t.bytes_out)}</strong></div>
                        </td>
                        <td style="padding: 12px; vertical-align: middle; text-align: right;">
                            <button class="btn" style="padding: 4px 10px; font-size: 12px; color: var(--danger); border-color: var(--danger);" onclick="kickTunnelFromUserModal('${escapeHTML(t.subdomain_prefix)}', '${userJsonEncoded}')">Kick</button>
                        </td>
                    `;
                    tbody.appendChild(tr);
                });
            }
            
            document.getElementById('user-details-modal').style.display = 'flex';
        }

        function closeUserDetailsModal() {
            document.getElementById('user-details-modal').style.display = 'none';
        }

        async function kickTunnelFromUserModal(subdomain, userJsonEncoded) {
            const u = JSON.parse(decodeURIComponent(userJsonEncoded));
            if (confirm(`Are you sure you want to kick the tunnel lease for subdomain "${subdomain}"?`)) {
                const res = await fetch(`/api/admin/leases/${encodeURIComponent(subdomain)}`, { method: 'DELETE' });
                if (res.ok) {
                    showToast(`Kicked tunnel subdomain "${subdomain}"`, 'success');
                    
                    // Reload users list to refresh main UI
                    await loadUsers();
                    
                    // Fetch fresh list of users to reload the active modal with current data
                    const uRes = await fetch('/api/admin/users');
                    if (uRes.ok) {
                        const allUsers = await uRes.json();
                        const updatedUser = allUsers.find(item => item.id === u.id);
                        if (updatedUser) {
                            // If user still exists, reload modal with updated data
                            const updatedJson = encodeURIComponent(JSON.stringify(updatedUser));
                            openUserDetailsModal(updatedJson);
                            return;
                        }
                    }
                    // Fallback: close modal if user or info is gone
                    closeUserDetailsModal();
                } else {
                    const err = await res.json();
                    showToast('Failed to kick tunnel: ' + (err.error || 'Unknown error'), 'danger');
                }
            }
        }

        let activeDetailTunnelSubdomain = null;

        async function kickActiveTunnel(subdomain) {
            if (confirm(`Are you sure you want to kick the tunnel lease for subdomain "${subdomain}"?`)) {
                const res = await fetch(`/api/admin/leases/${encodeURIComponent(subdomain)}`, { method: 'DELETE' });
                if (res.ok) {
                    showToast(`Kicked tunnel subdomain "${subdomain}"`, 'success');
                    
                    // Close details modal if the active detail tunnel was kicked
                    if (activeDetailTunnelSubdomain === subdomain) {
                        closeTunnelDetailsModal();
                    }

                    // Instantly reload active leases to update UI stats
                    const meRes = await fetch('/api/me');
                    if (meRes.ok) {
                        currentUser = await meRes.json();
                        loadTunnels();
                    }
                } else {
                    const err = await res.json();
                    showToast('Failed to kick tunnel: ' + (err.error || 'Unknown error'), 'danger');
                }
            }
        }

        let refreshIntervalId = null;

        async function openTunnelDetailsModal(tunnelJsonEncoded) {
            const t = JSON.parse(decodeURIComponent(tunnelJsonEncoded));
            populateTunnelDetails(t);
            document.getElementById('tunnel-details-modal').style.display = 'flex';

            if (refreshIntervalId) clearInterval(refreshIntervalId);
            refreshIntervalId = setInterval(() => refreshTunnelDetails(true), 3000);

            // Asynchronously fetch fresh data to update metrics instantly on open
            try {
                const meRes = await fetch('/api/me');
                if (meRes.ok) {
                    currentUser = await meRes.json();
                    loadTunnels(); // Keep table in sync
                    const freshTunnel = (currentUser.tunnels || []).find(x => x.subdomain_prefix === t.subdomain_prefix);
                    if (freshTunnel) {
                        populateTunnelDetails(freshTunnel);
                    }
                }
            } catch (e) {
                console.error("Failed to fetch fresh tunnel details on open", e);
            }
        }

        function populateTunnelDetails(t) {
            activeDetailTunnelSubdomain = t.subdomain_prefix;
            
            // Set Host Link
            const hostEl = document.getElementById('detail-tunnel-host');
            hostEl.innerText = t.full_host;
            hostEl.href = `https://${t.full_host}`;

            // Basic details
            document.getElementById('detail-tunnel-subdomain').innerText = t.subdomain_prefix;
            
            const statusEl = document.getElementById('detail-tunnel-status');
            statusEl.innerText = t.status;
            statusEl.className = `badge ${t.status === 'up' ? 'success' : ''}`;

            document.getElementById('detail-tunnel-limit').innerText = t.rate_limit ? `${t.rate_limit} RPS` : 'Unlimited';
            document.getElementById('detail-tunnel-connected').innerHTML = renderTimestamp(t.created_at);
            document.getElementById('detail-tunnel-bytes-in').innerText = formatBytes(t.bytes_in);
            document.getElementById('detail-tunnel-bytes-out').innerText = formatBytes(t.bytes_out);
            document.getElementById('detail-tunnel-client-ip').innerText = t.client_ip || 'N/A';

            // Populate active visitor IPs
            const visitorTbody = document.getElementById('detail-tunnel-visitor-ips-tbody');
            if (visitorTbody) {
                visitorTbody.innerHTML = '';
                const ips = t.visitor_ips || [];
                if (ips.length === 0) {
                    visitorTbody.innerHTML = `<tr><td style="color: var(--text-muted); text-align: center; padding: 8px 0;">No active visitor connections (last 30s)</td></tr>`;
                } else {
                    ips.forEach(ip => {
                        const tr = document.createElement('tr');
                        tr.innerHTML = `<td style="font-family: monospace; padding: 4px 0;"><span style="color: #10b981; margin-right: 6px;">●</span>${escapeHTML(ip)}</td>`;
                        visitorTbody.appendChild(tr);
                    });
                }
            }

            // Role / Admin features check
            const isAdmin = currentUser && (currentUser.role === 'admin' || currentUser.role === 'owner');
            
            const ownerContainer = document.getElementById('detail-tunnel-owner-container');
            const overrideBtn = document.getElementById('detail-tunnel-override-btn');
            const kickBtn = document.getElementById('detail-tunnel-kick-btn');

            if (isAdmin) {
                ownerContainer.style.display = 'block';
                document.getElementById('detail-tunnel-owner').innerText = t.user_id || 'N/A';
                overrideBtn.style.display = 'inline-block';
                overrideBtn.onclick = () => {
                    closeTunnelDetailsModal();
                    openTunnelOverrideModal(t.full_host, t.rate_limit || 0);
                };
                kickBtn.style.display = 'block';
            } else {
                ownerContainer.style.display = 'none';
                overrideBtn.style.display = 'none';
                kickBtn.style.display = 'none';
            }
        }

        function closeTunnelDetailsModal() {
            document.getElementById('tunnel-details-modal').style.display = 'none';
            activeDetailTunnelSubdomain = null;
            if (refreshIntervalId) {
                clearInterval(refreshIntervalId);
                refreshIntervalId = null;
            }
        }

        function copyTunnelUrlToClipboard() {
            const host = document.getElementById('detail-tunnel-host').innerText;
            if (host) {
                navigator.clipboard.writeText("https://" + host);
                showToast("Copied tunnel URL to clipboard!", "success");
            }
        }

        async function refreshTunnelDetails(silent = false) {
            if (!activeDetailTunnelSubdomain) return;
            
            // Reload user data to get fresh tunnel metrics
            const meRes = await fetch('/api/me');
            if (meRes.ok) {
                currentUser = await meRes.json();
                loadTunnels(); // Refresh main view table
                
                // Find matching active tunnel to update the modal
                const freshTunnel = (currentUser.tunnels || []).find(t => t.subdomain_prefix === activeDetailTunnelSubdomain);
                if (freshTunnel) {
                    populateTunnelDetails(freshTunnel);
                    if (!silent) showToast("Tunnel metrics refreshed!", "success");
                } else {
                    if (!silent) showToast("Tunnel connection is no longer active.", "warning");
                    closeTunnelDetailsModal();
                }
            }
        }

        function kickFromDetails() {
            if (activeDetailTunnelSubdomain) {
                kickActiveTunnel(activeDetailTunnelSubdomain);
            }
        }

        async function loadWhatsNew() {
            try {
                const res = await fetch('/static/whats-new.json');
                if (res.ok) {
                    const data = await res.json();
                    document.getElementById('whats-new-title').innerText = `What's New in ${data.version || 'this version'}`;
                    const list = document.getElementById('whats-new-list');
                    list.innerHTML = '';
                    if (data.features && data.features.length > 0) {
                        data.features.forEach(f => {
                            const li = document.createElement('li');
                            const colonIdx = f.indexOf(':');
                            if (colonIdx !== -1) {
                                const boldPart = f.substring(0, colonIdx + 1);
                                const regularPart = f.substring(colonIdx + 1);
                                li.innerHTML = `<strong>${escapeHTML(boldPart)}</strong>${escapeHTML(regularPart)}`;
                            } else {
                                li.textContent = f;
                            }
                            list.appendChild(li);
                        });
                    } else {
                        list.innerHTML = '<li>No recent feature updates documented.</li>';
                    }
                }
            } catch (e) {
                console.error("Failed to load What's New content", e);
            }
        }

        // SUBDOMAIN RESERVATIONS SYSTEM
        let domainsLoaded = false;
        async function loadDomains() {
            if (domainsLoaded) return;
            try {
                const res = await fetch('/api/domains');
                if (res.ok) {
                    const domains = await res.json() || [];
                    const select = document.getElementById('res-domain');
                    if (select) {
                        select.innerHTML = '';
                        domains.forEach(d => {
                            const opt = document.createElement('option');
                            opt.value = d;
                            opt.textContent = d;
                            select.appendChild(opt);
                        });
                        domainsLoaded = true;
                    }
                }
            } catch (e) {
                console.error("Failed to load domains", e);
            }
        }

        async function loadReservations() {
            await loadDomains();
            
            try {
                const res = await fetch('/api/portal/reservations');
                if (res.ok) {
                    const data = await res.json();
                    const list = data.reservations || [];
                    const limit = data.limit !== undefined && data.limit !== null ? data.limit : 0;
                    const used = data.used || 0;

                    // Progress bar & quota text
                    if (limit < 0) {
                        document.getElementById('reservation-quota-text').innerText = `${used} / ∞ subdomains reserved`;
                        document.getElementById('reservation-quota-bar').style.width = `0%`;
                    } else {
                        document.getElementById('reservation-quota-text').innerText = `${used} / ${limit} subdomains reserved`;
                        const percent = limit > 0 ? (used / limit) * 100 : 0;
                        document.getElementById('reservation-quota-bar').style.width = `${Math.min(percent, 100)}%`;
                    }

                    const formContainer = document.getElementById('reservation-form-container');
                    const warningAlert = document.getElementById('reservation-quota-warning');

                    if (limit >= 0 && used >= limit) {
                        if (formContainer) {
                            formContainer.querySelectorAll('input, select, button').forEach(el => el.disabled = true);
                        }
                        if (warningAlert) warningAlert.classList.remove('hidden');
                    } else {
                        if (formContainer) {
                            formContainer.querySelectorAll('input, select, button').forEach(el => el.disabled = false);
                        }
                        if (warningAlert) warningAlert.classList.add('hidden');
                    }

                    // Set headers dynamically BEFORE calling renderTable
                    const resHeaders = document.getElementById('reservations-table-headers');
                    const isAdminOrOwner = currentUser && (currentUser.role === 'admin' || currentUser.role === 'owner');
                    if (resHeaders) {
                        resHeaders.innerHTML = `
                            <tr>
                                <th data-sort="subdomain">Host</th>
                                ${isAdminOrOwner ? '<th data-sort="user_id">Owner</th>' : ''}
                                <th>Status</th>
                                <th data-sort="expires_at">Expires</th>
                                <th style="text-align: right;">Action</th>
                            </tr>
                        `;
                    }

                    renderTable('reservations-table-body', list, item => {
                        let statusText = '<span class="badge success">Active</span>';
                        let expiresText = 'Never (Permanent)';
                        
                        if (item.expires_at) {
                            const expiryDate = new Date(item.expires_at);
                            expiresText = renderTimestamp(item.expires_at);
                            
                            // Check if expired (in quarantine)
                            if (expiryDate < new Date()) {
                                statusText = '<span class="badge danger" style="background: rgba(239, 68, 68, 0.15); color: #f87171; border: 1px solid rgba(239, 68, 68, 0.3);">Quarantined</span>';
                            } else if (item.extension_requested) {
                                statusText = '<span class="badge warning" style="background: rgba(245, 158, 11, 0.15); color: #fbbf24; border: 1px solid rgba(245, 158, 11, 0.3);">Extension Requested</span>';
                            }
                        }

                        const canExtend = !!(item.expires_at && !item.extension_requested);

                        const host = `${item.subdomain}.${item.domain}`;
                        const hostLink = `<a href="https://${host}" target="_blank" class="host-link" style="color: var(--primary); font-family: monospace; font-weight: 600; text-decoration: none;">${escapeHTML(host)}</a>`;
                        const copyBtn = `
                            <button class="btn-copy" onclick="copyToClipboard('${escapeHTML(host)}')" style="background: none; border: none; color: var(--text-muted); cursor: pointer; padding: 2px 6px; font-size: 13px; transition: color 0.2s;" title="Copy Host to Clipboard">
                                📋
                            </button>
                        `;

                        return `
                            <tr>
                                <td>
                                    <div style="display: flex; align-items: center; gap: 4px;">
                                        ${hostLink}
                                        ${copyBtn}
                                    </div>
                                </td>
                                ${isAdminOrOwner ? `<td style="font-family: monospace; font-size: 13px;">${escapeHTML(item.user_id || 'N/A')}</td>` : ''}
                                <td>${statusText}</td>
                                <td>${expiresText}</td>
                                <td style="text-align: right;">
                                    <div class="action-menu">
                                        <button class="action-menu-btn" onclick="toggleActionMenu('menu-reservation-${item.id}', event)">⋮</button>
                                        <div id="menu-reservation-${item.id}" class="action-menu-dropdown">
                                            ${canExtend ? `<button class="action-menu-item" onclick="requestExtension('${item.id}')">Extend</button>` : ''}
                                            <button class="action-menu-item danger" onclick="releaseReservation('${item.id}', '${escapeHTML(host)}')">Release</button>
                                        </div>
                                    </div>
                                </td>
                            </tr>
                        `;
                    });

                    // Admin checks
                    const adminSection = document.getElementById('admin-reservations-section');
                    if (isAdminOrOwner) {
                        if (adminSection) adminSection.classList.remove('hidden');
                        loadAdminExtensions();
                    } else {
                        if (adminSection) adminSection.classList.add('hidden');
                    }
                }
            } catch (e) {
                console.error("Failed to load reservations", e);
            }
        }

        async function generateRandomSubdomain() {
            const style = document.getElementById('res-style-select').value;
            try {
                const res = await fetch(`/api/portal/generate-subdomain?style=${encodeURIComponent(style)}`);
                if (res.ok) {
                    const data = await res.json();
                    document.getElementById('res-subdomain').value = data.subdomain || '';
                }
            } catch (e) {
                console.error("Failed to generate random subdomain", e);
            }
        }

        async function reserveSubdomain() {
            const sub = document.getElementById('res-subdomain').value.trim().toLowerCase();
            const dom = document.getElementById('res-domain').value;
            if (!sub) return showToast("Please enter or generate a subdomain prefix", "danger");

            try {
                const res = await fetch('/api/portal/reservations', {
                    method: 'POST',
                    headers: {'Content-Type': 'application/json'},
                    body: JSON.stringify({ subdomain: sub, domain: dom })
                });

                if (res.ok) {
                    showToast(`Subdomain ${sub}.${dom} successfully reserved!`, "success");
                    document.getElementById('res-subdomain').value = '';
                    loadReservations();
                } else {
                    const err = await res.json();
                    showToast("Failed to reserve: " + (err.error || "Unknown error"), "danger");
                }
            } catch (e) {
                console.error("Failed to reserve subdomain", e);
            }
        }

        async function requestExtension(id) {
            try {
                const res = await fetch(`/api/portal/reservations/${encodeURIComponent(id)}/request-extension`, {
                    method: 'POST'
                });
                if (res.ok) {
                    showToast("Extension request submitted to administrators.", "success");
                    loadReservations();
                } else {
                    const err = await res.json();
                    showToast("Failed to request extension: " + (err.error || "Unknown error"), "danger");
                }
            } catch (e) {
                console.error("Failed to request extension", e);
            }
        }

        async function releaseReservation(id, fqdn) {
            if (confirm(`Are you sure you want to release the subdomain "${fqdn}"? This cannot be undone.`)) {
                try {
                    const res = await fetch(`/api/portal/reservations/${encodeURIComponent(id)}`, {
                        method: 'DELETE'
                    });
                    if (res.ok) {
                        showToast(`Released subdomain reservation "${fqdn}"`, "success");
                        loadReservations();
                    } else {
                        const err = await res.json();
                        showToast("Failed to release: " + (err.error || "Unknown error"), "danger");
                    }
                } catch (e) {
                    console.error("Failed to release subdomain", e);
                }
            }
        }

        async function loadAdminExtensions() {
            try {
                const res = await fetch('/api/admin/reservations/extensions');
                if (res.ok) {
                    const list = await res.json() || [];
                    const tbody = document.getElementById('admin-extensions-table-body');
                    tbody.innerHTML = '';

                    if (list.length === 0) {
                        tbody.innerHTML = `<tr><td colspan="5" style="text-align: center; padding: 24px; color: var(--text-muted);">No pending extension requests.</td></tr>`;
                    } else {
                        list.forEach(item => {
                            const expiresVal = item.expires_at ? renderTimestamp(item.expires_at) : 'Never';
                            const row = `
                                <tr>
                                    <td><span style="font-weight: 500;">${escapeHTML(item.user_email || 'User ' + item.user_id)}</span></td>
                                    <td style="font-family: monospace;">${escapeHTML(item.subdomain)}</td>
                                    <td style="font-family: monospace;">${escapeHTML(item.domain)}</td>
                                    <td>${expiresVal}</td>
                                    <td style="text-align: right;">
                                        <div class="action-menu">
                                            <button class="action-menu-btn" onclick="toggleActionMenu('menu-admin-ext-${item.id}', event)">⋮</button>
                                            <div id="menu-admin-ext-${item.id}" class="action-menu-dropdown">
                                                <button class="action-menu-item" onclick="approveExtension('${item.id}', 30, false)">Approve +30 Days</button>
                                                <button class="action-menu-item" onclick="approveExtension('${item.id}', 0, true)">Approve Permanent</button>
                                                <button class="action-menu-item danger" onclick="demoteReservation('${item.id}')">Demote</button>
                                            </div>
                                        </div>
                                    </td>
                                </tr>
                            `;
                            tbody.innerHTML += row;
                        });
                    }
                }
            } catch (e) {
                console.error("Failed to load admin extensions", e);
            }
        }

        async function approveExtension(id, days, permanent) {
            try {
                const res = await fetch(`/api/admin/reservations/${encodeURIComponent(id)}/approve-extension`, {
                    method: 'POST',
                    headers: {'Content-Type': 'application/json'},
                    body: JSON.stringify({ days: days, permanent: permanent })
                });

                if (res.ok) {
                    showToast("Extension approved successfully!", "success");
                    loadReservations();
                } else {
                    const err = await res.json();
                    showToast("Failed to approve extension: " + (err.error || "Unknown error"), "danger");
                }
            } catch (e) {
                console.error("Failed to approve extension", e);
            }
        }

        async function demoteReservation(id) {
            try {
                const res = await fetch(`/api/admin/reservations/${encodeURIComponent(id)}/demote`, {
                    method: 'POST'
                });

                if (res.ok) {
                    showToast("Reservation successfully demoted to standard 7 days.", "success");
                    loadReservations();
                } else {
                    const err = await res.json();
                    showToast("Failed to demote: " + (err.error || "Unknown error"), "danger");
                }
            } catch (e) {
                console.error("Failed to demote reservation", e);
            }
        }

        // ADMINISTRATIVE RESERVATIONS LIMIT OVERRIDES
        let activeResLimitEmail = '';
        function openUserResLimitModal(email, currentLimit) {
            activeResLimitEmail = email;
            document.getElementById('user-res-limit-email-hint').innerText = email;
            document.getElementById('user-res-limit-input').value = currentLimit || '';
            document.getElementById('user-res-limit-modal').style.display = 'flex';
        }

        function closeUserResLimitModal() {
            document.getElementById('user-res-limit-modal').style.display = 'none';
        }

        async function submitUserResLimit() {
            const val = document.getElementById('user-res-limit-input').value;
            const limit = val !== '' ? parseInt(val) : null;
            
            try {
                const res = await fetch(`/api/admin/users/${encodeURIComponent(activeResLimitEmail)}/limit`, {
                    method: 'POST',
                    headers: {'Content-Type': 'application/json'},
                    body: JSON.stringify({ max_reservations: limit })
                });

                if (res.ok) {
                    showToast("User reservations limit updated successfully", "success");
                    closeUserResLimitModal();
                    loadUsers();
                } else {
                    const err = await res.json();
                    showToast("Failed to update limit: " + (err.error || "Unknown error"), "danger");
                }
            } catch (e) {
                console.error("Failed to submit quota override", e);
            }
        }

        // ADMINISTRATIVE ACTIVE TUNNELS LIMIT OVERRIDES
        let activeTunnelsLimitEmail = '';
        function openUserTunnelsLimitModal(email, currentLimit) {
            activeTunnelsLimitEmail = email;
            document.getElementById('user-tunnels-limit-email-hint').innerText = email;
            document.getElementById('user-tunnels-limit-input').value = currentLimit || '';
            document.getElementById('user-tunnels-limit-modal').style.display = 'flex';
        }
        window.openUserTunnelsLimitModal = openUserTunnelsLimitModal;

        function closeUserTunnelsLimitModal() {
            document.getElementById('user-tunnels-limit-modal').style.display = 'none';
        }
        window.closeUserTunnelsLimitModal = closeUserTunnelsLimitModal;

        async function submitUserTunnelsLimit() {
            const val = document.getElementById('user-tunnels-limit-input').value;
            const limit = val !== '' ? parseInt(val) : null;
            
            try {
                const res = await fetch(`/api/admin/users/${encodeURIComponent(activeTunnelsLimitEmail)}/tunnels-limit`, {
                    method: 'POST',
                    headers: {'Content-Type': 'application/json'},
                    body: JSON.stringify({ max_tunnels: limit })
                });

                if (res.ok) {
                    showToast("User active tunnels limit updated successfully", "success");
                    closeUserTunnelsLimitModal();
                    loadUsers();
                } else {
                    const err = await res.json();
                    showToast("Failed to update limit: " + (err.error || "Unknown error"), "danger");
                }
            } catch (e) {
                console.error("Failed to submit tunnels limit override", e);
            }
        }
        window.submitUserTunnelsLimit = submitUserTunnelsLimit;

        // COPY TO CLIPBOARD HELPER
        function copyToClipboard(text) {
            navigator.clipboard.writeText(text).then(() => {
                showToast("Copied to clipboard!", "success");
            }).catch(err => {
                console.error("Failed to copy text", err);
            });
        }
        window.copyToClipboard = copyToClipboard;

        // ADMINISTRATIVE TOKEN EXTENSION
        async function extendToken(id, days) {
            try {
                const res = await fetch(`/api/admin/tokens/${id}/extend`, {
                    method: 'POST',
                    headers: {'Content-Type': 'application/json'},
                    body: JSON.stringify({ days })
                });
                if (res.ok) {
                    showToast("Token expiration updated successfully", "success");
                    loadTokens();
                } else {
                    const err = await res.json();
                    showToast("Failed to extend token: " + (err.error || "Unknown error"), "danger");
                }
            } catch(e) {
                console.error("Failed to extend token", e);
            }
        }
        window.extendToken = extendToken;

        // CSV EXPORT UTILITIES
        async function exportReservationsCSV() {
            try {
                const res = await fetch('/api/portal/reservations');
                if (res.ok) {
                    const data = await res.json();
                    const list = data.reservations || [];
                    let csv = "ID,Subdomain,Domain,Owner,Status,ExpiresAt,CreatedAt\n";
                    list.forEach(item => {
                        let status = "Active";
                        if (item.expires_at) {
                            const expiryDate = new Date(item.expires_at);
                            if (expiryDate < new Date()) {
                                status = "Quarantined";
                            } else if (item.extension_requested) {
                                status = "Extension Requested";
                            }
                        }
                        const row = [
                            item.id,
                            item.subdomain,
                            item.domain,
                            item.user_id || "",
                            status,
                            item.expires_at || "Never",
                            item.created_at || ""
                        ].map(val => `"${String(val).replace(/"/g, '""')}"`).join(",");
                        csv += row + "\n";
                    });
                    
                    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
                    const link = document.createElement("a");
                    link.href = URL.createObjectURL(blob);
                    link.setAttribute("download", "subdomain_reservations.csv");
                    document.body.appendChild(link);
                    link.click();
                    document.body.removeChild(link);
                    showToast("Reservations exported successfully", "success");
                } else {
                    showToast("Failed to load reservations for export", "danger");
                }
            } catch (e) {
                console.error("Failed to export CSV", e);
            }
        }
        window.exportReservationsCSV = exportReservationsCSV;

        async function exportTokensCSV() {
            try {
                const res = await fetch('/api/tokens');
                if (res.ok) {
                    const list = await res.json() || [];
                    let csv = "ID,Name,Prefix,Owner,ExpiresAt,CreatedAt\n";
                    list.forEach(item => {
                        const row = [
                            item.id,
                            item.name,
                            item.token_prefix,
                            item.user_id || "",
                            item.expires_at || "Never",
                            item.created_at || ""
                        ].map(val => `"${String(val).replace(/"/g, '""')}"`).join(",");
                        csv += row + "\n";
                    });
                    
                    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
                    const link = document.createElement("a");
                    link.href = URL.createObjectURL(blob);
                    link.setAttribute("download", "personal_access_tokens.csv");
                    document.body.appendChild(link);
                    link.click();
                    document.body.removeChild(link);
                    showToast("Tokens exported successfully", "success");
                } else {
                    showToast("Failed to load tokens for export", "danger");
                }
            } catch (e) {
                console.error("Failed to export CSV", e);
            }
        }
        window.exportTokensCSV = exportTokensCSV;

        function closeAllActionMenus() {
            document.querySelectorAll('.action-menu-dropdown').forEach(el => {
                el.classList.remove('show');
                if (el.dataset.originalParentId) {
                    const origParent = document.getElementById(el.dataset.originalParentId);
                    if (origParent && document.body.contains(origParent)) {
                        if (el.parentElement === document.body) {
                            origParent.appendChild(el);
                        }
                    } else {
                        // The original parent is gone (e.g. table redrawn), clean up from body to prevent leak
                        if (el.parentElement === document.body) {
                            el.remove();
                        }
                    }
                }
            });
        }
        window.closeAllActionMenus = closeAllActionMenus;

        window.toggleActionMenu = function(menuId, event) {
            if (event) event.stopPropagation();
            const menu = document.getElementById(menuId);
            const show = menu && !menu.classList.contains('show');
            
            // Close all other dropdowns
            closeAllActionMenus();
            
            if (menu && show && event) {
                // Save original parent so we can restore it when closed
                const parent = menu.parentElement;
                if (parent && parent !== document.body) {
                    if (!parent.id) {
                        parent.id = 'parent-' + menuId;
                    }
                    menu.dataset.originalParentId = parent.id;
                }
                
                // Move to body before calculating position and showing
                document.body.appendChild(menu);
                menu.classList.add('show');
                
                const btn = event.currentTarget || (event.target && event.target.closest('.action-menu-btn')) || event.target;
                const btnRect = btn.getBoundingClientRect();
                
                const menuWidth = menu.offsetWidth || 160;
                const menuHeight = menu.offsetHeight || 120;
                
                // Horizontal placement (right-aligned to the button by default)
                let leftPos = btnRect.right - menuWidth;
                if (leftPos < 10) {
                    leftPos = 10;
                }
                if (leftPos + menuWidth > window.innerWidth - 10) {
                    leftPos = window.innerWidth - 10 - menuWidth;
                }
                
                // Vertical placement (dropdown by default, dropup if it overflows bottom)
                let topPos = btnRect.bottom + 4;
                if (topPos + menuHeight > window.innerHeight - 10) {
                    topPos = btnRect.top - menuHeight - 4;
                }
                
                menu.style.top = `${topPos}px`;
                menu.style.left = `${leftPos}px`;
                menu.style.right = 'auto';
                menu.style.bottom = 'auto';
                menu.style.margin = '0';
            }
        };

        window.addEventListener('click', () => {
            closeAllActionMenus();
        });

        // Hide dropdowns when scrolling any container to prevent floating detached menus
        window.addEventListener('scroll', () => {
            closeAllActionMenus();
        }, { capture: true, passive: true });
