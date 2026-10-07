#!/usr/bin/env node
// The fork installs a binary built from the same source as its Pi resources.
import { spawnSync } from 'node:child_process';
import * as fs from 'node:fs';
import { dirname, join, resolve, delimiter } from 'node:path';
import { fileURLToPath } from 'node:url';

const action = process.argv[2];
if (action !== 'install' && action !== 'uninstall') {
  console.error('usage: run-lifecycle.mjs install|uninstall');
  process.exit(2);
}
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const env = { ...process.env };
if (action === 'install') {
  const binary = join(root, process.platform === 'win32' ? 'pi-web.exe' : 'pi-web');
  const args = ['build', `BINARY=${binary}`];
  if (process.platform === 'win32') {
    const git = spawnSync('git', ['--exec-path'], { encoding: 'utf8' });
    if (git.error || git.status !== 0) throw new Error('Building pi-web requires Git for Windows.');
    const bash = resolve(git.stdout.trim(), '../../../usr/bin/bash.exe');
    if (!fs.existsSync(bash)) throw new Error('Building pi-web requires Git Bash.');
    args.push('SHELL=bash.exe');
    // Build-only tools; preserve the service and login PATH.
    for (const key of Object.keys(env)) if (key.toUpperCase() === 'PATH') delete env[key];
    env.PATH = dirname(bash) + delimiter + process.env.PATH;
  }
  const build = spawnSync(process.env.MAKE || 'make', args, { cwd: root, env, stdio: 'inherit' });
  if (build.error) throw new Error(`Building pi-web requires Go, Node and GNU Make: ${build.error.message}`);
  if (build.status !== 0) process.exit(build.status ?? 1);
  const version = spawnSync(binary, ['-version'], { encoding: 'utf8' });
  if (version.error || version.status !== 0 || !version.stdout.trim()) throw new Error('The built pi-web binary did not report its version.');
  env.PI_WEB_SOURCE_BINARY = binary;
  env.PI_WEB_SOURCE_VERSION = version.stdout.trim();
}
const result = process.platform === 'win32'
  ? spawnSync('powershell.exe', ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', join(root, `${action}.ps1`)], { cwd: root, env, stdio: 'inherit' })
  : spawnSync('bash', [join(root, `${action}.sh`)], { cwd: root, env, stdio: 'inherit' });
if (result.error) {
  console.error(result.error.message);
  process.exit(1);
}
process.exit(result.status ?? 1);
