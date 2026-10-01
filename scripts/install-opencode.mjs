// Install the pinned native runtime locally without running package scripts.
import { spawnSync } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const version = '1.18.34';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const destination = resolve(root, 'artifacts', 'opencode-runtime');
if (!['darwin', 'linux'].includes(process.platform) || !['arm64', 'x64'].includes(process.arch)) throw new Error('This local pilot supports macOS and Linux on arm64 or x64.');
const name = `opencode-${process.platform}-${process.arch}${process.arch === 'x64' ? '-baseline' : ''}`;
await mkdir(destination, { recursive: true });
const install = spawnSync('npm', ['install', '--prefix', destination, '--ignore-scripts', '--no-audit', '--no-fund', `${name}@${version}`], { cwd: root, stdio: 'inherit' });
if (install.error || install.status !== 0) throw new Error('Pinned OpenCode runtime installation failed.');
const binary = resolve(destination, 'node_modules', name, 'bin', 'opencode');
const check = spawnSync(binary, ['--version'], { encoding: 'utf8', timeout: 15000 });
if (check.error || check.status !== 0 || check.stdout.trim() !== version) throw new Error('OpenCode runtime version check failed.');
console.log(`QA_OPENCODE_BINARY=${binary}`);
if (process.env.GITHUB_ENV) {
  const { appendFile } = await import('node:fs/promises');
  await appendFile(process.env.GITHUB_ENV, `TEST_OPENCODE_BINARY=${binary}\n`);
}
