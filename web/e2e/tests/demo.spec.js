// A demo (--no-login --visitor-role demo, issue #118): a second server that run.sh starts at :8082, set up as the
// deployment does it, with a church folder and a recording put in its inbox. A visitor edits and makes the files,
// and sees nothing they may not use.
import { test, expect } from '@playwright/test';
import path from 'node:path';

const shots = path.join(import.meta.dirname, '..', 'screenshots');
const DEMO = 'http://localhost:8082/';

test.use({ baseURL: DEMO });

test('a demo: the recording from the inbox, a banner, and no upload, delete, shares or church settings', async ({ page }) => {
  await page.goto('');
  await expect(page).toHaveURL(/c\/demo-church\/$/);
  const banner = page.getByRole('note');
  await expect(banner).toContainText('This is a demo. Anyone can see your changes, and everything is reset every night.');
  await expect(banner.getByRole('link', { name: 'About Sermon Studio' })).toHaveAttribute('href', 'https://sermon-studio.app/');
  // The inbox files the recording under the date in its name (it looks every 10 seconds).
  await expect.poll(async () => JSON.stringify(await (await page.request.get(DEMO + 'api/recordings?tenant=demo-church')).json()),
    { timeout: 60_000, intervals: [1000] }).toContain('2026-01-15');
  await page.reload();
  await expect(page.getByText('2026-01-15').first()).toBeVisible();
  await expect(page.getByText('Upload a recording:')).toHaveCount(0);
  await expect(page.locator('[data-nav=shares]')).toBeHidden();
  await expect(page.locator('#church')).toBeHidden(); // one church, no adding: just its name
  await expect(page.locator('#church-name')).toHaveText('Demo');
  await page.screenshot({ path: path.join(shots, 'demo-recordings.png') });

  await page.goto('c/demo-church/recordings/2026-01-15/edit');
  await expect(page.locator('#waveform')).toBeVisible({ timeout: 60_000 });
  await page.waitForFunction(() => window.__editor && window.__editor.ws.getDuration() > 0, null, { timeout: 60_000 });
  await expect(page.getByRole('heading', { name: 'Delete', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Delete original…' })).toHaveCount(0);
  await expect(page.locator('select[name=intro] option', { hasText: 'Upload for this sermon only…' })).toHaveCount(0);
  // The visitor saves and makes the files.
  await page.getByLabel('Section from').fill('0:03');
  await page.getByLabel('Section from').press('Enter');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Done' })).toBeVisible({ timeout: 60_000 });
  await page.screenshot({ path: path.join(shots, 'demo-editor.png') });

  // Settings: only this browser's.
  await page.goto('c/demo-church/settings');
  await expect(page.getByRole('heading', { name: 'This browser' })).toBeVisible();
  await expect(page.locator('details.settings-group')).toHaveCount(1);

  // The server says no, whatever the page shows.
  for (const [method, url] of [['POST', 'api/upload'], ['POST', 'api/recordings/delete'], ['POST', 'api/tenants'], ['PUT', 'api/settings'], ['GET', 'api/shares']]) {
    const res = await page.request.fetch(DEMO + url + '?tenant=demo-church', { method, data: {} });
    expect([403, 404], `${method} ${url}`).toContain(res.status());
  }
});
