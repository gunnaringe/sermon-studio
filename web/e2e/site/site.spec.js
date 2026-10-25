// The website (site/), served as Cloudflare serves it (serve.js): every page in every language, the language
// menu, the phone's menu, the same keys in every language, every internal link and picture, and the video.
// Saves screenshots of the pages (web/e2e/screenshots/site-*.png) for the PR. Run with scripts/test.sh site.
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

const SITE = process.env.SITE || '/site';
const LANGS = ['en', 'nb', 'nn'];
const PAGES = ['/', '/guide.html', '/privacy.html'];
const dicts = Object.fromEntries(LANGS.map((l) => [l, JSON.parse(fs.readFileSync(path.join(SITE, 'lang', l + '.json'), 'utf8'))]));
const shots = path.join(import.meta.dirname, '..', 'screenshots');
const ORIGIN = 'http://127.0.0.1:8090';

// watch fails the test on a script error, a request to another host, a failed request or a CSP violation.
function watch(page) {
  const problems = [];
  page.on('pageerror', (e) => problems.push('error: ' + e.message));
  page.on('console', (m) => { if (m.type() === 'error') problems.push('console: ' + m.text()); });
  page.on('request', (r) => { if (!r.url().startsWith(ORIGIN) && !r.url().startsWith('data:')) problems.push('request to another host: ' + r.url()); });
  page.on('response', (r) => { if (r.status() >= 400) problems.push(`${r.status()}: ${r.url()}`); });
  return problems;
}
const ready = (page) => expect(page.locator('html.ready')).toHaveCount(1);
// choose picks a language in the header's menu (the globe).
async function choose(page, lang) {
  await page.locator('#lang summary').click();
  await page.locator(`#lang button[lang=${lang}]`).click();
  await expect(page.locator('#lang')).not.toHaveAttribute('open');
}
// eager loads the lazy pictures, so the page is whole.
async function eager(page) {
  await page.evaluate(() => document.querySelectorAll('img[loading=lazy]').forEach((i) => { i.loading = 'eager'; }));
  await page.waitForFunction(() => [...document.images].every((i) => i.complete && i.naturalWidth > 0));
}

test('every language has the same keys, and the pages use only those keys', () => {
  const keys = (l) => Object.keys(dicts[l]).sort();
  for (const l of LANGS) {
    expect(keys(l), `${l}.json`).toEqual(keys('en'));
    for (const [k, v] of Object.entries(dicts[l])) expect(v.trim(), `${l}: ${k}`).not.toBe('');
  }
  const used = new Set();
  for (const file of fs.readdirSync(SITE).filter((f) => f.endsWith('.html'))) {
    const html = fs.readFileSync(path.join(SITE, file), 'utf8');
    for (const m of html.matchAll(/data-t(?:-[a-z-]+)?="([^"]+)"/g)) {
      expect(dicts.en, `${file}: ${m[1]}`).toHaveProperty([m[1]]);
      used.add(m[1]);
    }
  }
  expect(Object.keys(dicts.en).filter((k) => !used.has(k)), 'keys no page uses').toEqual([]);
});

