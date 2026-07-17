import os

for root, _, files in os.walk('.'):
    if 'node_modules' in root or '.git' in root:
        continue
    for file in files:
        if not file.endswith('.go'):
            continue
        path = os.path.join(root, file)
        with open(path, 'r') as f:
            content = f.read()
            
        lines = content.split('\n')
        new_lines = []
        changed = False
        for i, line in enumerate(lines):
            if '//nolint:errcheck' in line and ', _ :=' in line:
                if line.rstrip().endswith('{') or line.rstrip().endswith('{ //nolint:errcheck'):
                    new_lines.append(line)
                    continue
                    
                changed = True
                indent = len(line) - len(line.lstrip())
                prefix = line[:indent]
                
                new_line = line.replace(', _ :=', ', _err :=').replace('//nolint:errcheck', '').rstrip()
                
                new_lines.append(new_line)
                new_lines.append(prefix + '_ = _err //nolint:errcheck')
            else:
                new_lines.append(line)
                
        if changed:
            with open(path, 'w') as f:
                f.write('\n'.join(new_lines))
            print(f"Fixed {path}")
