import urllib.request, json, re, os, time

req = urllib.request.Request("http://localhost:8000/api/auth/magic-link", data=b'{"email": "admin@lfr-demo.local"}', headers={'Content-Type': 'application/json'}, method='POST')
try:
    urllib.request.urlopen(req)
except Exception as e:
    print("Magic link error:", e, file=sys.stderr)

time.sleep(2)

port = os.environ.get("E2E_MAILPIT_PORT", "8025")
magic_token = None
try:
    data = json.loads(urllib.request.urlopen(f"http://localhost:{port}/api/v1/messages").read())
    for m in data["messages"]:
        msg = json.loads(urllib.request.urlopen(f"http://localhost:{port}/api/v1/message/" + m["ID"]).read())
        body = (msg.get("HTML") or "") + "\n" + (msg.get("Text") or "")
        match = re.search(r"token=([a-f0-9A-Z]+)", body, re.IGNORECASE)
        if match:
            magic_token = match.group(1)
            break
except Exception as e:
    pass

if not magic_token:
    exit(1)

req = urllib.request.Request("http://localhost:8000/api/auth/verify", data=json.dumps({"token": magic_token}).encode(), headers={'Content-Type': 'application/json', 'Host': 'tunnel.lfr-demo.local'}, method='POST')
try:
    resp = urllib.request.urlopen(req)
    cookie = resp.headers.get('Set-Cookie')
    session_token = re.search(r"lfr_session=([^;]+)", cookie).group(1)
except Exception as e:
    exit(1)

req = urllib.request.Request("http://localhost:8000/api/tokens", data=b'{"name": "UI Test"}', headers={'Content-Type': 'application/json', 'Cookie': 'lfr_session=' + session_token, 'Host': 'tunnel.lfr-demo.local'}, method='POST')
try:
    resp_raw = urllib.request.urlopen(req).read()
    pat = json.loads(resp_raw).get("raw_token")
    print(pat)
except Exception as e:
    exit(1)
