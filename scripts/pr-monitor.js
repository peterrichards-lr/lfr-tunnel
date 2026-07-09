#!/usr/bin/env node

const fs = require('fs');
const path = require('path');
const { execSync } = require('child_process');

const STATE_FILE = path.join(__dirname, 'pr-monitor-state.json');
const POLL_INTERVAL_MS = 30000; // 30 seconds

function log(msg, type = 'info') {
  const timestamp = new Date().toISOString();
  const colors = {
    info: '\x1b[34m',    // Blue
    success: '\x1b[32m', // Green
    warn: '\x1b[33m',    // Yellow
    error: '\x1b[31m',   // Red
    reset: '\x1b[0m'
  };
  console.log(`[${timestamp}] ${colors[type] || ''}${msg}${colors.reset}`);
}

function loadState() {
  if (fs.existsSync(STATE_FILE)) {
    try {
      return JSON.parse(fs.readFileSync(STATE_FILE, 'utf8'));
    } catch (e) {
      log(`Failed to parse state file: ${e.message}`, 'warn');
    }
  }
  return {};
}

function saveState(state) {
  try {
    fs.writeFileSync(STATE_FILE, JSON.stringify(state, null, 2), 'utf8');
  } catch (e) {
    log(`Failed to write state file: ${e.message}`, 'error');
  }
}

function getOpenPRs() {
  try {
    const output = execSync('gh pr list --state open --json number,headRefName,baseRefName,updatedAt', {
      encoding: 'utf8',
      stdio: ['pipe', 'pipe', 'ignore'] // ignore stderr to prevent cluttering
    });
    return JSON.parse(output);
  } catch (e) {
    log(`GitHub CLI execution failed: ${e.message}`, 'error');
    return null;
  }
}

log('Starting Pull Request Monitoring Agent...', 'info');

// Prime the state with the currently known state
const state = loadState();

function poll() {
  const prs = getOpenPRs();
  if (!prs) {
    return;
  }

  let stateUpdated = false;

  prs.forEach((pr) => {
    const prKey = String(pr.number);
    const lastUpdated = state[prKey];

    if (!lastUpdated || lastUpdated !== pr.updatedAt) {
      log(`[PR-ALERT] PR #${pr.number} (Branch: ${pr.headRefName}) has been updated/opened. Target base: ${pr.baseRefName}. Last Updated: ${pr.updatedAt}`, 'success');
      state[prKey] = pr.updatedAt;
      stateUpdated = true;
    }
  });

  if (stateUpdated) {
    saveState(state);
  }
}

// Initial poll
poll();

// Set interval
setInterval(poll, POLL_INTERVAL_MS);
