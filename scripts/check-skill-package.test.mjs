import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { validateInstalled } from './check-skill-package.mjs';

const manifest = { version: 1, mcpServer: 'okf-mcp', skills: { demo: { mcpTools: [], cli: {}, references: ['references/contract.md'] } } };

test('installed package resolves references within its own tree', async () => {
  // Arrange.
  const root = await mkdtemp(join(tmpdir(), 'okf-package-test-'));
  try {
    const dir = join(root, 'skills/demo');
    await mkdir(join(dir, 'references'), { recursive: true });
    await writeFile(join(dir, 'SKILL.md'), '---\nname: demo\ndescription: Demo skill\n---\n[contract](references/contract.md)\n');
    await writeFile(join(dir, 'references/contract.md'), '# Contract\n');
    // Act/Assert: installation works; removing a packaged reference fails.
    await validateInstalled(manifest, root);
    const missing = structuredClone(manifest);
    missing.skills.demo.references.push('references/missing.md');
    await assert.rejects(validateInstalled(missing, root), /ENOENT/);
    const escaping = structuredClone(manifest);
    escaping.skills.demo.references.push('../../outside.md');
    await assert.rejects(validateInstalled(escaping, root), /escapes installed package/);
    await writeFile(join(dir, 'SKILL.md'), '---\nname: demo\ndescription: Demo skill\n---\n[details][target]\n\n[target]: references/contract.md\n');
    await validateInstalled(manifest, root);
    await writeFile(join(dir, 'SKILL.md'), '---\nname: demo\ndescription: Demo skill\n---\n[details][target]\n\n[target]: references/missing.md\n');
    await assert.rejects(validateInstalled(manifest, root), /ENOENT/);
    await writeFile(join(dir, 'SKILL.md'), '---\nname: demo\ndescription: Demo skill\n---\n[details]\n\n[details]: references/contract.md\n');
    await validateInstalled(manifest, root);
    await writeFile(join(dir, 'SKILL.md'), '---\nname: demo\ndescription: Demo skill\n---\n[details]\n\n[details]: references/missing.md\n');
    await assert.rejects(validateInstalled(manifest, root), /ENOENT/);
    await writeFile(join(dir, 'SKILL.md'), '---\nname: demo\ndescription: Demo skill\n---\n[details][]\n\n[details]: references/missing.md\n');
    await assert.rejects(validateInstalled(manifest, root), /ENOENT/);
    for (const destination of ['<references/contract.md>', 'references/contract.md "Title"', "references/contract.md 'Title'", 'references/contract.md "Title (extra)"']) {
      await writeFile(join(dir, 'SKILL.md'), `---\nname: demo\ndescription: Demo skill\n---\n[contract](${destination})\n`);
      await validateInstalled(manifest, root);
    }
    for (const destination of ['<references/missing.md>', 'references/missing.md "Title"', "references/missing.md 'Title'"]) {
      await writeFile(join(dir, 'SKILL.md'), `---\nname: demo\ndescription: Demo skill\n---\n[contract](${destination})\n`);
      await assert.rejects(validateInstalled(manifest, root), /ENOENT/);
    }
    for (const quote of ["'", '"']) {
      const existing = `references/author${quote}s.md`;
      await writeFile(join(dir, existing), '# Author\n');
      await writeFile(join(dir, 'SKILL.md'), `---\nname: demo\ndescription: Demo skill\n---\n[author](${existing})\n`);
      await validateInstalled(manifest, root);
      await writeFile(join(dir, 'SKILL.md'), `---\nname: demo\ndescription: Demo skill\n---\n[missing](references/missing${quote}s.md)\n`);
      await assert.rejects(validateInstalled(manifest, root), /ENOENT/);
    }
    await writeFile(join(dir, 'SKILL.md'), '---\nname: demo\ndescription: Demo skill\n---\n[broken](references/contract.md "unclosed)\n');
    await assert.rejects(validateInstalled(manifest, root), /unclosed Markdown inline link/);
    await writeFile(join(dir, 'SKILL.md'), '---\nname: demo\ndescription: Demo skill\n---\n[site](https://example.invalid/path(with-parentheses) "Site") [section](#local)\n`[code](references/missing.md)`\n```md\n[fenced](references/missing.md)\n```\n');
    await validateInstalled(manifest, root);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('frontmatter validates parsed YAML values and unique metadata', async () => {
  // Arrange.
  const root = await mkdtemp(join(tmpdir(), 'okf-frontmatter-test-'));
  try {
    const dir = join(root, 'skills/demo');
    await mkdir(join(dir, 'references'), { recursive: true });
    await writeFile(join(dir, 'references/contract.md'), '# Contract\n');
    const invalid = [
      ['empty folded description', 'name: demo\ndescription: >\n  \n'],
      ['oversized folded description', `name: demo\ndescription: >\n  ${'x'.repeat(1025)}\n`],
      ['consecutive name hyphens', 'name: de--mo\ndescription: Demo\n'],
      ['malformed YAML', 'name: demo\ndescription: [broken\n'],
      ['duplicate name', 'name: demo\nname: other\ndescription: Demo\n'],
      ['duplicate conflicting description', 'name: demo\ndescription: Demo\ndescription: Other\n'],
      ['oversized compatibility', `name: demo\ndescription: Demo\ncompatibility: ${'x'.repeat(501)}\n`],
    ];
    for (const [scenario, yaml] of invalid) {
      // Act/Assert.
      await writeFile(join(dir, 'SKILL.md'), `---\n${yaml}---\n# Body\n`);
      await assert.rejects(validateInstalled(manifest, root), /invalid frontmatter/, scenario);
    }
  } finally { await rm(root, { recursive: true, force: true }); }
});
