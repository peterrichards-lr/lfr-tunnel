#!/usr/bin/env node
const fs = require('fs');
const path = require('path');
const { execSync, execFileSync } = require('child_process');

function log(msg, type = 'info') {
  const colors = {
    info: '\x1b[34m', // Blue
    success: '\x1b[32m', // Green
    warn: '\x1b[33m', // Yellow
    error: '\x1b[31m', // Red
    reset: '\x1b[0m',
  };
  console.log(`${colors[type]}${msg}${colors.reset}`);
}

// Check dependencies
try {
  execSync('gh --version', { stdio: 'ignore' });
} catch (e) {
  log(
    'Error: GitHub CLI (gh) is not installed. Please install it and log in.',
    'error'
  );
  process.exit(1);
}

try {
  execSync('gh auth status', { stdio: 'ignore' });
} catch (e) {
  log(
    'Error: GitHub CLI is not authenticated. Please run "gh auth login".',
    'error'
  );
  process.exit(1);
}

// Parse args
const args = process.argv.slice(2);
const dryRun = args.includes('--dry-run');
const jsonArg = args.find((a) => a.endsWith('.json'));

if (!jsonArg) {
  log('Usage: node gh-issue-sync.cjs <issues.json> [--dry-run]', 'warn');
  process.exit(1);
}

const jsonPath = path.resolve(jsonArg);
if (!fs.existsSync(jsonPath)) {
  log(`Error: File not found at ${jsonPath}`, 'error');
  process.exit(1);
}

const config = JSON.parse(fs.readFileSync(jsonPath, 'utf8'));

let commitHash = 'master';
try {
  commitHash = execSync('git rev-parse --short HEAD', {
    encoding: 'utf8',
  }).trim();
} catch (e) {
  // Silent fallback
}

let needsSave = false;

log(`=== Reusable GitHub Issue Sync ${dryRun ? '(DRY RUN)' : ''} ===`, 'info');
log(`Referencing commit: ${commitHash}`, 'info');

// Create Epic or reuse existing
let epicNumber = config.epicNumber;
if (epicNumber) {
  log(`Reusing existing Epic: #${epicNumber}`, 'info');
} else {
  log(`\nCreating Epic: "${config.title}"...`, 'info');
  const args = ['issue', 'create', '--title', config.title, '--body', config.body];
  (config.labels || []).forEach(l => {
    args.push('--label', l);
  });

  if (dryRun) {
    log(`[DRY RUN] Would execute: gh ${args.join(' ')}`, 'success');
    epicNumber = '123';
  } else {
    const epicUrl = execFileSync('gh', args, { encoding: 'utf8' }).trim();
    epicNumber = epicUrl.split('/').pop();
    config.epicNumber = epicNumber;
    needsSave = true;
    log(
      `Epic created successfully: Issue #${epicNumber} (${epicUrl})`,
      'success'
    );
  }
}

// Create Sub-issues
if (config.issues && config.issues.length > 0) {
  config.issues.forEach((issue, idx) => {
    log(
      `\nProcessing sub-issue [${idx + 1}/${config.issues.length}]: "${issue.title}"...`,
      'info'
    );
    const bodyText = `${issue.body}\n\n(Belongs to Epic #${epicNumber})`;
    const args = ['issue', 'create', '--title', issue.title, '--body', bodyText];
    (issue.labels || []).forEach(l => {
      args.push('--label', l);
    });

    if (dryRun) {
      log(`[DRY RUN] Would execute: gh ${args.join(' ')}`, 'success');
      if (issue.completed && !issue.closed) {
        log(`[DRY RUN] Would comment and close sub-issue.`, 'success');
      }
    } else {
      let subIssueNumber = issue.issueNumber;
      if (subIssueNumber) {
        log(`Reusing sub-issue: #${subIssueNumber}`, 'info');
      } else {
        const subIssueUrl = execFileSync('gh', args, { encoding: 'utf8' }).trim();
        subIssueNumber = subIssueUrl.split('/').pop();
        issue.issueNumber = subIssueNumber;
        needsSave = true;
        log(
          `Sub-issue created: Issue #${subIssueNumber} (${subIssueUrl})`,
          'success'
        );
      }

      if (issue.completed && !issue.closed) {
        log(`Closing completed sub-issue #${subIssueNumber}...`, 'info');
        execFileSync('gh', ['issue', 'comment', subIssueNumber, '--body', `This issue was successfully implemented and verified in commit ${commitHash}. Closing.`]);
        execFileSync('gh', ['issue', 'close', subIssueNumber]);
        issue.closed = true;
        needsSave = true;
        log(`Issue #${subIssueNumber} closed successfully.`, 'success');
      }
    }
  });
}

if (needsSave && !dryRun) {
  fs.writeFileSync(jsonPath, JSON.stringify(config, null, 2), 'utf8');
  log('\nUpdated JSON file with issue numbers.', 'info');
}

log('\nAll sync operations completed!', 'success');
