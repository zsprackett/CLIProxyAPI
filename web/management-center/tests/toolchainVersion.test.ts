import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';

const readProjectFile = (path: string) =>
  readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');
const packageJson = JSON.parse(readProjectFile('package.json')) as { packageManager: string };
const bunVersion = packageJson.packageManager.match(/^bun@(\d+\.\d+\.\d+)$/)?.[1];

describe('Bun toolchain version', () => {
  test('pins an exact Bun release', () => {
    expect(bunVersion).toBeDefined();
  });

  // The subtree's workflows are replaced by this repo's panel-ci workflow.
  test.each(['../../.github/workflows/panel-ci.yml'])('%s uses the pinned Bun release', (path) => {
    expect(readProjectFile(path)).toContain(`bun-version: '${bunVersion}'`);
  });

  test.each(['README.md', 'README_CN.md'])('%s documents the pinned Bun release', (path) => {
    expect(readProjectFile(path)).toContain(`**Bun ${bunVersion}**`);
  });

  test('repository guidance documents the pinned Bun release', () => {
    expect(readProjectFile('AGENTS.md')).toContain(`\`bun@${bunVersion}\``);
  });
});
