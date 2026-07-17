import re

with open('pkg/gui/gui.go', 'r') as f:
    go = f.read()

go = go.replace('_, _ = w.Write(client.InspectorHTML)', '_, _ = w.Write(client.DashboardHTML)')
go = go.replace('_, _ = w.Write(client.LogsHTML)', '_, _ = w.Write(client.DashboardHTML)')

with open('pkg/gui/gui.go', 'w') as f:
    f.write(go)
print("gui.go updated.")
