import os
import re

locales = ['ar', 'de', 'es', 'fr', 'ja', 'ko', 'pt', 'ro', 'zh']

translations = {
    "maint_tab_title": {"en": "Gateway Maintenance", "ar": "صيانة البوابة", "de": "Gateway-Wartung", "es": "Mantenimiento de Puerta de Enlace", "fr": "Maintenance de la Passerelle", "ja": "ゲートウェイのメンテナンス", "ko": "게이트웨이 유지보수", "pt": "Manutenção do Gateway", "ro": "Mentenanță Gateway", "zh": "网关维护"},
    "maint_soft_title": {"en": "🛠️ Gateway Soft Maintenance Mode", "ar": "🛠️ وضع الصيانة المرن للبوابة", "de": "🛠️ Gateway Soft-Wartungsmodus", "es": "🛠️ Modo de Mantenimiento Suave", "fr": "🛠️ Mode Maintenance Douce", "ja": "🛠️ ゲートウェイソフトメンテナンスモード", "ko": "🛠️ 게이트웨이 소프트 점검 모드", "pt": "🛠️ Modo de Manutenção Suave", "ro": "🛠️ Mod Mentenanță Ușoară", "zh": "🛠️ 网关软维护模式"},
    "maint_soft_desc": {"en": "Activating Soft Maintenance Mode blocks standard user logins, rejects new tunnel connections, and kicks active standard tunnels. Visitors trying to access developer subdomains will receive a scheduled maintenance page. <strong>Administrators and Owners will remain fully unblocked so they can manage the portal and disable this mode from the dashboard.</strong>", "ar": "تفعيل وضع الصيانة المرن يمنع تسجيلات الدخول العادية، ويرفض الاتصالات الجديدة. <strong>سيظل المسؤولون غير محظورين تمامًا لإدارة البوابة.</strong>", "de": "Die Aktivierung blockiert Standardanmeldungen. <strong>Administratoren bleiben vollständig entsperrt.</strong>", "es": "Activar esto bloquea inicios de sesión estándar. <strong>Los administradores permanecerán desbloqueados.</strong>", "fr": "L'activation bloque les connexions standards. <strong>Les administrateurs restent débloqués.</strong>", "ja": "これを有効にすると標準ログインがブロックされます。<strong>管理者は引き続きアクセス可能です。</strong>", "ko": "소프트 점검을 활성화하면 일반 로그인이 차단됩니다. <strong>관리자는 차단되지 않습니다.</strong>", "pt": "A ativação bloqueia logins padrão. <strong>Administradores permanecem desbloqueados.</strong>", "ro": "Activarea blochează conectările standard. <strong>Administratorii rămân deblocați.</strong>", "zh": "激活软维护将阻止标准登录。<strong>管理员将保持完全畅通。</strong>"},
    "maint_lbl_action": {"en": "Action Name", "ar": "اسم الإجراء", "de": "Aktionsname", "es": "Nombre de la Acción", "fr": "Nom de l'Action", "ja": "アクション名", "ko": "작업 이름", "pt": "Nome da Ação", "ro": "Nume Acțiune", "zh": "操作名称"},
    "maint_lbl_duration": {"en": "Duration (Minutes)", "ar": "المدة (بالدقائق)", "de": "Dauer (Minuten)", "es": "Duración (Minutos)", "fr": "Durée (Minutes)", "ja": "所要時間（分）", "ko": "소요 시간 (분)", "pt": "Duração (Minutos)", "ro": "Durată (Minute)", "zh": "持续时间（分钟）"},
    "maint_lbl_reason": {"en": "Reason / Details", "ar": "السبب / التفاصيل", "de": "Grund / Details", "es": "Razón / Detalles", "fr": "Raison / Détails", "ja": "理由 / 詳細", "ko": "이유 / 세부 정보", "pt": "Motivo / Detalhes", "ro": "Motiv / Detalii", "zh": "原因 / 详情"},
    "maint_opt_0": {"en": "Immediate Activation ⚡", "ar": "تفعيل فوري ⚡", "de": "Sofortige Aktivierung ⚡", "es": "Activación Inmediata ⚡", "fr": "Activation Immédiate ⚡", "ja": "即時有効化 ⚡", "ko": "즉시 활성화 ⚡", "pt": "Ativação Imediata ⚡", "ro": "Activare Imediată ⚡", "zh": "立即激活 ⚡"},
    "maint_opt_1": {"en": "Schedule in 1 Minute ⏳", "ar": "جدولة في دقيقة واحدة ⏳", "de": "In 1 Minute planen ⏳", "es": "Programar en 1 Minuto ⏳", "fr": "Planifier dans 1 Minute ⏳", "ja": "1分後にスケジュール ⏳", "ko": "1분 후 예약 ⏳", "pt": "Agendar em 1 Minuto ⏳", "ro": "Programează în 1 Minut ⏳", "zh": "计划在1分钟后 ⏳"},
    "maint_opt_5": {"en": "Schedule in 5 Minutes ⏳", "ar": "جدولة في 5 دقائق ⏳", "de": "In 5 Minuten planen ⏳", "es": "Programar en 5 Minutos ⏳", "fr": "Planifier dans 5 Minutes ⏳", "ja": "5分後にスケジュール ⏳", "ko": "5분 후 예약 ⏳", "pt": "Agendar em 5 Minutos ⏳", "ro": "Programează în 5 Minute ⏳", "zh": "计划在5分钟后 ⏳"},
    "maint_opt_10": {"en": "Schedule in 10 Minutes ⏳", "ar": "جدولة في 10 دقائق ⏳", "de": "In 10 Minuten planen ⏳", "es": "Programar en 10 Minutos ⏳", "fr": "Planifier dans 10 Minutes ⏳", "ja": "10分後にスケジュール ⏳", "ko": "10분 후 예약 ⏳", "pt": "Agendar em 10 Minutos ⏳", "ro": "Programează în 10 Minute ⏳", "zh": "计划在10分钟后 ⏳"},
    "maint_opt_30": {"en": "Schedule in 30 Minutes ⏳", "ar": "جدولة في 30 دقيقة ⏳", "de": "In 30 Minuten planen ⏳", "es": "Programar en 30 Minutos ⏳", "fr": "Planifier dans 30 Minutes ⏳", "ja": "30分後にスケジュール ⏳", "ko": "30분 후 예약 ⏳", "pt": "Agendar em 30 Minutos ⏳", "ro": "Programează în 30 Minute ⏳", "zh": "计划在30分钟后 ⏳"},
    "maint_btn_enable_soft": {"en": "Enable Soft Maintenance", "ar": "تفعيل الصيانة المرنة", "de": "Soft-Wartung aktivieren", "es": "Habilitar Mantenimiento Suave", "fr": "Activer la Maintenance Douce", "ja": "ソフトメンテナンスを有効化", "ko": "소프트 점검 활성화", "pt": "Ativar Manutenção Suave", "ro": "Activează Mentenanță Ușoară", "zh": "启用软维护"},
    "maint_btn_disable_soft": {"en": "Disable Soft Maintenance", "ar": "تعطيل الصيانة المرنة", "de": "Soft-Wartung deaktivieren", "es": "Deshabilitar Mantenimiento Suave", "fr": "Désactiver la Maintenance Douce", "ja": "ソフトメンテナンスを無効化", "ko": "소프트 점검 비활성화", "pt": "Desativar Manutenção Suave", "ro": "Dezactivează Mentenanță Ușoară", "zh": "禁用软维护"},
    "maint_iron_title": {"en": "🔒 Nginx Iron Curtain Mode (Hard Maintenance - Owner Only)", "ar": "🔒 وضع الستار الحديدي لـ Nginx", "de": "🔒 Nginx Iron Curtain Modus", "es": "🔒 Modo Cortina de Hierro Nginx", "fr": "🔒 Mode Rideau de Fer Nginx", "ja": "🔒 Nginx 鉄のカーテンモード", "ko": "🔒 Nginx 철의 장막 모드", "pt": "🔒 Modo Cortina de Ferro Nginx", "ro": "🔒 Modul Cortina de Fier Nginx", "zh": "🔒 Nginx 铁幕模式"},
    "maint_iron_desc": {"en": "Activating Nginx Iron Curtain Mode blocks all external incoming requests. <strong style=\"color: #ef4444;\">Warning: You will be disconnected. It must be disabled via SSH.</strong>", "ar": "تفعيل هذا يمنع كل الطلبات الخارجية. <strong style=\"color: #ef4444;\">تحذير: سيتم قطع اتصالك. يجب تعطيله عبر SSH.</strong>", "de": "Blockiert alle externen Anfragen. <strong style=\"color: #ef4444;\">Warnung: Sie werden getrennt.</strong>", "es": "Bloquea todas las solicitudes externas. <strong style=\"color: #ef4444;\">Advertencia: Será desconectado.</strong>", "fr": "Bloque toutes les requêtes externes. <strong style=\"color: #ef4444;\">Avertissement : Vous serez déconnecté.</strong>", "ja": "外部からの要求をすべてブロックします。<strong style=\"color: #ef4444;\">警告: 切断されます。</strong>", "ko": "모든 외부 요청을 차단합니다. <strong style=\"color: #ef4444;\">경고: 연결이 끊어집니다.</strong>", "pt": "Bloqueia todas as solicitações externas. <strong style=\"color: #ef4444;\">Aviso: Você será desconectado.</strong>", "ro": "Blochează toate cererile externe. <strong style=\"color: #ef4444;\">Avertisment: Veți fi deconectat.</strong>", "zh": "阻止所有外部请求。<strong style=\"color: #ef4444;\">警告：您将断开连接。</strong>"},
    "maint_btn_enable_hard": {"en": "Enable Iron Curtain", "ar": "تفعيل الستار الحديدي", "de": "Iron Curtain aktivieren", "es": "Habilitar Cortina de Hierro", "fr": "Activer le Rideau de Fer", "ja": "鉄のカーテンを有効化", "ko": "철의 장막 활성화", "pt": "Ativar Cortina de Ferro", "ro": "Activează Cortina de Fier", "zh": "启用铁幕"},
    "maint_btn_disable_hard": {"en": "Disable Iron Curtain", "ar": "تعطيل الستار الحديدي", "de": "Iron Curtain deaktivieren", "es": "Deshabilitar Cortina de Hierro", "fr": "Désactiver le Rideau de Fer", "ja": "鉄のカーテンを無効化", "ko": "철의 장막 비활성화", "pt": "Desativar Cortina de Ferro", "ro": "Dezactivează Cortina de Fier", "zh": "禁用铁幕"},
    "maint_status_inactive": {"en": "Status: <span style=\"color: var(--text-muted);\">INACTIVE (All welcome) 🟢</span>", "ar": "الحالة: <span style=\"color: var(--text-muted);\">غير نشط 🟢</span>", "de": "Status: <span style=\"color: var(--text-muted);\">INAKTIV 🟢</span>", "es": "Estado: <span style=\"color: var(--text-muted);\">INACTIVO 🟢</span>", "fr": "Statut: <span style=\"color: var(--text-muted);\">INACTIF 🟢</span>", "ja": "ステータス: <span style=\"color: var(--text-muted);\">非アクティブ 🟢</span>", "ko": "상태: <span style=\"color: var(--text-muted);\">비활성 🟢</span>", "pt": "Status: <span style=\"color: var(--text-muted);\">INATIVO 🟢</span>", "ro": "Stare: <span style=\"color: var(--text-muted);\">INACTIV 🟢</span>", "zh": "状态: <span style=\"color: var(--text-muted);\">未激活 🟢</span>"},
    "maint_iron_inactive": {"en": "Status: Inactive 🟢", "ar": "الحالة: غير نشط 🟢", "de": "Status: Inaktiv 🟢", "es": "Estado: Inactivo 🟢", "fr": "Statut: Inactif 🟢", "ja": "ステータス: 非アクティブ 🟢", "ko": "상태: 비활성 🟢", "pt": "Status: Inativo 🟢", "ro": "Stare: Inactiv 🟢", "zh": "状态: 未激活 🟢"},
    "maint_status_scheduled": {"en": "Status: <span style=\"color: var(--warning-amber);\">SCHEDULED 🟡</span>", "ar": "الحالة: <span style=\"color: var(--warning-amber);\">مجدول 🟡</span>", "de": "Status: <span style=\"color: var(--warning-amber);\">GEPLANT 🟡</span>", "es": "Estado: <span style=\"color: var(--warning-amber);\">PROGRAMADO 🟡</span>", "fr": "Statut: <span style=\"color: var(--warning-amber);\">PLANIFIÉ 🟡</span>", "ja": "ステータス: <span style=\"color: var(--warning-amber);\">予定あり 🟡</span>", "ko": "상태: <span style=\"color: var(--warning-amber);\">예약됨 🟡</span>", "pt": "Status: <span style=\"color: var(--warning-amber);\">AGENDADO 🟡</span>", "ro": "Stare: <span style=\"color: var(--warning-amber);\">PROGRAMAT 🟡</span>", "zh": "状态: <span style=\"color: var(--warning-amber);\">已计划 🟡</span>"},
    "maint_status_active": {"en": "Status: <span style=\"color: var(--danger);\">ACTIVE 🔴</span>", "ar": "الحالة: <span style=\"color: var(--danger);\">نشط 🔴</span>", "de": "Status: <span style=\"color: var(--danger);\">AKTIV 🔴</span>", "es": "Estado: <span style=\"color: var(--danger);\">ACTIVO 🔴</span>", "fr": "Statut: <span style=\"color: var(--danger);\">ACTIF 🔴</span>", "ja": "ステータス: <span style=\"color: var(--danger);\">アクティブ 🔴</span>", "ko": "상태: <span style=\"color: var(--danger);\">활성 🔴</span>", "pt": "Status: <span style=\"color: var(--danger);\">ATIVO 🔴</span>", "ro": "Stare: <span style=\"color: var(--danger);\">ACTIV 🔴</span>", "zh": "状态: <span style=\"color: var(--danger);\">活动 🔴</span>"},
    "maint_iron_active": {"en": "Status: Active 🔴", "ar": "الحالة: نشط 🔴", "de": "Status: Aktiv 🔴", "es": "Estado: Activo 🔴", "fr": "Statut: Actif 🔴", "ja": "ステータス: アクティブ 🔴", "ko": "상태: 활성 🔴", "pt": "Status: Ativo 🔴", "ro": "Stare: Activ 🔴", "zh": "状态: 活动 🔴"}
}

