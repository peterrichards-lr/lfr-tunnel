const clientTranslations = {
    "en": {
        "client_inspector": "Inspector",
        "client_maint": "Maintenance",
        "client_traffic_log": "Traffic Log",
        "client_settings": "Settings",
        "client_access_control": "Access Control",
        "client_passcode": "Passcode Protection",
        "client_passcode_placeholder": "No passcode set (Public)",
        "client_ip_whitelist": "IP Address Whitelist",
        "client_ip_placeholder": "e.g. 192.168.1.1, 10.0.0.0/24",
        "client_combinator": "Combinator Mode",
        "client_mode_or": "Bypass passcode if IP matched (OR)",
        "client_mode_and": "Require both passcode & IP match (AND)",
        "client_save_access": "Save Access Control",
        "client_public_urls": "Public Endpoints",
        "client_select_req": "Select a request to view details",
        "client_server_url": "Server URL",
        "client_server_url_help": "The public address of the remote Liferay Tunnel Gateway. E.g. https://tunnel.lfr-demo.se",
        "client_auth_token": "Authentication Token",
        "client_auth_token_help": "Your personal token from the gateway to authenticate your tunnel. Leave blank to use a token file.",
        "client_auth_placeholder": "Enter token (masked if set)",
        "client_local_port": "Local Target Port",
        "client_local_port_help": "The port of your local dev server running on your machine (e.g. 8080 for Liferay).",
        "client_local_host": "Local Target Host",
        "client_local_host_help": "The hostname of your local dev server. Usually 'localhost' or 'host.docker.internal' if proxying into a container.",
        "client_req_subdomain": "Requested Subdomain (Optional)",
        "client_req_subdomain_help": "A custom name for your public URL (e.g. entering 'peter' gets peter.lfr-demo.online). If empty, a random one is assigned.",
        "client_preserve_host": "Preserve incoming Host header",
        "client_preserve_host_help": "If checked, forwards the public URL Host header to your local server instead of rewriting it to the Target Host. Useful for Liferay Virtual Instances.",
        "client_insecure_ssl": "Allow insecure local SSL (Skip TLS Verification)",
        "client_insecure_ssl_help": "If checking against an HTTPS port (e.g. 443), this bypasses strict certificate checks. Required for most self-signed localhost certificates.",
        "client_save_config": "Save Configuration",
        "client_config_saved": "Configuration saved successfully!"
    },
    "es": {
        "client_inspector": "Inspector",
        "client_maint": "Mantenimiento",
        "client_traffic_log": "Registro de Tráfico",
        "client_settings": "Ajustes",
        "client_access_control": "Control de Acceso",
        "client_passcode": "Protección por Contraseña",
        "client_passcode_placeholder": "Sin contraseña (Público)",
        "client_ip_whitelist": "Lista Blanca de IP",
        "client_ip_placeholder": "ej. 192.168.1.1, 10.0.0.0/24",
        "client_combinator": "Modo Combinador",
        "client_mode_or": "Ignorar contraseña si la IP coincide (O)",
        "client_mode_and": "Requerir contraseña e IP (Y)",
        "client_save_access": "Guardar Acceso",
        "client_public_urls": "Endpoints Públicos",
        "client_select_req": "Seleccione una solicitud para ver los detalles",
        "client_server_url": "URL del Servidor",
        "client_server_url_help": "La dirección pública del Gateway remoto. Ej. https://tunnel.lfr-demo.se",
        "client_auth_token": "Token de Autenticación",
        "client_auth_token_help": "Su token personal para autenticar el túnel. Deje en blanco para usar archivo.",
        "client_auth_placeholder": "Ingrese el token (oculto)",
        "client_local_port": "Puerto de Destino",
        "client_local_port_help": "El puerto de su servidor local (ej. 8080 para Liferay).",
        "client_local_host": "Host de Destino",
        "client_local_host_help": "El nombre de host de su servidor local. Usualmente 'localhost' o 'host.docker.internal'.",
        "client_req_subdomain": "Subdominio Solicitado (Opcional)",
        "client_req_subdomain_help": "Un nombre personalizado para su URL pública.",
        "client_preserve_host": "Preservar encabezado Host",
        "client_preserve_host_help": "Reenvía el encabezado Host de la URL pública a su servidor local.",
        "client_insecure_ssl": "Permitir SSL local inseguro",
        "client_insecure_ssl_help": "Omite las comprobaciones estrictas de certificados. Necesario para localhost.",
        "client_save_config": "Guardar Configuración",
        "client_config_saved": "¡Configuración guardada!"
    },
    "de": {
        "client_inspector": "Inspektor",
        "client_maint": "Wartung",
        "client_traffic_log": "Verkehrsprotokoll",
        "client_settings": "Einstellungen",
        "client_access_control": "Zugangskontrolle",
        "client_passcode": "Passwortschutz",
        "client_passcode_placeholder": "Kein Passwort gesetzt (Öffentlich)",
        "client_ip_whitelist": "IP-Whitelist",
        "client_ip_placeholder": "z.B. 192.168.1.1, 10.0.0.0/24",
        "client_combinator": "Kombinatormodus",
        "client_mode_or": "Passwort überspringen wenn IP übereinstimmt (ODER)",
        "client_mode_and": "Passwort & IP erforderlich (UND)",
        "client_save_access": "Zugang speichern",
        "client_public_urls": "Öffentliche Endpunkte",
        "client_select_req": "Wählen Sie eine Anfrage aus",
        "client_server_url": "Server-URL",
        "client_server_url_help": "Die öffentliche Adresse des Remote-Gateways. Z.B. https://tunnel.lfr-demo.se",
        "client_auth_token": "Authentifizierungstoken",
        "client_auth_token_help": "Ihr persönliches Token zur Authentifizierung. Leer lassen für Token-Datei.",
        "client_auth_placeholder": "Token eingeben (maskiert)",
        "client_local_port": "Lokaler Zielport",
        "client_local_port_help": "Der Port Ihres lokalen Servers (z.B. 8080 für Liferay).",
        "client_local_host": "Lokaler Zielhost",
        "client_local_host_help": "Der Hostname Ihres lokalen Servers. Normalerweise 'localhost'.",
        "client_req_subdomain": "Gewünschte Subdomain (Optional)",
        "client_req_subdomain_help": "Ein benutzerdefinierter Name für Ihre öffentliche URL.",
        "client_preserve_host": "Eingehenden Host-Header beibehalten",
        "client_preserve_host_help": "Leitet den Host-Header der öffentlichen URL an Ihren lokalen Server weiter.",
        "client_insecure_ssl": "Unsicheres lokales SSL zulassen",
        "client_insecure_ssl_help": "Überspringt Zertifikatsprüfungen. Erforderlich für localhost-Zertifikate.",
        "client_save_config": "Konfiguration speichern",
        "client_config_saved": "Konfiguration erfolgreich gespeichert!"
    },
    "fr": {
        "client_inspector": "Inspecteur",
        "client_maint": "Maintenance",
        "client_traffic_log": "Journal de Trafic",
        "client_settings": "Paramètres",
        "client_access_control": "Contrôle d'Accès",
        "client_passcode": "Code d'accès",
        "client_passcode_placeholder": "Aucun code d'accès (Public)",
        "client_ip_whitelist": "Liste Blanche IP",
        "client_ip_placeholder": "ex. 192.168.1.1, 10.0.0.0/24",
        "client_combinator": "Mode Combinateur",
        "client_mode_or": "Ignorer le code si l'IP correspond (OU)",
        "client_mode_and": "Code ET IP requis (ET)",
        "client_save_access": "Enregistrer l'accès",
        "client_public_urls": "Points de terminaison",
        "client_select_req": "Sélectionnez une requête",
        "client_server_url": "URL du Serveur",
        "client_server_url_help": "L'adresse publique de la passerelle. Ex. https://tunnel.lfr-demo.se",
        "client_auth_token": "Jeton d'authentification",
        "client_auth_token_help": "Votre jeton personnel pour authentifier le tunnel.",
        "client_auth_placeholder": "Entrez le jeton (masqué)",
        "client_local_port": "Port Local",
        "client_local_port_help": "Le port de votre serveur local (ex. 8080 pour Liferay).",
        "client_local_host": "Hôte Local",
        "client_local_host_help": "Le nom d'hôte de votre serveur local. Généralement 'localhost'.",
        "client_req_subdomain": "Sous-domaine demandé (Optionnel)",
        "client_req_subdomain_help": "Un nom personnalisé pour votre URL publique.",
        "client_preserve_host": "Préserver l'en-tête Host",
        "client_preserve_host_help": "Transmet l'en-tête Host de l'URL publique à votre serveur local.",
        "client_insecure_ssl": "Autoriser SSL local non sécurisé",
        "client_insecure_ssl_help": "Contourne les vérifications de certificats. Requis pour localhost.",
        "client_save_config": "Enregistrer la Configuration",
        "client_config_saved": "Configuration enregistrée avec succès !"
    },
    "pt": {
        "client_inspector": "Inspetor",
        "client_maint": "Manutenção",
        "client_traffic_log": "Tráfego",
        "client_settings": "Configurações",
        "client_access_control": "Controle de Acesso",
        "client_passcode": "Senha de Proteção",
        "client_passcode_placeholder": "Sem senha (Público)",
        "client_ip_whitelist": "Lista Branca de IP",
        "client_ip_placeholder": "ex. 192.168.1.1, 10.0.0.0/24",
        "client_combinator": "Modo Combinador",
        "client_mode_or": "Ignorar senha se o IP corresponder (OU)",
        "client_mode_and": "Exigir senha e IP (E)",
        "client_save_access": "Salvar Acesso",
        "client_public_urls": "Endpoints Públicos",
        "client_select_req": "Selecione uma solicitação",
        "client_server_url": "URL do Servidor",
        "client_server_url_help": "O endereço público do Gateway remoto. Ex. https://tunnel.lfr-demo.se",
        "client_auth_token": "Token de Autenticação",
        "client_auth_token_help": "Seu token pessoal para autenticar o túnel.",
        "client_auth_placeholder": "Insira o token (oculto)",
        "client_local_port": "Porta Local",
        "client_local_port_help": "A porta do seu servidor local (ex. 8080 para Liferay).",
        "client_local_host": "Host Local",
        "client_local_host_help": "O nome de host do seu servidor local. Geralmente 'localhost'.",
        "client_req_subdomain": "Subdomínio Solicitado (Opcional)",
        "client_req_subdomain_help": "Um nome personalizado para sua URL pública.",
        "client_preserve_host": "Preservar cabeçalho Host",
        "client_preserve_host_help": "Encaminha o cabeçalho Host da URL pública para o servidor local.",
        "client_insecure_ssl": "Permitir SSL local inseguro",
        "client_insecure_ssl_help": "Ignora verificações rigorosas de certificado. Necessário para localhost.",
        "client_save_config": "Salvar Configuração",
        "client_config_saved": "Configuração salva com sucesso!"
    },
    "ja": {
        "client_inspector": "インスペクター",
        "client_maint": "メンテナンス",
        "client_traffic_log": "トラフィックログ",
        "client_settings": "設定",
        "client_access_control": "アクセス制御",
        "client_passcode": "パスコード保護",
        "client_passcode_placeholder": "パスコードなし（公開）",
        "client_ip_whitelist": "IPホワイトリスト",
        "client_ip_placeholder": "例 192.168.1.1, 10.0.0.0/24",
        "client_combinator": "コンビネーターモード",
        "client_mode_or": "IPが一致すればパスコードをバイパス (OR)",
        "client_mode_and": "パスコードとIPの両方が必要 (AND)",
        "client_save_access": "アクセスを保存",
        "client_public_urls": "パブリックエンドポイント",
        "client_select_req": "詳細を表示するリクエストを選択してください",
        "client_server_url": "サーバー URL",
        "client_server_url_help": "リモートゲートウェイの公開アドレス。例 https://tunnel.lfr-demo.se",
        "client_auth_token": "認証トークン",
        "client_auth_token_help": "トンネルを認証するための個人のトークンです。",
        "client_auth_placeholder": "トークンを入力（マスクされます）",
        "client_local_port": "ローカルターゲットポート",
        "client_local_port_help": "ローカルサーバーのポート（例 Liferayの8080）。",
        "client_local_host": "ローカルターゲットホスト",
        "client_local_host_help": "ローカルサーバーのホスト名。通常は 'localhost'。",
        "client_req_subdomain": "希望するサブドメイン（オプション）",
        "client_req_subdomain_help": "公開URLのカスタム名です。",
        "client_preserve_host": "着信Hostヘッダーを保持する",
        "client_preserve_host_help": "公開URLのHostヘッダーをローカルサーバーに転送します。",
        "client_insecure_ssl": "安全でないローカルSSLを許可する",
        "client_insecure_ssl_help": "厳密な証明書チェックをバイパスします。localhostに必要です。",
        "client_save_config": "設定を保存",
        "client_config_saved": "設定が正常に保存されました！"
    }
};

