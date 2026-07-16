import re
with open('pkg/client/inspector.html', 'r', encoding='utf-8') as f:
    html = f.read()

# Replace strings in renderDetails
html = html.replace("Replay Request", "${t('client_replay_req')}")
html = html.replace("<div class=\"panel-title\">Request</div>", "<div class=\"panel-title\">${t('client_req')}</div>")
html = html.replace("<div class=\"panel-title\">Response</div>", "<div class=\"panel-title\">${t('client_resp')}</div>")
html = html.replace("Replaying...", "${t('client_replaying')}")

# Update dictionary block
dict_updates = {
    '"client_inspector": "Inspector",': '"client_inspector": "Inspector",\n        "client_replay_req": "Replay Request",\n        "client_req": "Request",\n        "client_resp": "Response",\n        "client_replaying": "Replaying...",',
    '"client_inspector": "Inspector",': '"client_inspector": "Inspector",\n        "client_replay_req": "Replay Request",\n        "client_req": "Request",\n        "client_resp": "Response",\n        "client_replaying": "Replaying...",',
}

# Add English ones
html = html.replace('"client_inspector": "Inspector",', '"client_inspector": "Inspector",\n        "client_replay_req": "Replay Request",\n        "client_req": "Request",\n        "client_resp": "Response",\n        "client_replaying": "Replaying...",', 1)

with open('pkg/client/inspector.html', 'w', encoding='utf-8') as f:
    f.write(html)