def add_properties(locale):
    filename = 'pkg/server/i18n/Language.properties' if locale == 'en' else f'pkg/server/i18n/Language_{locale}.properties'
    if not os.path.exists(filename): return
    with open(filename, 'a', encoding='utf-8') as f:
        f.write('\n# Gateway Maintenance Tab\n')
        for key, vals in translations.items():
            f.write(f'{key}={vals[locale]}\n')

add_properties('en')
for l in locales:
    add_properties(l)

# Update HTML dashboard
html_path = 'pkg/server/dashboard.html'
with open(html_path, 'r', encoding='utf-8') as f:
    html = f.read()

# Make the manual replacements
html = html.replace('<h2>Gateway Maintenance</h2>', '<h2 data-i18n="maint_tab_title">Gateway Maintenance</h2>')
html = html.replace('🛠️ Gateway Soft Maintenance Mode</h4>', '🛠️ Gateway Soft Maintenance Mode</h4>').replace('<h4 style="margin-bottom: 4px; font-size: 15px; font-weight: 600; color: #f59e0b;">🛠️ Gateway Soft Maintenance Mode</h4>', '<h4 data-i18n="maint_soft_title" style="margin-bottom: 4px; font-size: 15px; font-weight: 600; color: #f59e0b;">🛠️ Gateway Soft Maintenance Mode</h4>')

