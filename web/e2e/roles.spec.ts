import { expect, seedNode, signIn, signInAs, test } from './fixtures';

test('[T-E-05] after the owner lowers an admin to viewer, the other browser loses its buttons and gets a 403 message', async ({
  page,
  openSession,
  provision,
}) => {
  const { node, account } = await provision(seedNode('Partner Alpha'), 'company', 'admin');

  // Browser 1: the owner. Browser 2: the admin, a separate context with its own cookies and storage.
  await signInAs(page, 'owner');
  const second = await openSession();
  await signIn(second.page, account);
  await second.page.goto(`/nodes/${node.id}`);

  const credit = second.page.getByRole('button', { name: 'Credit', exact: true });
  const rename = second.page.getByRole('button', { name: 'Rename', exact: true });
  await expect(credit).toBeVisible();
  await expect(rename).toBeVisible();
  // The dialog is open while the role changes, so the page still holds the old permissions.
  await credit.click();
  const dialog = second.page.getByRole('dialog');
  await expect(dialog).toBeVisible();

  await page.goto(`/nodes/${node.id}/members`);
  await page.getByLabel(`Role of ${account.email}`).selectOption('viewer');
  await page.getByRole('button', { name: 'Save role' }).click();
  await expect(page.getByRole('button', { name: 'Save role' })).toBeDisabled();
  await expect(page.getByText('viewer').first()).toBeVisible();

  // The mutation with the lowered role is refused by the server and the UI says so.
  await dialog.getByLabel(/^Amount/).fill('1.00');
  await dialog.getByRole('button', { name: 'Confirm credit' }).click();
  await expect(dialog.getByText('You do not have permission for this action.')).toBeVisible();
  await dialog.getByRole('button', { name: 'Cancel' }).click();

  // The 403 makes the page read the permissions again: the buttons are gone, the read-only parts stay.
  await expect(credit).toHaveCount(0);
  await expect(rename).toHaveCount(0);
  await expect(second.page.getByRole('heading', { level: 1, name: node.name })).toBeVisible();

  await second.page.reload();
  await expect(second.page.getByRole('heading', { level: 1, name: node.name })).toBeVisible();
  await expect(second.page.getByRole('button', { name: 'Credit', exact: true })).toHaveCount(0);
  await expect(second.page.getByRole('button', { name: 'Rename', exact: true })).toHaveCount(0);
});
