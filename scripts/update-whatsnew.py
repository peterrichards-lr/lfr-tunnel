import json

with open('pkg/server/static/whats-new.json', 'r') as f:
    data = json.load(f)

data['version'] = 'v1.40.0'
data['release_date'] = '2026-07-17'
data['features'].insert(0, "Feature: Client Dashboard UX Unification & Responsive Design")
data['features'].insert(0, "Feature: LDM Integration API Bridge for seamless project synchronization")

with open('pkg/server/static/whats-new.json', 'w') as f:
    json.dump(data, f, indent=2)

print("Updated whats-new.json")
