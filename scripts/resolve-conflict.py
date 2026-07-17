import re

with open('pkg/server/dashboard.html', 'r') as f:
    html = f.read()

# Replace the conflict marker block with the origin/master version
html = re.sub(r'<<<<<<< HEAD\n\s*=======\n(.*?)\n>>>>>>> origin/master', r'\1', html, flags=re.DOTALL)

with open('pkg/server/dashboard.html', 'w') as f:
    f.write(html)
print("Conflict resolved.")
