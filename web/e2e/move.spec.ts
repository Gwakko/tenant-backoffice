import { expect, seedNode, signInAs, test } from './fixtures';

test('[T-E-02] owner moves a company to another partner through the dialog and the tree follows', async ({ page, owner }) => {
  const alpha = seedNode('Partner Alpha');
  const beta = seedNode('Partner Beta');
  const company = await owner.createNode(alpha.id, 'company', `E2E move ${Date.now().toString(36)}`);

  await signInAs(page, 'owner');
  await page.goto(`/nodes/${company.id}`);

  const tree = page.getByRole('tree', { name: 'Nodes' });
  const underAlpha = tree.getByRole('treeitem', { name: alpha.name, exact: true }).getByRole('treeitem', { name: company.name, exact: true });
  const underBeta = tree.getByRole('treeitem', { name: beta.name, exact: true }).getByRole('treeitem', { name: company.name, exact: true });
  await expect(underAlpha).toBeVisible();

  await page.getByRole('button', { name: 'Move', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Move node' });
  await dialog.getByLabel('Search new parent').fill(beta.name);
  const target = dialog.getByRole('radio', { name: new RegExp(beta.name) });
  await target.check();
  await expect(target).toBeChecked();
  await expect(dialog.getByText('Move under', { exact: true })).toBeVisible();
  await dialog.getByRole('button', { name: 'Move', exact: true }).click();
  await expect(dialog).toBeHidden();

  // The card, the breadcrumbs and the tree all show the new parent; the old one no longer lists the node.
  const breadcrumbs = page.getByRole('navigation', { name: 'Breadcrumbs' });
  await expect(breadcrumbs.getByRole('link', { name: beta.name })).toBeVisible();
  await expect(breadcrumbs.getByRole('link', { name: alpha.name })).toHaveCount(0);
  await expect(underAlpha).toHaveCount(0);
  // Partner Beta was collapsed (the owner came in through Partner Alpha); opening it lists the moved node.
  await tree.getByRole('button', { name: `Expand ${beta.name}` }).click();
  await expect(underBeta).toBeVisible();
});
