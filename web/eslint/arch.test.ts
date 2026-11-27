import path from 'node:path';

import { ESLint } from 'eslint';
import { describe, expect, it } from 'vitest';

import { archConfig } from './arch.js';

const root = path.join(import.meta.dirname, 'fixtures', 'arch');

async function ruleIds(file: string): Promise<string[]> {
  const eslint = new ESLint({
    cwd: root,
    overrideConfigFile: true,
    overrideConfig: archConfig(root),
  });
  const [result] = await eslint.lintFiles([path.join(root, 'src', file)]);
  return (result?.messages ?? []).map((m) => m.ruleId ?? `fatal:${m.message}`);
}

// Self-test of the architecture rules: every violating fixture must be caught, every clean one must pass.
describe('[T-ARCH-04] web architecture rules', () => {
  it('passes imports inside a feature and from a feature to api', async () => {
    expect(await ruleIds('features/a/clean.ts')).toEqual([]);
    expect(await ruleIds('api/clean.ts')).toEqual([]);
  });

  it.each([
    ['features/a/imports-sibling-feature.ts', 'no-restricted-imports'],
    ['features/a/imports-routes.ts', 'no-restricted-imports'],
    ['features/a/parent-relative.ts', 'no-restricted-imports'],
    ['api/uses-react.ts', 'no-restricted-imports'],
    ['lib/imports-api.ts', 'no-restricted-imports'],
    ['components/imports-feature.ts', 'no-restricted-imports'],
    ['auth/uses-storage.ts', 'no-restricted-globals'],
    ['auth/uses-window-storage.ts', 'no-restricted-properties'],
    ['routes/uses-any.ts', '@typescript-eslint/no-explicit-any'],
    ['routes/uses-console.ts', 'no-console'],
  ])('catches the violation in %s', async (file, rule) => {
    expect(await ruleIds(file)).toContain(rule);
  });

  it('does not let an inline disable comment hide a violation', async () => {
    expect(await ruleIds('routes/disable-comment.ts')).toContain('@typescript-eslint/no-explicit-any');
  });
});
