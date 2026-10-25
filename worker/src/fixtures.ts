import { readFile, mkdtemp, rm } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join, isAbsolute } from 'node:path';
import { bindingName, parseAllowedOrigins } from './contract.js';

export type FixtureProfile = {
  id: string;
  name: string;
  description: string;
  origins: string[];
  secret_refs: string[];
  prepare: string[];
  cleanup: string[];
  environment?: string[];
  timeout_ms?: number;
};
export async function loadFixtures(path?: string): Promise<FixtureProfile[]> {
  if (!path) return [];
  const raw = await readFile(path, 'utf8');
  if (Buffer.byteLength(raw) > 65536) throw new Error('Fixture configuration too large');
  const profiles: unknown = JSON.parse(raw);
  if (!Array.isArray(profiles) || profiles.length > 20)
    throw new Error('Invalid fixture configuration');
  const seen = new Set<string>();
  for (const p of profiles) {
    if (
      !p ||
      !bindingName.test(p.id) ||
      seen.has(p.id) ||
      typeof p.name !== 'string' ||
      !Array.isArray(p.origins) ||
      !p.origins.length ||
      !Array.isArray(p.secret_refs) ||
      p.secret_refs.length > 50 ||
      p.secret_refs.some(
        (s: unknown) => typeof s !== 'string' || !/^QA_TEST_[A-Z0-9_]{1,92}$/.test(s)
      )
    )
      throw new Error('Invalid fixture profile');
    seen.add(p.id);
    for (const origin of p.origins) parseAllowedOrigins(origin);
    for (const cmd of [p.prepare, p.cleanup])
      if (
        !Array.isArray(cmd) ||
        !cmd.length ||
        cmd.length > 30 ||
        !isAbsolute(cmd[0]) ||
        cmd.some(
          (arg: unknown) => typeof arg !== 'string' || arg.length > 4000 || arg.includes('\0')
        )
      )
        throw new Error('Fixture commands must be configured argument arrays');
    if (
      p.environment !== undefined &&
      (!Array.isArray(p.environment) ||
        p.environment.some(
          (key: unknown) => typeof key !== 'string' || !/^QA_FIXTURE_[A-Z0-9_]+$/.test(key)
        ))
    )
      throw new Error('Invalid fixture environment references');
    if (
      p.timeout_ms !== undefined &&
      (!Number.isInteger(p.timeout_ms) || p.timeout_ms < 100 || p.timeout_ms > 60000)
    )
      throw new Error('Invalid fixture timeout');
  }
  return profiles as FixtureProfile[];
}

function command(
  args: string[],
  env: NodeJS.ProcessEnv,
  timeout: number,
  signal?: AbortSignal
): Promise<string> {
  return new Promise((resolve, reject) => {
    let output = '',
      bytes = 0,
      settled = false;
    const child = spawn(args[0], args.slice(1), {
      env,
      stdio: ['ignore', 'pipe', 'ignore'],
      detached: process.platform !== 'win32'
    });
    const fail = () => {
      if (settled) return;
      settled = true;
      try {
        if (process.platform !== 'win32' && child.pid) process.kill(-child.pid, 'SIGKILL');
        else child.kill('SIGKILL');
      } catch {}
      done();
      reject(new Error('Configured test-data routine failed or timed out'));
    };
    const timer = setTimeout(fail, timeout);
    function done() {
      clearTimeout(timer);
      signal?.removeEventListener('abort', fail);
    }
    signal?.addEventListener('abort', fail, { once: true });
    if (signal?.aborted) fail();
    child.stdout.on('data', (chunk: Buffer) => {
      bytes += chunk.length;
      if (bytes > 65536) {
        fail();
        return;
      }
      output += chunk.toString('utf8');
    });
    child.once('error', fail);
    child.once('close', (code) => {
      if (settled) return;
      settled = true;
      done();
      if (code !== 0) reject(new Error('Configured test-data routine failed'));
      else resolve(output);
    });
  });
}

export class RunFixture {
  private directory = '';
  constructor(
    readonly profile: FixtureProfile,
    readonly runID: string,
    readonly origin: string
  ) {}
  private environment(): NodeJS.ProcessEnv {
    const env: NodeJS.ProcessEnv = {
      PATH: process.env.PATH,
      LANG: process.env.LANG,
      TMPDIR: process.env.TMPDIR,
      QA_RUN_ID: this.runID,
      QA_TARGET_ORIGIN: this.origin,
      QA_FIXTURE_STATE_DIR: this.directory
    };
    for (const key of this.profile.environment ?? []) {
      if (!process.env[key]) throw new Error('Configured fixture environment is unavailable');
      env[key] = process.env[key];
    }
    return env;
  }
  async prepare(signal: AbortSignal): Promise<Record<string, string>> {
    if (!this.profile.origins.includes(this.origin))
      throw new Error('Fixture is not configured for this origin');
    this.directory = await mkdtemp(join(tmpdir(), 'qa-fixture-'));
    const raw = await command(
      this.profile.prepare,
      this.environment(),
      this.profile.timeout_ms ?? 30000,
      signal
    );
    const output = JSON.parse(raw) as { env: Record<string, unknown> };
    if (
      !output ||
      !output.env ||
      typeof output.env !== 'object' ||
      Array.isArray(output.env) ||
      Object.keys(output).some((k) => k !== 'env')
    )
      throw new Error('Invalid fixture output');
    if (Object.keys(output.env).some((key) => !this.profile.secret_refs.includes(key)))
      throw new Error('Fixture returned an unregistered credential');
    const env: Record<string, string> = {};
    for (const key of this.profile.secret_refs) {
      const value = output.env[key];
      if (typeof value !== 'string' || !value || value.length > 4000)
        throw new Error('Fixture did not provide its registered test credentials');
      env[key] = value;
    }
    return env;
  }
  async cleanup(): Promise<void> {
    try {
      if (this.directory)
        await command(this.profile.cleanup, this.environment(), this.profile.timeout_ms ?? 30000);
    } finally {
      if (this.directory) await rm(this.directory, { recursive: true, force: true });
    }
  }
}
