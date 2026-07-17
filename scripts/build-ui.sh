#!/bin/bash
cd ui
if command -v pnpm &> /dev/null; then
  pnpm install && pnpm run lint && pnpm run build
elif command -v npm &> /dev/null; then
  npm install && npm run lint && npm run build
else
  echo "Error: Neither pnpm nor npm is installed."
  exit 1
fi
