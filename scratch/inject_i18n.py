import re
import sys

def main():
    with open('pkg/client/inspector.html', 'r', encoding='utf-8') as f:
        html = f.read()
        
    with open('scratch/client_i18n.js', 'r', encoding='utf-8') as f:
        js_code = f.read()
        
    # Inject language selector into header
    header_regex = r'(<span id="maint-label">Maintenance</span>\s*<label class="toggle-switch">)'
    lang_selector = '''<select id="lang-selector" onchange="setLanguage(this.value)" style="background: rgba(0,0,0,0.3); border: 1px solid var(--border); color: var(--text-muted); padding: 4px 8px; border-radius: 4px; font-size: 11px; outline: none; cursor: pointer; margin-right: 10px;">
                        <option value="en">English</option>
                        <option value="es">Español</option>
                        <option value="de">Deutsch</option>
                        <option value="fr">Français</option>
                        <option value="pt">Português</option>
                        <option value="ja">日本語</option>
                    </select>
                    '''
    html = re.sub(header_regex, lang_selector + r'\1', html)
    
    # Inject JS
    html = html.replace('<script>', '<script>\n' + js_code)
    
    # Replace static tags
    replacements = {
        'Inspector\n': '<span data-i18n="client_inspector">Inspector</span>\n',
        '<span id="maint-label">Maintenance</span>': '<span id="maint-label" data-i18n="client_maint">Maintenance</span>',
        'Listening on localhost:4040': '<span data-i18n="client_listening">Listening on localhost:4040</span>', # Wait, listening string has dynamic port. The js should handle it or we don't translate it. Let's not translate it for now.
        'onclick="switchView(\'traffic\')">Traffic Log</div>': 'onclick="switchView(\'traffic\')" data-i18n="client_traffic_log">Traffic Log</div>',
        'onclick="switchView(\'settings\')">Settings</div>': 'onclick="switchView(\'settings\')" data-i18n="client_settings">Settings</div>',
        '>Access Control</div>': ' data-i18n="client_access_control">Access Control</div>',
        '>Passcode Protection</label>': ' data-i18n="client_passcode">Passcode Protection</label>',
        'placeholder="No passcode set (Public)"': 'placeholder="No passcode set (Public)" data-i18n="client_passcode_placeholder"',
        '>IP Address Whitelist</label>': ' data-i18n="client_ip_whitelist">IP Address Whitelist</label>',
        'placeholder="e.g. 192.168.1.1, 10.0.0.0/24"': 'placeholder="e.g. 192.168.1.1, 10.0.0.0/24" data-i18n="client_ip_placeholder"',
        '>Combinator Mode</label>': ' data-i18n="client_combinator">Combinator Mode</label>',
        '>Bypass passcode if IP matched (OR)</option>': ' data-i18n="client_mode_or">Bypass passcode if IP matched (OR)</option>',
        '>Require both passcode & IP match (AND)</option>': ' data-i18n="client_mode_and">Require both passcode & IP match (AND)</option>',
        '>Save Access Control</button>': ' data-i18n="client_save_access">Save Access Control</button>',
        '>Public Endpoints</div>': ' data-i18n="client_public_urls">Public Endpoints</div>',
        '>Select a request to view details</div>': ' data-i18n="client_select_req">Select a request to view details</div>',
        '>Server URL <span class="help-icon" title="The public address of the remote Liferay Tunnel Gateway. E.g. https://tunnel.lfr-demo.se">?</span></label>': ' data-i18n="client_server_url" data-i18n-help="client_server_url_help">Server URL <span class="help-icon" title="The public address of the remote Liferay Tunnel Gateway. E.g. https://tunnel.lfr-demo.se">?</span></label>',
        '>Authentication Token <span class="help-icon" title="Your personal token from the gateway to authenticate your tunnel. Leave blank to use a token file.">?</span></label>': ' data-i18n="client_auth_token" data-i18n-help="client_auth_token_help">Authentication Token <span class="help-icon" title="Your personal token from the gateway to authenticate your tunnel. Leave blank to use a token file.">?</span></label>',
        'placeholder="Enter token (masked if set)"': 'placeholder="Enter token (masked if set)" data-i18n="client_auth_placeholder"',
        '>Local Target Port <span class="help-icon" title="The port of your local dev server running on your machine (e.g. 8080 for Liferay).">?</span></label>': ' data-i18n="client_local_port" data-i18n-help="client_local_port_help">Local Target Port <span class="help-icon" title="The port of your local dev server running on your machine (e.g. 8080 for Liferay).">?</span></label>',
        '>Local Target Host <span class="help-icon" title="The hostname of your local dev server. Usually \'localhost\' or \'host.docker.internal\' if proxying into a container.">?</span></label>': ' data-i18n="client_local_host" data-i18n-help="client_local_host_help">Local Target Host <span class="help-icon" title="The hostname of your local dev server. Usually \'localhost\' or \'host.docker.internal\' if proxying into a container.">?</span></label>',
        '>Requested Subdomain (Optional) <span class="help-icon" title="A custom name for your public URL (e.g. entering \'peter\' gets peter.lfr-demo.online). If empty, a random one is assigned.">?</span></label>': ' data-i18n="client_req_subdomain" data-i18n-help="client_req_subdomain_help">Requested Subdomain (Optional) <span class="help-icon" title="A custom name for your public URL (e.g. entering \'peter\' gets peter.lfr-demo.online). If empty, a random one is assigned.">?</span></label>',
        '>Preserve incoming Host header <span class="help-icon" title="If checked, forwards the public URL Host header to your local server instead of rewriting it to the Target Host. Useful for Liferay Virtual Instances.">?</span></label>': ' data-i18n="client_preserve_host" data-i18n-help="client_preserve_host_help">Preserve incoming Host header <span class="help-icon" title="If checked, forwards the public URL Host header to your local server instead of rewriting it to the Target Host. Useful for Liferay Virtual Instances.">?</span></label>',
        '>Allow insecure local SSL (Skip TLS Verification) <span class="help-icon" title="If checking against an HTTPS port (e.g. 443), this bypasses strict certificate checks. Required for most self-signed localhost certificates.">?</span></label>': ' data-i18n="client_insecure_ssl" data-i18n-help="client_insecure_ssl_help">Allow insecure local SSL (Skip TLS Verification) <span class="help-icon" title="If checking against an HTTPS port (e.g. 443), this bypasses strict certificate checks. Required for most self-signed localhost certificates.">?</span></label>',
        '>Save Configuration</button>': ' data-i18n="client_save_config">Save Configuration</button>',
        '>Configuration saved successfully!</div>': ' data-i18n="client_config_saved">Configuration saved successfully!</div>'
    }
    
    for old, new in replacements.items():
        if old not in html:
            print(f"WARNING: Could not find '{old}'")
        html = html.replace(old, new)
        
    # Append setLanguage() call at the end of window.onload or end of script
    html = html.replace('setInterval(fetchState, 1000);', 'setInterval(fetchState, 1000);\n            setLanguage(currentLang);')
    
    with open('pkg/client/inspector.html', 'w', encoding='utf-8') as f:
        f.write(html)
        
    print("Injection complete!")

if __name__ == '__main__':
    main()
