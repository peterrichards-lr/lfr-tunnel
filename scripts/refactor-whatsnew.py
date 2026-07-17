import re

with open('pkg/server/dashboard.html', 'r') as f:
    html = f.read()

# Replace <ul id="whats-new-list"...> with <div id="whats-new-list"...>
html = re.sub(r'<ul id="whats-new-list"([^>]*)>', r'<div id="whats-new-list"\1>', html)
html = html.replace('</ul>\n                        </div>', '</div>\n                        </div>')
html = html.replace('<li>Loading updates...</li>', '<div>Loading updates...</div>')
# Make sure the title just says "What's New" and doesn't get overwritten dynamically.

with open('pkg/server/dashboard.html', 'w') as f:
    f.write(html)

print("Updated dashboard.html")
