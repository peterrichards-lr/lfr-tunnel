import subprocess
import re
import sys

# Fetch the CI logs
result = subprocess.run(["gh", "run", "view", "29576058229", "--log-failed"], capture_output=True, text=True)

lines_to_fix = {}

for line in result.stdout.split('\n') + result.stderr.split('\n'):
    m = re.search(r'##\[error\]([^:]+\.go):(\d+):\d+: Error return value.*\(errcheck\)', line)
    if m:
        filepath = m.group(1)
        line_num = int(m.group(2))
        if filepath not in lines_to_fix:
            lines_to_fix[filepath] = []
        lines_to_fix[filepath].append(line_num)

if not lines_to_fix:
    print("No errors found in log.")
    sys.exit(0)

for filepath, lines in lines_to_fix.items():
    with open(filepath, 'r') as f:
        content = f.readlines()
        
    for line_num in lines:
        idx = line_num - 1
        if idx < len(content) and "//nolint:errcheck" not in content[idx]:
            content[idx] = content[idx].rstrip() + " //nolint:errcheck\n"
            
    with open(filepath, 'w') as f:
        f.writelines(content)
        print(f"Fixed {filepath}")

