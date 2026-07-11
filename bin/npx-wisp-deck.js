#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');
const os = require('os');

const REPO = 'JackUait/wisp-deck';

// Allow overrides for testing
const home = process.env.HOME || os.homedir();
const installDir = process.env.WISP_DECK_INSTALL_DIR
  || path.join(home, '.local', 'share', 'wisp-deck');
const tuiBinDir = path.join(home, '.local', 'bin');
const tuiBinPath = path.join(tuiBinDir, 'wisp-deck-tui');

// Package root (where npm extracted us)
const pkgRoot = path.resolve(__dirname, '..');

function main() {
  // Platform check
  const platform = process.env.WISP_DECK_MOCK_PLATFORM || process.platform;
  if (platform !== 'darwin') {
    process.stderr.write(`Error: wisp-deck only supports macOS (detected: ${platform})\n`);
    process.exit(1);
  }

  const version = fs.readFileSync(path.join(pkgRoot, 'VERSION'), 'utf8').trim();

  // Check if already installed at correct version
  const versionMarker = path.join(installDir, '.version');
  let installedVersion = '';
  try {
    installedVersion = fs.readFileSync(versionMarker, 'utf8').trim();
  } catch (_) {
    // Not installed yet
  }

  if (installedVersion === version) {
    process.stdout.write(`wisp-deck ${version} already up to date\n`);
  } else {
    // Copy bash distribution to install dir
    process.stdout.write(`Installing wisp-deck ${version} to ${installDir}...\n`);
    copyDistribution(pkgRoot, installDir);
    fs.writeFileSync(versionMarker, version + '\n');
    process.stdout.write(`Installed wisp-deck ${version}\n`);
  }

  // Download TUI binary if needed
  if (!process.env.WISP_DECK_SKIP_TUI_DOWNLOAD) {
    ensureTuiBinary(version);
  }

  // Exec the bash installer
  if (!process.env.WISP_DECK_SKIP_EXEC) {
    const installer = path.join(installDir, 'bin', 'wisp-deck');
    const args = process.argv.slice(2);
    try {
      execFileSync('bash', [installer, ...args], { stdio: 'inherit' });
    } catch (err) {
      process.exit(err.status || 1);
    }
  }
}

// Recursively copy the bash distribution files.
function copyDistribution(src, dest) {
  const entries = [
    'bin/wisp-deck',
    'bin/wisp-deck-config',
    'lib',
    'templates',
    'defaults',
    'ghostty',
    'terminals',
    'wrapper.sh',
    'VERSION',
  ];

  for (const entry of entries) {
    const srcPath = path.join(src, entry);
    if (!fs.existsSync(srcPath)) continue;
    const destPath = path.join(dest, entry);
    copyRecursive(srcPath, destPath);
  }
}

function copyRecursive(src, dest) {
  const stat = fs.statSync(src);
  if (stat.isDirectory()) {
    fs.mkdirSync(dest, { recursive: true });
    for (const child of fs.readdirSync(src)) {
      copyRecursive(path.join(src, child), path.join(dest, child));
    }
  } else {
    fs.mkdirSync(path.dirname(dest), { recursive: true });
    fs.copyFileSync(src, dest);
    // Preserve executable bit
    if (stat.mode & 0o111) {
      fs.chmodSync(dest, stat.mode);
    }
  }
}

// Download the TUI binary from GitHub Releases if missing or wrong version.
function ensureTuiBinary(version) {
  // Check if existing binary matches version
  try {
    const out = execFileSync(tuiBinPath, ['--version'], { encoding: 'utf8' });
    const installed = out.replace(/.*version\s*/, '').trim();
    if (installed === version) {
      process.stdout.write(`wisp-deck-tui ${version} already up to date\n`);
      return;
    }
    process.stdout.write(`Updating wisp-deck-tui (${installed} -> ${version})...\n`);
  } catch (_) {
    process.stdout.write(`Downloading wisp-deck-tui ${version}...\n`);
  }

  const arch = process.arch === 'x64' ? 'amd64' : process.arch;
  const url = `https://github.com/${REPO}/releases/download/v${version}/wisp-deck-tui-darwin-${arch}`;

  fs.mkdirSync(tuiBinDir, { recursive: true });
  downloadFile(url, tuiBinPath);
  fs.chmodSync(tuiBinPath, 0o755);
  process.stdout.write(`wisp-deck-tui ${version} installed\n`);
}

// Synchronous HTTPS download with redirect following.
function downloadFile(url, dest) {
  try {
    execFileSync('curl', ['-fsSL', '-o', dest, url], {
      stdio: ['pipe', 'pipe', 'pipe'],
    });
  } catch (_) {
    process.stderr.write(`Failed to download ${url}\n`);
    process.stderr.write('Check your network connection and that this version has been released.\n');
    process.exit(1);
  }
}

main();
