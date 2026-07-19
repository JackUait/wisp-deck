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

  if (installedVersion === version && isInstallIntact(installDir)) {
    process.stdout.write(`wisp-deck ${version} already up to date\n`);
  } else {
    // Copy bash distribution to install dir
    process.stdout.write(`Installing wisp-deck ${version} to ${installDir}...\n`);
    copyDistribution(pkgRoot, installDir);
    fs.writeFileSync(versionMarker, version + '\n');
    process.stdout.write(`Installed wisp-deck ${version}\n`);
  }

  // Download TUI binary if needed
  const skipTuiDownload = process.env.WISP_DECK_TESTING === '1'
    && process.env.WISP_DECK_SKIP_TUI_DOWNLOAD === '1';
  if (!skipTuiDownload) {
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

// The .version marker says which version was installed, not that the install
// is still whole. An interrupted copy or a partial delete leaves the marker
// behind, and trusting it alone made the launcher report "already up to date"
// and then exec a bin/wisp-deck that wasn't there. Re-running `npx wisp-deck`
// — the first thing a stuck user tries — must repair that, so spot-check the
// files the launcher and the wrapper actually depend on.
function isInstallIntact(dir) {
  return [
    'bin/wisp-deck',
    'bin/wisp-deck-config',
    'lib',
    'wrapper.sh',
    'VERSION',
  ].every((rel) => fs.existsSync(path.join(dir, rel)));
}

// Recursively copy the bash distribution files. Each entry's destination is
// removed first so files dropped between versions don't linger and get
// sourced/globbed by the new code.
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
    const destPath = path.join(dest, entry);
    fs.rmSync(destPath, { recursive: true, force: true });
    if (!fs.existsSync(srcPath)) continue;
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

// Check the exact version and compiled production host-effect boundary. Runtime
// host_effects_allowed is diagnostic only and may be false under test ancestry.
function verifyTuiBinary(binaryPath, expectedVersion) {
  let reported = '';
  try {
    reported = execFileSync(binaryPath, ['--version'], { encoding: 'utf8' }).trim();
    if (reported !== `wisp-deck-tui version ${expectedVersion}`) {
      return { valid: false, reported };
    }

    const output = execFileSync(
      binaryPath,
      ['capabilities', '--require-production'],
      { encoding: 'utf8' },
    );
    const capabilities = JSON.parse(output);
    const valid = capabilities !== null
      && typeof capabilities === 'object'
      && !Array.isArray(capabilities)
      && capabilities.host_effects_compiled === true
      && capabilities.sound_preview_compiled === true
      && Number.isInteger(capabilities.host_effects_boundary)
      && capabilities.host_effects_boundary === 1;
    return { valid, reported };
  } catch (_) {
    return { valid: false, reported };
  }
}

function tuiAssetArchitecture() {
  const arch = process.env.WISP_DECK_TESTING === '1'
    && process.env.WISP_DECK_MOCK_ARCH
    ? process.env.WISP_DECK_MOCK_ARCH
    : process.arch;
  switch (arch) {
    case 'x64':
      return 'amd64';
    case 'arm64':
      return 'arm64';
    default:
      process.stderr.write(`Unsupported architecture: ${arch}\n`);
      process.exit(1);
  }
}

// Download the TUI binary from GitHub Releases if missing or invalid.
function ensureTuiBinary(version) {
  const existing = verifyTuiBinary(tuiBinPath, version);
  if (existing.valid) {
    process.stdout.write(`wisp-deck-tui ${version} already up to date\n`);
    return;
  }
  if (fs.existsSync(tuiBinPath)) {
    process.stdout.write(`Updating wisp-deck-tui (${existing.reported || 'unknown'} -> ${version})...\n`);
  } else {
    process.stdout.write(`Downloading wisp-deck-tui ${version}...\n`);
  }

  const arch = tuiAssetArchitecture();
  const url = `https://github.com/${REPO}/releases/download/v${version}/wisp-deck-tui-darwin-${arch}`;

  // Download to a temp path and only replace the real binary after the new
  // one runs and reports the expected version — a failed, truncated, or
  // wrong-version download must never clobber a working install. Running it
  // here also pays the first-exec Gatekeeper assessment at install time.
  fs.mkdirSync(tuiBinDir, { recursive: true });
  const tmpPath = tuiBinPath + '.download-' + process.pid;
  downloadFile(url, tmpPath);
  fs.chmodSync(tmpPath, 0o755);
  const downloaded = verifyTuiBinary(tmpPath, version);
  if (!downloaded.valid) {
    fs.rmSync(tmpPath, { force: true });
    process.stderr.write(`Downloaded wisp-deck-tui failed verification (expected version ${version}, got ${JSON.stringify(downloaded.reported)}).\n`);
    process.stderr.write('The existing install (if any) was left untouched. Please retry, and report this if it persists.\n');
    process.exit(1);
  }
  fs.renameSync(tmpPath, tuiBinPath);
  process.stdout.write(`wisp-deck-tui ${version} installed\n`);
}

// Synchronous HTTPS download with redirect following. Retries transient
// failures and bounds connection hangs; a failed attempt leaves no partial
// file behind.
function downloadFile(url, dest) {
  try {
    execFileSync('curl', [
      '-fsSL',
      '--retry', '3',
      '--retry-delay', '1',
      '--connect-timeout', '15',
      '-o', dest, url,
    ], {
      stdio: ['pipe', 'pipe', 'pipe'],
    });
  } catch (_) {
    fs.rmSync(dest, { force: true });
    process.stderr.write(`Failed to download ${url}\n`);
    process.stderr.write('Check your network connection and that this version has been released.\n');
    process.exit(1);
  }
}

main();