html = html.replace('Activating Soft Maintenance Mode blocks standard user logins, rejects new tunnel connections, and kicks active standard tunnels. Visitors trying to access developer subdomains will receive a scheduled maintenance page. \n                        <strong>Administrators and Owners will remain fully unblocked so they can manage the portal and disable this mode from the dashboard.</strong>', '')

html = re.sub(r'<p style="color: var\(--text-muted\); font-size: 12px; margin-bottom: 16px; line-height: 1.4;">.*?</p>', '<p data-i18n="maint_soft_desc" style="color: var(--text-muted); font-size: 12px; margin-bottom: 16px; line-height: 1.4;">Activating Soft Maintenance Mode blocks standard user logins, rejects new tunnel connections, and kicks active standard tunnels. Visitors trying to access developer subdomains will receive a scheduled maintenance page. <strong>Administrators and Owners will remain fully unblocked so they can manage the portal and disable this mode from the dashboard.</strong></p>', html, count=1, flags=re.DOTALL)

html = html.replace('>Action Name</label>', ' data-i18n="maint_lbl_action">Action Name</label>')
html = html.replace('>Duration (Minutes)</label>', ' data-i18n="maint_lbl_duration">Duration (Minutes)</label>')
html = html.replace('>Reason / Details</label>', ' data-i18n="maint_lbl_reason">Reason / Details</label>')

