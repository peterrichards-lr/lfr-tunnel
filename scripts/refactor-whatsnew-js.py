import re

with open('pkg/server/static/dashboard.js', 'r') as f:
    js = f.read()

new_func = """        async function loadWhatsNew() {
            try {
                const res = await fetch('/static/whats-new.json');
                if (res.ok) {
                    const data = await res.json();
                    
                    const list = document.getElementById('whats-new-list');
                    list.innerHTML = '';
                    
                    // If it's an array, it's the new format
                    if (Array.isArray(data)) {
                        document.getElementById('whats-new-title').innerText = "What's New";
                        data.forEach(release => {
                            const header = document.createElement('h4');
                            header.style.marginTop = '8px';
                            header.style.marginBottom = '8px';
                            header.style.fontSize = '14px';
                            header.style.color = 'var(--text-main)';
                            header.innerHTML = `${escapeHTML(release.version)} <span style="font-size: 12px; color: var(--text-muted); font-weight: normal;">(${escapeHTML(release.release_date)})</span>`;
                            list.appendChild(header);
                            
                            const ul = document.createElement('ul');
                            ul.style.paddingLeft = '20px';
                            ul.style.marginBottom = '16px';
                            
                            if (release.features && release.features.length > 0) {
                                release.features.forEach(f => {
                                    const li = document.createElement('li');
                                    const colonIdx = f.indexOf(':');
                                    if (colonIdx !== -1) {
                                        const boldPart = f.substring(0, colonIdx + 1);
                                        const regularPart = f.substring(colonIdx + 1);
                                        li.innerHTML = `<strong>${escapeHTML(boldPart)}</strong>${escapeHTML(regularPart)}`;
                                    } else {
                                        li.textContent = f;
                                    }
                                    ul.appendChild(li);
                                });
                            } else {
                                ul.innerHTML = '<li>No changes documented.</li>';
                            }
                            list.appendChild(ul);
                        });
                    } else {
                        // Fallback to old format just in case
                        document.getElementById('whats-new-title').innerText = `What's New in ${escapeHTML(data.version || 'this version')}`;
                        const ul = document.createElement('ul');
                        ul.style.paddingLeft = '20px';
                        if (data.features && data.features.length > 0) {
                            data.features.forEach(f => {
                                const li = document.createElement('li');
                                li.textContent = f;
                                ul.appendChild(li);
                            });
                        }
                        list.appendChild(ul);
                    }
                }
            } catch (e) {
                console.error("Failed to load What's New content", e);
            }
        }"""

# Replace the old loadWhatsNew
js = re.sub(r'async function loadWhatsNew\(\) \{.*?(?=\n        // SUBDOMAIN)', new_func, js, flags=re.DOTALL)

with open('pkg/server/static/dashboard.js', 'w') as f:
    f.write(js)

print("Updated dashboard.js")