let currentLang = localStorage.getItem('lfr_client_lang') || navigator.language.slice(0, 2) || 'en';
if (!clientTranslations[currentLang]) currentLang = 'en';

function t(key) {
    if (clientTranslations[currentLang] && clientTranslations[currentLang][key]) {
        return clientTranslations[currentLang][key];
    }
    if (clientTranslations['en'] && clientTranslations['en'][key]) {
        return clientTranslations['en'][key];
    }
    return key;
}

function setLanguage(lang) {
    if (clientTranslations[lang]) {
        currentLang = lang;
        localStorage.setItem('lfr_client_lang', lang);
        
        // Update static elements
        document.querySelectorAll('[data-i18n]').forEach(el => {
            const key = el.getAttribute('data-i18n');
            if (el.tagName === 'INPUT' && (el.type === 'text' || el.type === 'password' || el.type === 'number')) {
                el.placeholder = t(key);
            } else {
                // If it has children (like the help-icon span), preserve them
                const helpIcon = el.querySelector('.help-icon');
                if (helpIcon) {
                    const textNode = Array.from(el.childNodes).find(n => n.nodeType === Node.TEXT_NODE);
                    if (textNode) textNode.textContent = t(key) + " ";
                    
                    // Update the title attribute of the help icon
                    const titleKey = el.getAttribute('data-i18n-help');
                    if (titleKey) helpIcon.setAttribute('title', t(titleKey));
                } else {
                    el.innerText = t(key);
                }
            }
        });

        // Re-render empty state or dynamic text
        const emptyState = document.querySelector('.empty-state');
        if (emptyState) emptyState.innerText = t('client_select_req');

        // Update language selector dropdown
        const selector = document.getElementById('lang-selector');
        if (selector && selector.value !== lang) {
            selector.value = lang;
        }
    }
}
