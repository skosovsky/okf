#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { readFile, readdir, writeFile } from 'node:fs/promises';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const FIELDS = ['source', 'sourceUrl', 'ref', 'sourceType', 'skillPath', 'computedHash', 'subagents', 'wellKnownDigest'];
const OPTIONAL_STRINGS = ['sourceUrl', 'ref', 'skillPath', 'wellKnownDigest'];
const HASH = /^[0-9a-f]{64}$/;
const NAME = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/;
class UsageError extends Error {}
class SchemaError extends Error {}
const fail = (message) => { throw new SchemaError(message); };
const object = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);

// Byte-compatible with vercel-labs/skills src/local-lock.ts: localeCompare and no delimiters.
export async function computeSkillFolderHash(skillDir) {
  const root = resolve(skillDir), files = [];
  async function collect(dir) {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory() && entry.name !== '.git' && entry.name !== 'node_modules') await collect(path);
      else if (entry.isFile()) files.push({ path: relative(root, path).split('\\').join('/'), content: await readFile(path) });
    }
  }
  await collect(root);
  files.sort((a, b) => a.path.localeCompare(b.path));
  const hash = createHash('sha256');
  for (const file of files) hash.update(file.path).update(file.content);
  return hash.digest('hex');
}

function usage() {
  return 'Usage:\n  node scripts/update-skills-lock.mjs --check [--root <repo>]\n  node scripts/update-skills-lock.mjs --write [--root <repo>]\n  node scripts/update-skills-lock.mjs --hash <skill-directory>';
}

function argumentsFor(argv) {
  let mode, root = ROOT, hashDir;
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--help' || arg === '-h') return { mode: 'help' };
    if (arg === '--check' || arg === '--write' || arg === '--hash') {
      if (mode) throw new UsageError('exactly one of --check, --write, or --hash is required');
      mode = arg.slice(2);
      if (mode === 'hash' && (!argv[i + 1] || argv[i + 1].startsWith('--'))) throw new UsageError('--hash requires a skill directory');
      if (mode === 'hash') hashDir = argv[++i];
    } else if (arg === '--root') {
      if (!argv[i + 1] || argv[i + 1].startsWith('--')) throw new UsageError('--root requires a repository directory');
      root = resolve(argv[++i]);
    } else throw new UsageError(`unknown argument: ${arg}`);
  }
  if (!mode) throw new UsageError('exactly one of --check, --write, or --hash is required');
  if (mode === 'hash' && root !== ROOT) throw new UsageError('--root cannot be combined with --hash');
  return { mode, root, hashDir };
}

function validate(lock) {
  if (!object(lock)) fail('skills-lock.json must contain a JSON object');
  const keys = Object.keys(lock);
  if (keys.length !== 2 || !keys.includes('version') || !keys.includes('skills')) fail('skills-lock.json must contain exactly version and skills');
  if (lock.version !== 1) fail('skills-lock.json version must be 1');
  if (!object(lock.skills)) fail('skills-lock.json skills must be an object');
  for (const [name, entry] of Object.entries(lock.skills)) {
    if (!NAME.test(name)) fail(`invalid skill name: ${JSON.stringify(name)}`);
    if (!object(entry)) fail(`skills.${name} must be an object`);
    for (const key of Object.keys(entry)) if (!FIELDS.includes(key)) fail(`skills.${name} contains unknown field ${JSON.stringify(key)}`);
    for (const key of ['source', 'sourceType']) if (typeof entry[key] !== 'string' || !entry[key]) fail(`skills.${name}.${key} must be a non-empty string`);
    if (typeof entry.computedHash !== 'string' || !HASH.test(entry.computedHash)) fail(`skills.${name}.computedHash must be 64 lowercase hex characters`);
    for (const key of OPTIONAL_STRINGS) if (key in entry && (typeof entry[key] !== 'string' || !entry[key])) fail(`skills.${name}.${key} must be a non-empty string`);
    if ('subagents' in entry && (!Array.isArray(entry.subagents) || entry.subagents.some((value) => typeof value !== 'string'))) fail(`skills.${name}.subagents must be an array of strings`);
  }
}

async function expectedLock(root, lock) {
  const skills = {};
  for (const name of Object.keys(lock.skills).sort()) {
    const entry = lock.skills[name], computedHash = await computeSkillFolderHash(join(root, 'skills', name));
    skills[name] = {};
    for (const key of FIELDS) if (key in entry || key === 'computedHash') skills[name][key] = key === 'computedHash' ? computedHash : entry[key];
  }
  return { version: 1, skills };
}

async function run(argv) {
  const { mode, root, hashDir } = argumentsFor(argv);
  if (mode === 'help') return void process.stdout.write(`${usage()}\n`);
  if (mode === 'hash') return void process.stdout.write(`${await computeSkillFolderHash(hashDir)}\n`);
  const path = join(root, 'skills-lock.json');
  let lock;
  try { lock = JSON.parse(await readFile(path, 'utf8')); } catch (error) { throw new SchemaError(`cannot read ${path}: ${error.message}`); }
  validate(lock);
  const expected = await expectedLock(root, lock);
  const stale = Object.keys(expected.skills).filter((name) => expected.skills[name].computedHash !== lock.skills[name].computedHash);
  if (mode === 'check') {
    if (!stale.length) return void process.stdout.write('skills-lock.json is current\n');
    for (const name of stale) process.stderr.write(`${name}: lock has ${lock.skills[name].computedHash}, expected ${expected.skills[name].computedHash}\n`);
    return 1;
  }
  await writeFile(path, `${JSON.stringify(expected, null, 2)}\n`, 'utf8');
  process.stdout.write(`updated ${relative(process.cwd(), path) || 'skills-lock.json'}\n`);
}

try { process.exitCode = (await run(process.argv.slice(2))) || 0; }
catch (error) {
  process.stderr.write(`${error.message}\n${error instanceof UsageError ? `${usage()}\n` : ''}`);
  process.exitCode = 2;
}
