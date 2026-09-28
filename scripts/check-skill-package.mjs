#!/usr/bin/env node

import { readFile, lstat, realpath, mkdtemp, cp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

const defaultRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const skillName = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

function assert(condition, message) { if (!condition) throw new Error(message); }
function inside(root, path) { return path.startsWith(`${root}${sep}`); }
async function regularFile(root, path) {
  assert(inside(root, path), `reference escapes installed package: ${path}`);
  const stat = await lstat(path);
  assert(stat.isFile(), `not a regular packaged file: ${path}`);
  assert(inside(await realpath(root), await realpath(path)), `reference escapes installed package through symlink: ${path}`);
}
function decodeLink(raw) {
  const value = raw.trim();
  let destination, title;
  if (value.startsWith('<')) {
    const end = value.indexOf('>');
    assert(end > 0, `invalid angle Markdown destination: ${raw}`);
    destination = value.slice(1, end);
    title = value.slice(end + 1).trim();
  } else {
    const match = /^(\S+)(?:\s+([\s\S]+))?$/.exec(value);
    assert(match, `invalid Markdown destination: ${raw}`);
    destination = match[1];
    title = match[2]?.trim();
  }
  if (title) assert(/^(?:"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')$/.test(title), `invalid Markdown link title: ${raw}`);
  const target = destination.split('#', 1)[0].split('?', 1)[0];
  if (!target || /^(?:[a-z][\w+.-]*:|\/|#)/i.test(target)) return null;
  return decodeURIComponent(target);
}
function inlineTargets(content) {
  const targets = [];
  for (let start = content.indexOf(']('); start >= 0; start = content.indexOf('](', start + 2)) {
    let depth = 1, quote = null, angle = false, escaped = false;
    let destinationStarted = false, whitespaceAfterDestination = false, closed = false;
    for (let i = start + 2; i < content.length; i += 1) {
      const char = content[i];
      if (escaped) { escaped = false; continue; }
      if (char === '\\') { escaped = true; continue; }
      if (quote) { if (char === quote) quote = null; continue; }
      if (angle) { if (char === '>') angle = false; continue; }
      if (!destinationStarted && /\s/.test(char)) continue;
      if (!destinationStarted && char === '<') { destinationStarted = true; angle = true; continue; }
      if (/\s/.test(char) && depth === 1 && destinationStarted) { whitespaceAfterDestination = true; continue; }
      if ((char === '"' || char === "'") && whitespaceAfterDestination) { quote = char; continue; }
      destinationStarted = true;
      if (char === '(') depth += 1;
      if (char === ')' && --depth === 0) {
        targets.push(content.slice(start + 2, i));
        start = i;
        closed = true;
        break;
      }
    }
    assert(closed && !angle && !quote, `unclosed Markdown inline link near offset ${start}`);
  }
  return targets;
}
function markdownTargets(content) {
  // Ignore code examples: they are data, not Markdown links to package files.
  content = content.replace(/^ {0,3}(```|~~~)[^\n]*\n[\s\S]*?^ {0,3}\1[^\n]*$/gm, '').replace(/`[^`\n]*`/g, '');
  const definitions = new Map();
  for (const match of content.matchAll(/^ {0,3}\[([^\]]+)\]:\s*(?:<([^>]+)>|(\S+))/gm)) {
    definitions.set(match[1].trim().replace(/\s+/g, ' ').toLowerCase(), match[2] ?? match[3]);
  }
  // Validate every definition as a package dependency: a bare `[label]` may
  // resolve to it (shortcut reference syntax), with no explicit `[label][id]`.
  const targets = inlineTargets(content);
  targets.push(...definitions.values());
  for (const match of content.matchAll(/\[([^\]]+)\]\[([^\]]*)\]/g)) {
    const label = (match[2] || match[1]).trim().replace(/\s+/g, ' ').toLowerCase();
    assert(definitions.has(label), `unresolved Markdown reference [${label}]`);
    targets.push(definitions.get(label));
  }
  return targets;
}

export async function validateInstalled(manifest, installRoot) {
  assert(manifest?.version === 1 && typeof manifest.mcpServer === 'string' && manifest.mcpServer, 'invalid dependency manifest version/server');
  assert(manifest.skills && typeof manifest.skills === 'object', 'manifest skills missing');
  const skillsRoot = resolve(installRoot, 'skills');
  for (const [name, dependencies] of Object.entries(manifest.skills)) {
    assert(skillName.test(name) && name.length <= 64, `invalid skill name ${name}`);
    const skillRoot = join(skillsRoot, name);
    const main = join(skillRoot, 'SKILL.md');
    await regularFile(skillsRoot, main);
    const markdown = await readFile(main, 'utf8');
    const meta = spawnSync('go', ['run', './scripts/validate-skill-frontmatter.go', name, main], {
      cwd: defaultRoot,
      encoding: 'utf8',
      env: { ...process.env, GOCACHE: process.env.GOCACHE ?? join(tmpdir(), 'okf-skillcheck-go-cache') },
    });
    assert(meta.status === 0, `${name}: invalid frontmatter: ${meta.stderr || meta.error?.message || meta.stdout}`);
    assert(Array.isArray(dependencies.references) && Array.isArray(dependencies.mcpTools) && dependencies.cli, `${name}: invalid dependency lists`);
    for (const ref of dependencies.references) {
      const target = resolve(skillRoot, ref);
      await regularFile(skillRoot, target);
    }
    const seen = new Set();
    const queue = [main, ...dependencies.references.map(ref => resolve(skillRoot, ref))];
    while (queue.length) {
      const file = queue.pop();
      if (seen.has(file)) continue;
      seen.add(file);
      // Pinned, verbatim upstream specs contain illustrative bundle links.
      if (/\/spec-v02(?:-instant)?\.md$/.test(file)) continue;
      const content = await readFile(file, 'utf8');
      for (const raw of markdownTargets(content)) {
        const link = decodeLink(raw);
        if (!link) continue;
        const target = resolve(dirname(file), link);
        await regularFile(skillRoot, target);
        if (target.endsWith('.md')) queue.push(target);
      }
    }
  }
}

async function validateRepository(root, manifest) {
  for (const asset of manifest.toolkitAssets ?? []) await regularFile(root, resolve(root, asset));
  const names = Object.keys(manifest.skills).sort();
  const groups = JSON.parse(await readFile(join(root, 'skills.sh.json'), 'utf8'));
  const registered = groups.groupings.flatMap(group => group.skills).sort();
  assert(JSON.stringify(names) === JSON.stringify(registered), 'skills.sh registration differs from dependency manifest');
  for (const plugin of ['.codex-plugin/plugin.json', '.claude-plugin/plugin.json']) {
    const data = JSON.parse(await readFile(join(root, plugin), 'utf8'));
    assert(data.name === 'okf' && data.skills === (plugin.startsWith('.codex') ? './skills/' : './skills'), `${plugin}: wrong identity/skill root`);
  }
  const lock = spawnSync(process.execPath, [join(root, 'scripts/update-skills-lock.mjs'), '--check', '--root', root], { encoding: 'utf8' });
  assert(lock.status === 0, `stale skill lock: ${lock.stderr || lock.stdout}`);
}

export async function main(args) {
  assert(args.length === 0 || (args.length === 2 && args[0] === '--root'), 'usage: check-skill-package.mjs [--root REPO]');
  const root = resolve(args[1] ?? defaultRoot);
  const manifest = JSON.parse(await readFile(join(root, 'skill-dependencies.json'), 'utf8'));
  await validateRepository(root, manifest);
  let temp;
  try {
    temp = await mkdtemp(join(tmpdir(), 'okf-skills-install-'));
    await cp(join(root, 'skills'), join(temp, 'skills'), { recursive: true, dereference: false });
    await validateInstalled(manifest, temp);
  } finally { if (temp) await rm(temp, { recursive: true, force: true }); }
  process.stdout.write(`skill package and repository dependencies verified (${Object.keys(manifest.skills).length} skills)\n`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch(error => { process.stderr.write(`${error.message}\n`); process.exitCode = 1; });
}
