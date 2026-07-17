#!/bin/bash
while true; do
  STATUS=$(gh pr view 592 --json state -q .state)
  if [ "$STATUS" == "MERGED" ]; then
    echo "PR merged!"
    break
  fi
  echo "Waiting for PR to merge..."
  sleep 15
done
git checkout master
git pull
./scripts/create-release-tag.sh v1.39.0
