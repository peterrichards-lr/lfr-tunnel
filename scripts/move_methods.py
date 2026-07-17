import sys
import re
import os

def extract_methods(source_file, target_file, method_names):
    with open(source_file, 'r') as f:
        content = f.read()

    extracted = []
    
    for method in method_names:
        # Match optional doc comments followed by the func declaration
        # Regex explanation:
        # (?:\/\/[^\n]*\n)*  : Zero or more single-line comments
        # func \(s \*Server\) METHOD_NAME\(
        pattern = re.compile(
            r'((?://[^\n]*\n)*func\s+\(s\s+\*Server\)\s+' + re.escape(method) + r'\b.*?\{)',
            re.MULTILINE | re.DOTALL
        )
        
        match = pattern.search(content)
        if not match:
            print(f"Method not found: {method}")
            continue
            
        start_idx = match.start()
        
        # Now find the matching closing brace
        brace_count = 0
        in_string = False
        in_char = False
        in_line_comment = False
        in_block_comment = False
        escape = False
        
        end_idx = -1
        i = start_idx
        
        # Fast forward to the first opening brace
        while i < len(content) and content[i] != '{':
            i += 1
            
        if i >= len(content):
            print(f"Malformed method: {method}")
            continue
            
        for idx in range(i, len(content)):
            char = content[idx]
            
            if in_line_comment:
                if char == '\n':
                    in_line_comment = False
                continue
                
            if in_block_comment:
                if char == '/' and content[idx-1] == '*':
                    in_block_comment = False
                continue
                
            if escape:
                escape = False
                continue
                
            if char == '\\':
                escape = True
                continue
                
            if in_string:
                if char == '"':
                    in_string = False
                continue
                
            if in_char:
                if char == "'":
                    in_char = False
                continue
                
            if char == '"':
                in_string = True
            elif char == "'":
                in_char = True
            elif char == '/' and idx + 1 < len(content):
                if content[idx+1] == '/':
                    in_line_comment = True
                elif content[idx+1] == '*':
                    in_block_comment = True
            elif char == '{':
                brace_count += 1
            elif char == '}':
                brace_count -= 1
                if brace_count == 0:
                    end_idx = idx + 1
                    break
                    
        if end_idx != -1:
            method_text = content[start_idx:end_idx]
            extracted.append(method_text)
            # Remove from original content
            content = content[:start_idx] + content[end_idx:]
            # Clean up double newlines
            content = re.sub(r'\n{3,}', '\n\n', content)
            print(f"Successfully extracted: {method}")
        else:
            print(f"Could not find end of method: {method}")

    if extracted:
        with open(source_file, 'w') as f:
            f.write(content)
            
        # Create target file if it doesn't exist, else append
        if not os.path.exists(target_file):
            with open(target_file, 'w') as f:
                f.write("package server\n\n")
                f.write("import (\n\t\"context\"\n\t\"fmt\"\n\t\"net/http\"\n\t\"time\"\n)\n\n")
                
        with open(target_file, 'a') as f:
            for ext in extracted:
                f.write(ext + "\n\n")
                
if __name__ == "__main__":
    if len(sys.argv) < 3:
        print("Usage: move_methods.py <target_file> <method1> [method2 ...]")
        sys.exit(1)
        
    target = sys.argv[1]
    methods = sys.argv[2:]
    extract_methods('pkg/server/server.go', target, methods)
