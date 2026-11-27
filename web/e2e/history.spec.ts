import { expect, seedNode, signInAs, test } from './fixtures';

function utcDaysAgo(days: number): string {
  const date = new Date(Date.now() - days * 86_400_000);
  return date.toISOString().slice(0, 10);
}

test('[T-E-06] history filters and report settings live in the URL and the browser back button restores them', async ({ page }) => {
  await signInAs(page, 'admin.alpha');
  await page.goto(`/nodes/${seedNode('Alpha Retail').id}/operations`);

  const from = utcDaysAgo(45);
  await page.getByLabel('Kind').selectOption({ label: 'Debit' });
  await expect(page).toHaveURL(/kind=debit/);
  await page.getByLabel('From (UTC date)').fill(from);
  await expect(page).toHaveURL(new RegExp(`from=${from}`));

  const kindCells = page.getByRole('table', { name: 'Operations' }).locator('tbody tr td:nth-child(3)');
  await expect(kindCells.first()).toBeVisible();
  const onlyDebits = async () => {
    const kinds = await kindCells.allTextContents();
    expect(kinds.length).toBeGreaterThan(0);
    for (const kind of kinds) expect(kind).toBe('Debit');
  };
  await onlyDebits();

  // Reports: another screen with its own parameters, applied through the form.
  const tabs = page.getByRole('navigation', { name: 'Node sections' });
  await tabs.getByRole('link', { name: 'Reports' }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Reports' })).toBeVisible();
  await expect(page.getByLabel('Granularity')).toHaveValue('day');
  await page.getByLabel('Granularity').selectOption('month');
  await page.getByRole('button', { name: 'Apply' }).click();
  await expect(page).toHaveURL(/granularity=month/);
  await expect(page.getByRole('term').filter({ hasText: 'Operations' }).first()).toBeVisible();

  // Back: the report returns to its previous settings, then the history to its filters.
  await page.goBack();
  await expect(page).not.toHaveURL(/granularity=month/);
  await expect(page.getByLabel('Granularity')).toHaveValue('day');
  await page.goBack();
  await expect(page).toHaveURL(/\/operations\?/);
  await expect(page).toHaveURL(/kind=debit/);
  await expect(page.getByLabel('Kind')).toHaveValue('debit');
  await expect(page.getByLabel('From (UTC date)')).toHaveValue(from);
  await onlyDebits();

  // Forward again restores the report settings too.
  await page.goForward();
  await page.goForward();
  await expect(page.getByLabel('Granularity')).toHaveValue('month');
});
