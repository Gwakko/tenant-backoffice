import { expect, seedNode, signIn, test } from './fixtures';
import type { Page } from '@playwright/test';

async function operate(page: Page, kind: 'Credit' | 'Debit', amount: string, comment: string, doubleClick = false) {
  await page.getByRole('button', { name: kind, exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel(/^Amount/).fill(amount);
  await dialog.getByLabel('Comment').fill(comment);
  const confirm = dialog.getByRole('button', { name: `Confirm ${kind.toLowerCase()}` });
  if (doubleClick) await confirm.dblclick();
  else await confirm.click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText(`${kind} completed.`)).toBeVisible();
}

test('[T-E-03] credit and debit change the balance, and a double click on confirm makes one operation', async ({ page, owner, provision }) => {
  const { node, account } = await provision(seedNode('Alpha Retail'), 'unit', 'admin');
  await signIn(page, account);
  await page.goto(`/nodes/${node.id}`);

  const balance = page.getByTestId('balance-amount');
  await expect(balance).toContainText('0.00');

  await operate(page, 'Credit', '100.00', 'e2e credit');
  await expect(balance).toContainText('100.00');
  await operate(page, 'Debit', '30.00', 'e2e debit');
  await expect(balance).toContainText('70.00');

  // Two clicks in a row on "Confirm debit": exactly one operation of 10.00, not two.
  await operate(page, 'Debit', '10.00', 'e2e double click', true);
  await expect(balance).toContainText('60.00');
  expect(await owner.balance(node.id), 'balance in minor units').toBe('6000');

  await page.getByRole('navigation', { name: 'Node sections' }).getByRole('link', { name: 'Operations' }).click();
  const table = page.getByRole('table', { name: 'Operations' });
  await expect(table.getByRole('row').filter({ hasText: 'e2e double click' })).toHaveCount(1);
  await expect(table.getByRole('row').filter({ hasText: 'e2e credit' })).toHaveCount(1);
  await expect(table.getByRole('row').filter({ hasText: 'e2e debit' })).toHaveCount(1);
  // Header row plus three operations, nothing else.
  await expect(table.getByRole('row')).toHaveCount(4);
});
