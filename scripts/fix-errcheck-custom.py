import os
import re

def fix_file(filepath):
    with open(filepath, 'r') as f:
        content = f.read()

    # Look for `\t_ = ` or `\t_, _ = ` or `\t_, _, _ = `
    out_content = ""
    idx = 0
    
    # regex to find the start of a suppressed assignment
    pattern = re.compile(r'([ \t]+)(?:_ = |_, _ = |_, _, _ = )([a-zA-Z0-9_\.\(\)]+\()')
    
    while True:
        match = pattern.search(content, idx)
        if not match:
            out_content += content[idx:]
            break
            
        start_pos = match.start()
        indent = match.group(1)
        func_call_start = match.group(2)
        
        # Don't replace if it's already in our exclude list
        # We'll just replace everything we find that isn't ignored.
        
        out_content += content[idx:start_pos]
        
        p_count = 1
        curr_pos = start_pos + len(indent) + len(match.group(0)) - len(func_call_start) + len(func_call_start)
        
        in_string = False
        in_raw_string = False
        escape = False
        
        while curr_pos < len(content) and p_count > 0:
            c = content[curr_pos]
            if in_string:
                if escape:
                    escape = False
                elif c == '\\':
                    escape = True
                elif c == '"':
                    in_string = False
            elif in_raw_string:
                if c == '`':
                    in_raw_string = False
            else:
                if c == '"':
                    in_string = True
                elif c == '`':
                    in_raw_string = True
                elif c == '(':
                    p_count += 1
                elif c == ')':
                    p_count -= 1
            curr_pos += 1
            
        func_call_full = content[start_pos + len(match.group(0)) - len(func_call_start) : curr_pos]
        
        # We need to determine the assignment based on what was suppressed
        suppression = match.group(0)[len(indent):-len(func_call_start)]
        if suppression == "_ = ":
            assignment = f"if err := {func_call_full}; err != nil"
        elif suppression == "_, _ = ":
            assignment = f"if _, err := {func_call_full}; err != nil"
        else:
            assignment = f"if _, _, err := {func_call_full}; err != nil"
            
        replacement = f"{indent}{assignment} {{\n{indent}\tlog.Printf(\"[Warning] Suppressed error: %v\", err)\n{indent}}}"
        
        out_content += replacement
        idx = curr_pos

    if out_content != content:
        if '"log"' not in out_content and '"log"\n' not in out_content:
            out_content = re.sub(r'import \(', 'import (\n\t"log"', out_content, count=1)
        
        with open(filepath, 'w') as f:
            f.write(out_content)
        print(f"Fixed {filepath}")

for root, _, files in os.walk('pkg'):
    for file in files:
        if file.endswith('.go'):
            fix_file(os.path.join(root, file))

