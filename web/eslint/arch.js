// Architecture rules for the web client (T-ARCH-04), kept apart from eslint.config.js so a
// self-test can run them against fixture trees (eslint/arch.test.ts).
import fs from 'node:fs';
import path from 'node:path';

import tseslint from 'typescript-eslint';

const srcGlob = (_root, sub) => `src/${sub}`;

const layerMessage = (from, to) => `${from} must not import ${to} (web layer rules, T-ARCH-04).`;

/**
 * @param {string} root directory that holds `src/` (the web package, or a fixture tree)
 */
export function archConfig(root) {
  const featuresDir = path.join(root, 'src', 'features');
  const features = fs.existsSync(featuresDir)
    ? fs
        .readdirSync(featuresDir, { withFileTypes: true })
        .filter((d) => d.isDirectory())
        .map((d) => d.name)
    : [];

  const parentRelative = { group: ['../*', '../**'], message: 'Use the @/ alias instead of parent-relative imports.' };
  const reactGroup = ['react', 'react-dom', 'react-dom/*', '@tanstack/react-*', 'react-hook-form'];

  return [
    {
      basePath: root,
      // Inline comments cannot switch rules off (checked together with a grep in make lint-web).
      linterOptions: { noInlineConfig: true, reportUnusedDisableDirectives: 'off' },
    },
    {
      basePath: root,
      files: [srcGlob(root, '**/*.{ts,tsx}')],
      languageOptions: { parser: tseslint.parser },
      plugins: { '@typescript-eslint': tseslint.plugin },
      rules: {
        '@typescript-eslint/no-explicit-any': 'error',
        'no-restricted-imports': ['error', { patterns: [parentRelative] }],
        // Server text is rendered as text only: no raw HTML sinks.
        'no-restricted-syntax': [
          'error',
          {
            selector: "JSXAttribute[name.name='dangerouslySetInnerHTML']",
            message: 'dangerouslySetInnerHTML is forbidden; render text.',
          },
          {
            selector: "AssignmentExpression[left.property.name=/^(innerHTML|outerHTML)$/]",
            message: 'Assigning innerHTML/outerHTML is forbidden; render text.',
          },
        ],
      },
    },
    {
      basePath: root,
      // No console output in production code.
      files: [srcGlob(root, '**/*.{ts,tsx}')],
      ignores: [srcGlob(root, '**/*.test.{ts,tsx}'), srcGlob(root, 'test/**')],
      rules: { 'no-console': 'error' },
    },
    {
      basePath: root,
      // The access token lives in memory only.
      files: [srcGlob(root, 'auth/**/*.{ts,tsx}'), srcGlob(root, 'api/**/*.{ts,tsx}')],
      ignores: [srcGlob(root, '**/*.test.{ts,tsx}')],
      rules: {
        'no-restricted-globals': [
          'error',
          { name: 'localStorage', message: 'Tokens are kept in memory only.' },
          { name: 'sessionStorage', message: 'Tokens are kept in memory only.' },
          { name: 'indexedDB', message: 'Tokens are kept in memory only.' },
        ],
        'no-restricted-properties': [
          'error',
          { object: 'window', property: 'localStorage', message: 'Tokens are kept in memory only.' },
          { object: 'window', property: 'sessionStorage', message: 'Tokens are kept in memory only.' },
        ],
      },
    },
    {
      basePath: root,
      // api: transport only. No React, no UI, no routing, no features.
      files: [srcGlob(root, 'api/**/*.{ts,tsx}')],
      ignores: [srcGlob(root, '**/*.test.{ts,tsx}')],
      rules: {
        'no-restricted-imports': [
          'error',
          {
            patterns: [
              parentRelative,
              { group: reactGroup, message: layerMessage('api', 'React') },
              { group: ['@/features/**', '@/routes/**', '@/components/**', '@/auth/**'], message: layerMessage('api', 'higher layers') },
            ],
          },
        ],
      },
    },
    {
      basePath: root,
      // lib: pure helpers with no framework or app dependencies.
      files: [srcGlob(root, 'lib/**/*.{ts,tsx}')],
      ignores: [srcGlob(root, '**/*.test.{ts,tsx}')],
      rules: {
        'no-restricted-imports': [
          'error',
          {
            patterns: [
              parentRelative,
              { group: reactGroup, message: layerMessage('lib', 'React') },
              { group: ['@/api/**', '@/auth/**', '@/features/**', '@/routes/**', '@/components/**'], message: layerMessage('lib', 'app layers') },
            ],
          },
        ],
      },
    },
    {
      basePath: root,
      // components/ui: presentational; no data access, no features, no routes.
      files: [srcGlob(root, 'components/**/*.{ts,tsx}')],
      ignores: [srcGlob(root, '**/*.test.{ts,tsx}')],
      rules: {
        'no-restricted-imports': [
          'error',
          {
            patterns: [
              parentRelative,
              { group: ['@/api/**', '@/auth/**', '@/features/**', '@/routes/**'], message: layerMessage('components', 'data, features or routes') },
            ],
          },
        ],
      },
    },
    {
      basePath: root,
      // auth: may use api, nothing above it.
      files: [srcGlob(root, 'auth/**/*.{ts,tsx}')],
      ignores: [srcGlob(root, '**/*.test.{ts,tsx}')],
      rules: {
        'no-restricted-imports': [
          'error',
          {
            patterns: [
              parentRelative,
              { group: ['@/features/**', '@/routes/**', '@/components/**'], message: layerMessage('auth', 'features, routes or components') },
            ],
          },
        ],
      },
    },
    // features: one block per feature; a feature must not import a sibling feature or routes.
    ...features.map((name) => ({
      basePath: root,
      files: [srcGlob(root, `features/${name}/**/*.{ts,tsx}`)],
      ignores: [srcGlob(root, '**/*.test.{ts,tsx}')],
      rules: {
        'no-restricted-imports': [
          'error',
          {
            patterns: [
              parentRelative,
              {
                group: ['@/features/**', `!@/features/${name}`, `!@/features/${name}/**`],
                message: layerMessage(`feature ${name}`, 'another feature'),
              },
              { group: ['@/routes/**'], message: layerMessage(`feature ${name}`, 'routes') },
            ],
          },
        ],
      },
    })),
  ];
}