html = html.replace('<option value="0">Immediate Activation ⚡</option>', '<option value="0" data-i18n="maint_opt_0">Immediate Activation ⚡</option>')
html = html.replace('<option value="1">Schedule in 1 Minute ⏳</option>', '<option value="1" data-i18n="maint_opt_1">Schedule in 1 Minute ⏳</option>')
html = html.replace('<option value="5" selected>Schedule in 5 Minutes ⏳</option>', '<option value="5" selected data-i18n="maint_opt_5">Schedule in 5 Minutes ⏳</option>')
html = html.replace('<option value="10">Schedule in 10 Minutes ⏳</option>', '<option value="10" data-i18n="maint_opt_10">Schedule in 10 Minutes ⏳</option>')
html = html.replace('<option value="30">Schedule in 30 Minutes ⏳</option>', '<option value="30" data-i18n="maint_opt_30">Schedule in 30 Minutes ⏳</option>')

html = html.replace('onclick="toggleSoftMaintenanceMode()">Enable Soft Maintenance</button>', 'onclick="toggleSoftMaintenanceMode()" data-i18n="maint_btn_enable_soft">Enable Soft Maintenance</button>')

html = html.replace('<h4 style="margin-bottom: 4px; font-size: 15px; font-weight: 600; color: var(--danger);">🔒 Nginx Iron Curtain Mode (Hard Maintenance - Owner Only)</h4>', '<h4 data-i18n="maint_iron_title" style="margin-bottom: 4px; font-size: 15px; font-weight: 600; color: var(--danger);">🔒 Nginx Iron Curtain Mode (Hard Maintenance - Owner Only)</h4>')

