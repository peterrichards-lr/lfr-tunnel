import urllib.request, json, re, os, time

print("Requesting Magic Link")
req = urllib.request.Request("http://localhost:8000/api/auth/magic-link", data=b'{"email": "admin@lfr-demo.local"}', headers={'Content-Type': 'application/json'}, method='POST')
try:
    urllib.request.urlopen(req)
except Exception as e:
    print("Magic link error:", e)

time.sleep(2)

print("Fetching Magic Token")
port = "8025"
magic_token = None
try:
    data = json.loads(urllib.request.urlopen(f"http://localhost:{port}/api/v1/messages").read())
    for m in data["messages"]:
        msg = json.loads(urllib.request.urlopen(f"http://localhost:{port}/api/v1/message/" + m["ID"]).read())
        body = (msg.get("HTML") or "") + "\n" + (msg.get("Text") or "")
        match = re.search(r"token=([a-f0-9A-Z]+)", body, re.IGNORECASE)
        if match:
            magic_token = match.group(1)
            print("Found magic token:", magic_token)
            break
except Exception as e:
    print("Error reading mailpit:", e)

if not magic_token:
    print("Could not find magic token")
    exit(1)

print("Logging in with Magic Token")
req = urllib.request.Request("http://localhost:8000/api/auth/verify", data=json.dumps({"token": magic_token}).encode(), headers={'Content-Type': 'application/json', 'Host': 'tunnel.lfr-demo.local'}, method='POST')
try:
    resp = urllib.request.urlopen(req)
    cookie = resp.headers.get('Set-Cookie')
    print("Got cookie:", cookie)
    session_token = re.search(r"lfr_session=([^;]+)", cookie).group(1)
except Exception as e:
    print("Login error:", e)
    exit(1)

print("Creating PAT")
req = urllib.request.Request("http://localhost:8000/api/tokens", data=b'{"name": "UI Test"}', headers={'Content-Type': 'application/json', 'Cookie': 'lfr_session=' + session_token, 'Host': 'tunnel.lfr-demo.local'}, method='POST')
try:
    resp_raw = urllib.request.urlopen(req).read()
    print("Raw response:", resp_raw)
    pat = json.loads(resp_raw).get("token")
    print("Got PAT:", pat)
except Exception as e:
    if hasattr(e, 'read'):
        print("PAT error body:", e.read().decode())
    else:
        print("PAT error:", e)
    exit(1)

