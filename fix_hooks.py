import os, glob

files = [
    "ui/src/pages/AdminMagicLinks.tsx",
    "ui/src/pages/AdminSubdomains.tsx",
    "ui/src/components/ReservationsPanel.tsx",
    "ui/src/pages/AdminExtensions.tsx",
    "ui/src/pages/AdminUsers.tsx",
]

for file in files:
    with open(file, 'r') as f:
        lines = f.readlines()
    
    # Find `if (loading) return ...`
    loading_idx = -1
    for i, line in enumerate(lines):
        if line.strip().startswith("if (loading) return"):
            loading_idx = i
            break
            
    if loading_idx == -1:
        continue
        
    hook_idx = -1
    for i in range(loading_idx + 1, len(lines)):
        if "useTableSort" in lines[i]:
            hook_idx = i
            break
            
    if hook_idx != -1:
        loading_line = lines[loading_idx]
        hook_line = lines[hook_idx]
        # Swap them: we remove hook_line and insert it before loading_idx
        lines.pop(hook_idx)
        lines.insert(loading_idx, hook_line)
        
        with open(file, 'w') as f:
            f.writelines(lines)