html = re.sub(r'<p style="color: var\(--text-muted\); font-size: 12px; margin-bottom: 16px; line-height: 1.4;">\s*Activating Nginx Iron Curtain Mode creates the trigger file on the server. Nginx will immediately block \*\*all\*\* external incoming requests—including developer tunnels and the Admin Dashboard itself. \s*<strong style="color: #ef4444;">Warning: You will be disconnected and unable to log in to disable this via the portal. It must be disabled via SSH on the VPS using the restore scripts.</strong>\s*</p>', '<p data-i18n="maint_iron_desc" style="color: var(--text-muted); font-size: 12px; margin-bottom: 16px; line-height: 1.4;">Activating Nginx Iron Curtain Mode blocks all external incoming requests. <strong style="color: #ef4444;">Warning: You will be disconnected. It must be disabled via SSH.</strong></p>', html, count=1, flags=re.DOTALL)


html = html.replace('onclick="toggleHardMaintenanceMode()">Enable Iron Curtain</button>', 'onclick="toggleHardMaintenanceMode()" data-i18n="maint_btn_enable_hard">Enable Iron Curtain</button>')

html = html.replace('<span id="maint-status-text" style="font-weight: bold; font-size: 13px; color: var(--text-muted); margin-right: 12px;">Status: Loading...</span>', '<span id="maint-status-text" data-i18n="maint_status_loading" style="font-weight: bold; font-size: 13px; color: var(--text-muted); margin-right: 12px;">Status: Loading...</span>')

with open(html_path, 'w', encoding='utf-8') as f:
    f.write(html)
print("Updated properties and HTML.")
