import os

files = [
    "ui/src/pages/AccountSettings.tsx",
    "ui/src/components/Layout.tsx"
]

for file in files:
    with open(file, 'r') as f:
        content = f.read()
    
    content = content.replace('catch (err: any)', 'catch')
    content = content.replace('catch (err)', 'catch')
    
    with open(file, 'w') as f:
        f.write(content)

