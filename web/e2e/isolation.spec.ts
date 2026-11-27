import { expect, seedNode, signInAs, test } from './fixtures';

test('[T-E-04] a user of one branch opening a node of another branch sees "Not found or no access"', async ({ page }) => {
  await signInAs(page, 'viewer.alpha');

  // Own branch first, so the test cannot pass just because every URL is refused.
  await page.goto(`/nodes/${seedNode('Alpha Retail Unit 1').id}`);
  await expect(page.getByRole('heading', { level: 1, name: 'Alpha Retail Unit 1' })).toBeVisible();

  const foreign = seedNode('Beta Retail');
  await page.goto(`/nodes/${foreign.id}`);
  await expect(page.getByRole('heading', { name: 'Not found or no access' })).toBeVisible();
  // Nothing about the foreign node is shown, not even its name; the page is the same for an id that does not exist.
  await expect(page.getByText(foreign.name)).toHaveCount(0);

  await page.goto('/nodes/00000000-0000-4000-8000-00000000dead');
  await expect(page.getByRole('heading', { name: 'Not found or no access' })).toBeVisible();
});