for (const lang of LANGS) {
  for (const p of PAGES) {
    test(`${p} in ${lang}`, async ({ page, request }) => {
      const problems = watch(page);
      await page.goto(p + '?lang=' + lang);
      await ready(page);
      await expect(page.locator('html')).toHaveAttribute('lang', lang);
      await expect(page.locator('#lang .code')).toHaveText(lang.toUpperCase());
      await expect(page.locator(`#lang button[lang=${lang}]`)).toHaveAttribute('aria-pressed', 'true');
      const h1 = page.locator('h1');
      await expect(h1).toBeVisible();
      expect((await h1.textContent()).trim()).not.toBe('');
      // Every text is filled in, from this language.
      for (const el of await page.locator('body [data-t]:not(noscript *)').all()) {
        const key = await el.getAttribute('data-t');
        expect((await el.innerHTML()).trim(), key).not.toBe('');
      }
      expect(await page.title()).toBe(dicts[lang][await page.locator('title').getAttribute('data-t')]);

      // Every picture is this language's, and loads.
      await eager(page);
      for (const src of await page.locator('img[data-shot]').evaluateAll((imgs) => imgs.map((i) => i.getAttribute('src')))) {
        expect(src).toMatch(new RegExp(`^img/shots/${lang}/[a-z-]+\\.webp$`));
      }
      // Every link: inside the site it resolves; outside it is GitHub, the studio, the author's page or the contact address.
      for (const href of new Set(await page.locator('a[href]').evaluateAll((as) => as.map((a) => a.getAttribute('href'))))) {
        if (/^mailto:/.test(href)) expect(href).toMatch(/^mailto:contact@sermon-studio\.app(\?subject=[^&]+)?$/);
        else if (/^https?:/.test(href)) expect(href).toMatch(/^https:\/\/(github\.com\/gunnaringe\/sermon-studio(\/|$)|my\.sermon-studio\.app\/$|gunnaringe\.sort\.land$)/);
        else {
          const url = new URL(href, page.url());
          expect((await request.get(url.pathname)).status(), href).toBe(200);
          if (url.hash && url.pathname === new URL(page.url()).pathname) await expect(page.locator(url.hash), href).toHaveCount(1);
        }
      }
      expect(problems).toEqual([]);
    });
  }
}

test('the language menu switches the page and is remembered', async ({ page }) => {
  const problems = watch(page);
  await page.goto('/');
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.en['home.h1']);
  await choose(page, 'nn');
  await expect(page.locator('h1')).toHaveText(dicts.nn['home.h1']);
  await expect(page.locator('html')).toHaveAttribute('lang', 'nn');
  await expect(page.locator('img[data-shot=editor]').first()).toHaveAttribute('src', 'img/shots/nn/editor.webp');
  await page.reload();
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.nn['home.h1']);
  await page.locator('nav a[href="guide.html"]').click();
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.nn['guide.h1']);
  // ?lang= is a choice too; the menu then takes it out of the address.
  await page.goto('/privacy.html?lang=nb');
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.nb['privacy.h1']);
  await choose(page, 'en');
  await expect(page).toHaveURL(/\/privacy\.html$/);
  await page.goto('/');
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.en['home.h1']);
  expect(problems).toEqual([]);
});

for (const [locale, lang] of [['nb-NO', 'nb'], ['no', 'nb'], ['nn-NO', 'nn'], ['en-GB', 'en'], ['de-DE', 'en']]) {
  test(`a browser in ${locale} gets ${lang} by itself`, async ({ browser }) => {
    const context = await browser.newContext({ locale });
    const page = await context.newPage();
    await page.goto(ORIGIN + '/');
    await ready(page);
    await expect(page.locator('html')).toHaveAttribute('lang', lang);
    await expect(page.locator('h1')).toHaveText(dicts[lang]['home.h1']);
    await context.close();
  });
}

test('the video has its sources and poster, and they are small', async ({ page, request }) => {
  await page.goto('/?lang=nb');
  await ready(page);
  const video = page.locator('video');
  await expect(video).toHaveAttribute('poster', 'media/demo-poster.webp');
  for (const attr of ['controls', 'muted', 'playsinline']) await expect(video).toHaveAttribute(attr, '');
  const sources = await video.locator('source').evaluateAll((s) => s.map((x) => [x.getAttribute('src'), x.getAttribute('type')]));
  expect(sources).toEqual([['media/demo.webm', 'video/webm'], ['media/demo.mp4', 'video/mp4']]);
  await expect(page.locator('#video .note')).toHaveText(dicts.nb['home.video.note']);
  let total = 0;
  for (const src of ['media/demo.webm', 'media/demo.mp4', 'media/demo-poster.webp']) {
    const r = await request.get(src);
    expect(r.status(), src).toBe(200);
    const size = (await r.body()).length;
    expect(size, src).toBeGreaterThan(10_000);
    total += size;
  }
  expect(total).toBeLessThan(10 * 1024 * 1024);
  // It plays (the browser here has no H.264, so WebM is the one it picks).
  await video.evaluate((v) => { v.preload = 'auto'; v.load(); });
  await expect.poll(() => video.evaluate((v) => v.readyState), { timeout: 15_000 }).toBeGreaterThanOrEqual(1);
  expect(await video.evaluate((v) => Math.round(v.duration))).toBeGreaterThanOrEqual(60);
});

