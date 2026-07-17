import re

with open('pkg/client/interceptor.go', 'r') as f:
    content = f.read()

# Fix w.Write(content) -> _, _ = w.Write(content)
content = re.sub(r'(^\s*)w\.Write\((.*?)\)', r'\1_, _ = w.Write(\2)', content, flags=re.MULTILINE)

with open('pkg/client/interceptor.go', 'w') as f:
    f.write(content)

print("Fixed errcheck.")
