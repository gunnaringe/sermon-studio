// Settings → Storage: what the church keeps, by kind, against its quota.
import { test, expect } from '@playwright/test';
import path from 'node:path';

const shots = path.join(import.meta.dirname, '..', 'screenshots');

test('storage: what the church keeps, without a quota and close to one', async ({ page }) => {
  // As it is: the test recordings, and no quota.
  await page.goto('settings/storage');
  const box = page.locator('#storage');
  await expect(box.locator('.storage-total')).toHaveText(/^\d+(\.\d)? (kB|MB|GB) used$/);
  await expect(box).toContainText('The church has no storage limit.');
  await expect(box.locator('meter')).toHaveCount(0);
  await expect(page.locator('[data-group="church"] summary')).toContainText('Name, logo and storage');

  // Close to its quota: the meter and a warning.
  const GB = 1 << 30;
  await page.route('**/api/storage**', (r) => r.fulfill({ json: {
    use: { recordings: 31.4 * GB, files: 9.6 * GB, clips: 0.05 * GB, trash: 5.2 * GB },
    total: 46.25 * GB, quota: 50 * GB, measured: true,
  } }));
  await page.reload();
  await expect(box.locator('.storage-total')).toHaveText('46.3 GB of 50.0 GB used');
  await expect(box.locator('meter')).toHaveAttribute('value', '0.925');
  await expect(box.getByRole('status')).toHaveText('The church has used 92 % of its storage. Delete recordings or files that are not needed, and empty the trash.');
  await expect(box.locator('dd')).toHaveText(['31.4 GB', '9.6 GB', '51.2 MB', '5.2 GB']);
  await page.setViewportSize({ width: 1280, height: 1600 }); // the whole section in the settings' scroll box
  await box.scrollIntoViewIfNeeded();
  await box.screenshot({ path: path.join(shots, 'settings-storage.png') });
});

// A member sees no storage: it is with the church's settings.
test('storage is the admins\'', async ({ page }) => {
  let asked = false;
  await page.route('**/api/storage**', (r) => { asked = true; return r.fulfill({ status: 403, json: { error: 'forbidden' } }); });
  await page.route('**/api/tenants', async (r) => {
    const json = await (await r.fetch()).json();
    for (const c of json.tenants) c.actions = c.actions.filter((a) => a !== 'church.settings' && a !== 'church.clips');
    return r.fulfill({ json });
  });
  await page.goto('settings/trash');
  await expect(page.getByRole('heading', { name: 'Trash' })).toBeVisible();
  await expect(page.locator('#storage')).toHaveCount(0);
  expect(asked).toBe(false);
});
