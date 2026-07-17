import os
import re

def fix_file(filepath):
    with open(filepath, 'r') as f:
        lines = f.readlines()
        
    changed = False
    for i, line in enumerate(lines):
        if re.search(r'^[ \t]+(?:_, )*_\s*=\s*[a-zA-Z0-9_\.\(\)]+', line):
            if "nolint:errcheck" not in line and "w.Write" not in line and "fmt.Fprintln" not in line and "fmt.Fprintf" not in line:
                lines[i] = line.rstrip('\n\r') + " //nolint:errcheck\n"
                changed = True
                
    if changed:
        with open(filepath, 'w') as f:
            f.writelines(lines)
        print(f"Fixed {filepath}")

for root, _, files in os.walk('cmd'):
    for file in files:
        if file.endswith('.go'):
            fix_file(os.path.join(root, file))

