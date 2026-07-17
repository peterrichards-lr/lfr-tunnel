#!/usr/bin/env python3
import json
import os

WHATS_NEW_PATH = 'pkg/server/static/whats-new.json'

def main():
    if not os.path.exists(WHATS_NEW_PATH):
        print(f"File {WHATS_NEW_PATH} not found.")
        return

    with open(WHATS_NEW_PATH, 'r') as f:
        data = json.load(f)

    if isinstance(data, list):
        if len(data) > 3:
            data = data[:3]
            with open(WHATS_NEW_PATH, 'w') as f:
                json.dump(data, f, indent=2)
            print(f"Trimmed whats-new.json to latest 3 releases.")
        else:
            print(f"whats-new.json contains {len(data)} releases (<= 3). No trimming needed.")
    else:
        print("whats-new.json is not an array format.")

if __name__ == '__main__':
    main()
