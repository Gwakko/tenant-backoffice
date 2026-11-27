import { z } from 'zod';

import { expect, seedAccount, signIn, test } from './fixtures';

const loginBody = z.object({ accessToken: z.string().min(1) });

test('[T-E-01] admin signs in, opens a node from the tree, and the page keeps no token in storage', async ({ page }) => {
  const admin = seedAccount('admin.alpha');

  const loginResponse = page.waitForResponse(
    (response) => response.url().endsWith('/api/v1/auth/login') && response.request().method() === 'POST',
  );
  await signIn(page, admin);
  const { accessToken } = loginBody.parse(await (await loginResponse).json());

  const tree = page.getByRole('tree', { name: 'Nodes' });
  await expect(tree.getByRole('treeitem', { name: 'Partner Alpha', exact: true })).toBeVisible();
  await tree.getByRole('button', { name: 'Expand Partner Alpha' }).click();
  await tree.getByRole('link', { name: 'Alpha Retail', exact: true }).click();

  await expect(page.getByRole('heading', { level: 1, name: 'Alpha Retail' })).toBeVisible();
  await expect(page.getByRole('term').filter({ hasText: 'Type' })).toBeVisible();
  await expect(page.getByRole('definition').filter({ hasText: 'company' })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Balance' })).toBeVisible();

  // The access token lives in memory; the refresh token is an HttpOnly cookie that scripts cannot read.
  const stored = await page.evaluate(() =>
    JSON.stringify([Object.entries(localStorage), Object.entries(sessionStorage), document.cookie]),
  );
  expect(stored.includes(accessToken), 'the access token must not be in localStorage, sessionStorage or document.cookie').toBe(false);
  expect(stored).not.toMatch(/token|jwt|bearer/i);
});