test('the header has the sections and the guide; privacy and About are at the bottom', async ({ page }) => {
  const problems = watch(page);
  await page.goto('/?lang=nb');
  await ready(page);
  const nav = page.locator('header nav');
  for (const key of ['nav.how', 'nav.mixer', 'nav.churches', 'nav.self', 'nav.guide']) await expect(nav.getByRole('link', { name: dicts.nb[key] })).toBeVisible();
  await expect(nav.locator('a.page')).toHaveAttribute('href', 'guide.html');
  await expect(nav.locator('a.page')).toHaveAttribute('title', dicts.nb['nav.guide.title']);
  await expect(page.locator('header a[href="privacy.html"]')).toHaveCount(0);
  const about = page.locator('footer');
  await expect(about.getByRole('heading', { name: dicts.nb['about.h2'] })).toBeVisible();
  await expect(about.getByRole('link', { name: 'Gunnar Inge Gjøvik Sortland' })).toHaveAttribute('href', 'https://gunnaringe.sort.land');
  await expect(about.getByRole('link', { name: dicts.nb['nav.privacy'] })).toHaveAttribute('href', 'privacy.html');
  // A section's link scrolls to it, and is marked while it is in view.
  await nav.getByRole('link', { name: dicts.nb['nav.self'] }).click();
  await expect(page).toHaveURL(/#self$/);
  await expect.poll(() => page.locator('#self').evaluate((s) => Math.round(s.getBoundingClientRect().top))).toBeLessThan(120);
  await expect(nav.getByRole('link', { name: dicts.nb['nav.self'] })).toHaveAttribute('aria-current', 'location');
  await expect(nav.locator('a[aria-current]')).toHaveCount(1);
  await nav.getByRole('link', { name: dicts.nb['nav.how'] }).click();
  await expect(nav.getByRole('link', { name: dicts.nb['nav.how'] })).toHaveAttribute('aria-current', 'location');
  // From another page, a section's link goes to it on the front page.
  await nav.locator('a.page').click();
  await ready(page);
  await expect(nav.locator('a.page')).toHaveAttribute('aria-current', 'page');
  await nav.getByRole('link', { name: dicts.nb['nav.mixer'] }).click();
  await expect(page).toHaveURL(/\/#mixer$/);
  await ready(page);
  await expect(page.locator('#mixer h2')).toBeInViewport();
  expect(problems).toEqual([]);
});

test('on a tablet the sections are behind the menu button', async ({ browser }) => {
  const tablet = await browser.newContext({ viewport: { width: 820, height: 1100 } });
  const page = await tablet.newPage();
  await page.goto(ORIGIN + '/?lang=nb');
  await ready(page);
  const nav = page.locator('header nav');
  await expect(nav).toBeHidden();
  await page.getByRole('button', { name: dicts.nb['nav.menu'] }).click();
  await nav.getByRole('link', { name: dicts.nb['nav.churches'] }).click();
  await expect(nav).toBeHidden(); // a choice closes the menu
  await expect(page.locator('#churches h2')).toBeInViewport();
  await tablet.close();
});

test('on a phone the header is one line, with the pages behind the menu button', async ({ browser }) => {
  const phone = await browser.newContext({ viewport: { width: 360, height: 740 }, isMobile: true, hasTouch: true });
  const page = await phone.newPage();
  const problems = watch(page);
  await page.goto(ORIGIN + '/?lang=nb');
  await ready(page);
  const bar = await page.locator('header').boundingBox();
  expect(bar.height).toBeLessThan(64);
  // Log in is a person only, still named for screen readers.
  const login = page.locator('header').getByRole('link', { name: dicts.nb['nav.login'] });
  await expect(login).toBeVisible();
  await expect(login.locator('span')).toBeHidden();
  expect((await login.boundingBox()).width).toBeLessThan(48);
  const nav = page.locator('header nav');
  await expect(nav).toBeHidden();
  const button = page.getByRole('button', { name: dicts.nb['nav.menu'] });
  await button.click();
  await expect(button).toHaveAttribute('aria-expanded', 'true');
  await expect(nav.getByRole('link', { name: dicts.nb['nav.mixer'] })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(nav).toBeHidden();
  // Nothing is wider than the screen.
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(360);
  await button.click();
  await nav.getByRole('link', { name: dicts.nb['nav.guide'] }).click();
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.nb['guide.h1']);
  expect(problems).toEqual([]);
  await phone.close();
});

test('an unknown address is the not-found page, and the Worker files are not served', async ({ page, request }) => {
  const r = await page.goto('/no/such/page');
  expect(r.status()).toBe(404);
  await ready(page);
  await expect(page.locator('h1')).not.toHaveText('');
  for (const f of ['/wrangler.jsonc', '/README.md', '/.assetsignore', '/_headers']) expect((await request.get(f)).status(), f).toBe(404);
  expect((await request.get('/guide')).status()).toBe(200);
});

test('screenshots for the PR', async ({ browser }) => {
  fs.mkdirSync(shots, { recursive: true });
  for (const lang of LANGS) {
    const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
    const page = await context.newPage();
    await page.goto(`${ORIGIN}/?lang=${lang}`);
    await ready(page);
    await eager(page);
    await page.screenshot({ path: path.join(shots, `site-home-${lang}.png`) });
    await page.locator('#churches').screenshot({ path: path.join(shots, `site-churches-${lang}.png`) });
    await page.locator('#mixer').screenshot({ path: path.join(shots, `site-mixer-${lang}.png`) });
    await page.locator('#self').screenshot({ path: path.join(shots, `site-self-${lang}.png`) });
    await page.locator('footer').screenshot({ path: path.join(shots, `site-about-${lang}.png`) });
    if (lang === 'nb') {
      await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
      await page.locator('#lang summary').click();
      await page.screenshot({ path: path.join(shots, 'site-lang-menu.png'), clip: { x: 0, y: 0, width: 1280, height: 320 } });
      await page.keyboard.press('Escape');
    }
    if (lang === 'en') {
      await page.screenshot({ path: path.join(shots, 'site-home-full.png'), fullPage: true });
      for (const [p, name] of [['guide.html', 'site-guide'], ['privacy.html', 'site-privacy']]) {
        await page.goto(`${ORIGIN}/${p}`);
        await ready(page);
        await eager(page);
        await page.screenshot({ path: path.join(shots, name + '.png'), fullPage: name === 'site-privacy' });
      }
    }
    await context.close();
  }
  for (const scheme of ['light', 'dark']) {
    const phone = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 1, isMobile: true, hasTouch: true, colorScheme: scheme });
    const page = await phone.newPage();
    await page.goto(`${ORIGIN}/?lang=nb`);
    await ready(page);
    await eager(page);
    await page.screenshot({ path: path.join(shots, `site-phone-${scheme}.png`) });
    if (scheme === 'light') {
      await page.locator('.menu-button').click();
      await page.screenshot({ path: path.join(shots, 'site-phone-menu.png') });
      await page.goto(`${ORIGIN}/privacy.html?lang=nb`);
      await ready(page);
      await page.screenshot({ path: path.join(shots, 'site-phone-privacy.png'), fullPage: true });
    }
    await phone.close();
  }
});
