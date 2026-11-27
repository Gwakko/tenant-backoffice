import { defineConfig, devices } from '@playwright/test';

import { e2eBaseURL } from './e2e/env';

const baseURL = e2eBaseURL();

// The scenarios change data (operations, moves, roles), so each one builds its own nodes and users
// and the suite runs in a single worker: the login limits of the API are per minute and per address.
export default defineConfig({
  testDir: './e2e',
  outputDir: 'test-results',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: true,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: [['list'], ['html', { outputFolder: 'playwright-report', open: 'never' }]],
  use: {
    baseURL,
    // Traces keep request headers (a short-lived access token of the demo stack): off unless asked for.
    trace: process.env.E2E_TRACE ? 'retain-on-failure' : 'off',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        channel: 'chromium',
      },
    },
  ],
});
