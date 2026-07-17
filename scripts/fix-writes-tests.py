import os
import re

def fix_file(filepath):
    with open(filepath, 'r') as f:
        content = f.read()

    # Find `\t_, _ = w.Write(` or `\t_, _ = fmt.Fprintln(` or `_, _ = writer.WriteString(`
    # Let's support `writer.WriteString` as well since we saw it in `mail_test.go`.
    
    out_content = ""
    idx = 0
    while True:
        match = re.search(r'([ \t]+)_, _ = ((?:w\.Write|fmt\.Fprintln|writer\.WriteString)\()', content[idx:])
        if not match:
            out_content += content[idx:]
            break
            
        start_pos = idx + match.start()
        indent = match.group(1)
        func_call_start = match.group(2)
        
        out_content += content[idx:start_pos]
        
        p_count = 1
        curr_pos = start_pos + len(indent) + len("_, _ = ") + len(func_call_start)
        
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
            
        func_call_full = content[start_pos + len(indent) + len("_, _ = ") : curr_pos]
        
        replacement = f"{indent}if _, err := {func_call_full}; err != nil {{\n{indent}\tlog.Printf(\"[Warning] Failed to write response: %v\", err)\n{indent}}}"
        
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
        if file.endswith('_test.go'):
            fix_file(os.path.join(root, file))

