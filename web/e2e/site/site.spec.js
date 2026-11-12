// The website (site/), served as Cloudflare serves it (serve.js): that its pages are what site/src makes,
// every page in every language (also without JavaScript), the language
// menu, the phone's menu, the same keys in every language, every internal link and picture, and the video.
// Saves screenshots of the pages (web/e2e/screenshots/site-*.png) for the PR. Run with scripts/test.sh site.
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

const SITE = process.env.SITE || '/site';
const LANGS = ['en', 'nb', 'nn'];
const PAGES = ['/', '/guide.html', '/privacy.html'];
const dicts = Object.fromEntries(LANGS.map((l) => [l, JSON.parse(fs.readFileSync(path.join(SITE, 'src', 'lang', l + '.json'), 'utf8'))]));
// at is a page's address in a language: /guide.html is /nb/guide in bokmål (as src/build.mjs has it).
const at = (lang, p) => (lang === 'en' ? '' : '/' + lang) + (p === '/' ? '/' : p.replace(/\.html$/, ''));
const { stale } = await import(path.join(SITE, 'src', 'build.mjs'));
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
  await page.locator(`#lang a[lang=${lang}]`).click();
  await expect(page.locator('html')).toHaveAttribute('lang', lang);
  await ready(page);
}
// eager loads the lazy pictures, so the page is whole.
async function eager(page) {
  await page.evaluate(() => document.querySelectorAll('img[loading=lazy]').forEach((i) => { i.loading = 'eager'; }));
  // (The picture in the dialog has no address until a click gives it one.)
  await page.waitForFunction(() => [...document.images].filter((i) => i.hasAttribute('src')).every((i) => i.complete && i.naturalWidth > 0));
}

test('every language has the same keys, and the pages use only those keys', () => {
  const keys = (l) => Object.keys(dicts[l]).sort();
  for (const l of LANGS) {
    expect(keys(l), `${l}.json`).toEqual(keys('en'));
    for (const [k, v] of Object.entries(dicts[l])) expect(v.trim(), `${l}: ${k}`).not.toBe('');
  }
  const used = new Set();
  for (const file of fs.readdirSync(path.join(SITE, 'src')).filter((f) => f.endsWith('.html'))) {
    const html = fs.readFileSync(path.join(SITE, 'src', file), 'utf8');
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
      await expect(page).toHaveURL(ORIGIN + at(lang, p)); // ?lang= goes to the language's own page
      await expect(page.locator(`#lang a[lang=${lang}]`)).toHaveAttribute('aria-current', 'true');
      const h1 = page.locator('h1');
      await expect(h1).toBeVisible();
      expect((await h1.textContent()).trim()).not.toBe('');
      expect(await page.title()).toBe(dicts[lang][(p === '/' ? 'home' : p.slice(1, -5)) + '.title']);
      await expect(page.locator('[data-t], [data-t-alt], [data-t-title]')).toHaveCount(0); // the templates' marks are gone

      // Every picture is this language's, and loads.
      await eager(page);
      for (const src of await page.locator('img[data-shot]').evaluateAll((imgs) => imgs.map((i) => i.getAttribute('src')))) {
        expect(src).toMatch(new RegExp(`^/img/shots/${lang}/[a-z-]+\\.webp$`));
      }
      // Every link: inside the site it resolves; outside it is GitHub, the studio, the demo, the author's page or the contact address.
      for (const href of new Set(await page.locator('a[href]').evaluateAll((as) => as.map((a) => a.getAttribute('href'))))) {
        if (/^mailto:/.test(href)) expect(href).toMatch(/^mailto:contact@sermon-studio\.app(\?subject=[^&]+)?$/);
        else if (/^https?:/.test(href)) expect(href).toMatch(/^https:\/\/(github\.com\/gunnaringe\/sermon-studio(\/|$)|(my|demo)\.sermon-studio\.app\/$|gunnaringe\.sort\.land$)/);
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
  await expect(page.locator('img[data-shot=editor]').first()).toHaveAttribute('src', '/img/shots/nn/editor.webp');
  await expect(page).toHaveURL(ORIGIN + '/nn/');
  await page.reload();
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.nn['home.h1']);
  await page.locator('nav a[href="/nn/guide"]').click();
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.nn['guide.h1']);
  // ?lang= is a choice too; the menu then takes it out of the address.
  await page.goto('/privacy.html?lang=nb');
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.nb['privacy.h1']);
  await choose(page, 'en');
  await expect(page).toHaveURL(ORIGIN + '/privacy');
  // A page asked for in a language stays in it, whatever was chosen before.
  await page.goto('/nn/guide');
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.nn['guide.h1']);
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

test('a picture of the studio has a phone version, and a click shows it larger', async ({ page, browser }) => {
  await page.goto('/?lang=nb');
  await ready(page);
  const first = page.locator('#how button.zoom').first();
  await expect(first).toHaveAttribute('title', dicts.nb['shot.enlarge']);
  await expect(first.locator('img')).toHaveAttribute('src', '/img/shots/nb/recordings.webp');
  // Shown beside the text, it is not shrunk to less than half its size (its text stays readable).
  const scale = await first.locator('img').evaluate((i) => i.getBoundingClientRect().width / 900);
  expect(scale).toBeGreaterThan(0.6);
  const dialog = page.locator('dialog#zoom');
  await expect(dialog).toBeHidden();
  await first.click();
  await expect(dialog).toBeVisible();
  await expect(dialog.locator('img')).toHaveAttribute('src', /img\/shots\/nb\/recordings\.webp$/);
  await expect(dialog.locator('img')).toHaveAttribute('alt', dicts.nb['home.step1.alt']);
  await expect(dialog.getByRole('button', { name: dicts.nb['shot.close'] })).toBeVisible();
  await page.screenshot({ path: path.join(shots, 'site-zoom.png') });
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await first.click();
  await dialog.getByRole('button', { name: dicts.nb['shot.close'] }).click();
  await expect(dialog).toBeHidden();
  // Every picture has its phone version, and a phone gets that one.
  for (const p of ['/', '/guide.html']) {
    await page.goto(p + '?lang=nb');
    await ready(page);
    const sources = await page.locator('button.zoom source').evaluateAll((s) => s.map((x) => [x.getAttribute('srcset'), x.parentElement.querySelector('img').getAttribute('src')]));
    expect(sources.length).toBeGreaterThan(5);
    for (const [phone, wide] of sources) {
      expect(phone).toBe(wide.replace(/\.webp$/, '-phone.webp'));
      expect((await page.request.get(phone)).ok(), phone).toBe(true);
    }
  }
  const context = await browser.newContext({ viewport: { width: 390, height: 800 }, baseURL: new URL(page.url()).origin });
  const phone = await context.newPage();
  await phone.goto('/?lang=nb');
  await ready(phone);
  const img = phone.locator('#how button.zoom img').nth(1);
  await img.scrollIntoViewIfNeeded();
  await expect.poll(() => img.evaluate((i) => i.currentSrc)).toMatch(/img\/shots\/nb\/editor-phone\.webp$/);
  await phone.screenshot({ path: path.join(shots, 'site-phone-step.png') });
  await context.close();
});

test('What it does has six cards, transcripts and sharing among them', async ({ page }) => {
  for (const lang of LANGS) {
    await page.goto('/?lang=' + lang);
    await ready(page);
    const cards = page.locator('#what .features li');
    await expect(cards).toHaveCount(6);
    for (const n of [5, 6]) await expect(cards.nth(n - 1)).toHaveText(dicts[lang]['home.what.' + n].replace(/<[^>]+>/g, ''));
  }
  expect(dicts.en['home.what.5']).toMatch(/Transcripts.*text and captions/);
});

test('the front page goes from how it works to getting it, then the demo and running it yourself', async ({ page }) => {
  await page.goto('/?lang=en');
  await ready(page);
  expect(await page.locator('main > section').evaluateAll((s) => s.map((x) => x.id || x.className)))
    .toEqual(['hero', 'how', 'what', 'mixer', 'churches', 'demo', 'self']);
});

test('the video has its sources and poster, and they are small', async ({ page, request }) => {
  await page.goto('/?lang=nb');
  await ready(page);
  const video = page.locator('video');
  await expect(video).toHaveAttribute('poster', '/media/demo-poster.webp');
  for (const attr of ['controls', 'playsinline']) await expect(video).toHaveAttribute(attr, '');
  await expect(video).not.toHaveAttribute('muted'); // it has sound, and starts only when asked to
  const sources = await video.locator('source').evaluateAll((s) => s.map((x) => [x.getAttribute('src'), x.getAttribute('type')]));
  expect(sources).toEqual([['/media/demo.webm', 'video/webm'], ['/media/demo.mp4', 'video/mp4']]);
  await expect(page.locator('#video .note')).toHaveText(dicts.nb['home.video.note']);
  // Subtitles in bokmål and nynorsk; a page in one of them has its own on from the start.
  const tracks = () => video.locator('track').evaluateAll((t) => t.map((x) => [x.getAttribute('srclang'), x.getAttribute('src'), x.hasAttribute('default')]));
  expect(await tracks()).toEqual([['nb', '/media/demo.nb.vtt', true], ['nn', '/media/demo.nn.vtt', false]]);
  for (const lang of ['nb', 'nn']) {
    const vtt = await (await request.get(`/media/demo.${lang}.vtt`)).text();
    expect(vtt.startsWith('WEBVTT\n'), lang).toBe(true);
    const cues = [...vtt.matchAll(/(\d\d):(\d\d):(\d\d)\.\d{3} --> (\d\d):(\d\d):(\d\d)\.\d{3}\n(.+)/g)];
    expect(cues.length, lang).toBeGreaterThan(15);
    expect(cues[0][7]).toBe('Sermon Studio');
    expect(vtt).toContain(lang === 'nb' ? 'Merk prekenen' : 'Merk preika');
  }
  await page.goto('/?lang=en');
  await ready(page);
  expect((await tracks()).map((t) => t[2])).toEqual([false, false]);
  await page.goto('/?lang=nb');
  await ready(page);
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

// The demo (demo.sermon-studio.app): a button at the top and a section of its own, which says what it cannot do.
test('the demo is linked from the top and has its own section, which says upload and share links are not there', async ({ page }) => {
  for (const lang of LANGS) {
    await page.goto('/?lang=' + lang);
    await ready(page);
    const DEMO = 'https://demo.sermon-studio.app/';
    await expect(page.locator('.hero').getByRole('link', { name: dicts[lang]['home.cta.demo'] })).toHaveAttribute('href', DEMO);
    // The top says what it costs, and For churches how to get it.
    await expect(page.locator('.hero .free')).toHaveText(dicts[lang]['home.free']);
    await expect(page.locator('.hero').getByRole('link', { name: dicts[lang]['home.cta.invite'] })).toHaveAttribute('href', '#churches');
    await expect(page.locator('#churches')).toContainText(dicts[lang]['home.churches.free'].replace(/<[^>]+>/g, ''));
    await expect(page.locator('.hero video')).toHaveAttribute('aria-label', dicts[lang]['home.video.label']);
    const section = page.locator('#demo');
    await expect(section.locator('h2')).toHaveText(dicts[lang]['home.demo.h2']);
    await expect(section).toContainText(dicts[lang]['home.demo.p2']);
    await expect(section.getByRole('link', { name: dicts[lang]['home.demo.button'] })).toHaveAttribute('href', DEMO);
    if (lang === 'en') {
      await section.scrollIntoViewIfNeeded();
      await page.screenshot({ path: path.join(shots, 'site-demo.png') });
    }
  }
  expect(dicts.en['home.demo.p2']).toMatch(/Uploading recordings is not supported.*share links cannot be made/);
});

test('the header has the sections and the guide; privacy and About are at the bottom', async ({ page }) => {
  const problems = watch(page);
  await page.goto('/?lang=nb');
  await ready(page);
  const nav = page.locator('header nav');
  for (const key of ['nav.how', 'nav.mixer', 'nav.churches', 'nav.self', 'nav.guide']) await expect(nav.getByRole('link', { name: dicts.nb[key] })).toBeVisible();
  await expect(nav.locator('a.page')).toHaveAttribute('href', '/nb/guide');
  await expect(nav.locator('a.page')).toHaveAttribute('title', dicts.nb['nav.guide.title']);
  await expect(page.locator('header a[href$="privacy"]')).toHaveCount(0);
  const about = page.locator('footer');
  await expect(about.getByRole('heading', { name: dicts.nb['about.h2'] })).toBeVisible();
  await expect(about.getByRole('link', { name: 'Gunnar Inge Gjøvik Sortland' })).toHaveAttribute('href', 'https://gunnaringe.sort.land');
  await expect(about.getByRole('link', { name: dicts.nb['nav.privacy'] })).toHaveAttribute('href', '/nb/privacy');
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
  await page.getByRole('button', { name: dicts.nb['nav.menu'], exact: true }).click();
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
  const button = page.getByRole('button', { name: dicts.nb['nav.menu'], exact: true });
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
  await expect(page.locator('h1')).toHaveText(dicts.en['notfound.h1']);
  // Under a language it is that language's, and leads to its front page.
  expect((await page.goto('/nb/no/such/page')).status()).toBe(404);
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.nb['notfound.h1']);
  await expect(page.locator('main a')).toHaveAttribute('href', '/nb/');
  for (const f of ['/wrangler.jsonc', '/README.md', '/.assetsignore', '/_headers', '/src/index.html', '/src/lang/en.json', '/src/build.mjs']) expect((await request.get(f)).status(), f).toBe(404);
  expect((await request.get('/guide')).status()).toBe(200);
});

// A link preview (Signal, Slack…) and a search engine run no script: each page's head has its title, description
// and picture in its own language, its own address, and the addresses of the other languages.
test('every page has a link preview and its other languages without its script', async ({ request }) => {
  for (const lang of LANGS) {
    for (const p of PAGES) {
      const where = at(lang, p);
      const head = (await (await request.get(where)).text()).split('</head>')[0];
      const tag = (prop) => head.match(new RegExp(`<meta (?:property|name)="${prop}"[^>]* content="([^"]*)"`))?.[1];
      const key = p === '/' ? 'home' : p.slice(1, -5);
      expect(tag('description'), where).toBe(dicts[lang][key + '.description']);
      expect(tag('og:title'), where).toBe(dicts[lang][key + '.title']);
      expect(tag('og:description'), where).toBe(dicts[lang][key + '.description']);
      expect(tag('og:url'), where).toBe('https://sermon-studio.app' + where);
      expect(head).toContain(`<link rel="canonical" href="https://sermon-studio.app${where}">`);
      for (const l of LANGS) expect(head).toContain(`<link rel="alternate" hreflang="${l}" href="https://sermon-studio.app${at(l, p)}">`);
      expect(head).toContain(`<link rel="alternate" hreflang="x-default" href="https://sermon-studio.app${at('en', p)}">`);
      const image = tag('og:image');
      expect(image, where).toBe('https://sermon-studio.app/img/og.png');
      const r = await request.get(new URL(image).pathname);
      expect(r.status()).toBe(200);
      expect(r.headers()['content-type']).toContain('image/png');
    }
  }
});

test('the pages are what site/src makes', () => {
  expect(stale(SITE), 'run: node site/src/build.mjs').toEqual([]);
});

test('without JavaScript every page has its text, pictures and links', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, locale: 'nb-NO' });
  const page = await context.newPage();
  for (const lang of LANGS) {
    await page.goto(ORIGIN + at(lang, '/'));
    await expect(page.locator('html')).toHaveAttribute('lang', lang); // no script sends a Norwegian browser elsewhere
    await expect(page.locator('h1')).toHaveText(dicts[lang]['home.h1']);
    await expect(page.locator('h1')).toBeVisible();
    await expect(page.locator('.hero .lede')).toHaveText(dicts[lang]['home.lede']);
    await expect(page.locator('img[data-shot=recordings]')).toHaveAttribute('src', `/img/shots/${lang}/recordings.webp`);
    // The language menu is links: it works as it is.
    await page.locator('#lang summary').click();
    await page.locator('#lang a[lang=nn]').click();
    await expect(page).toHaveURL(ORIGIN + '/nn/');
    await page.goto(ORIGIN + at(lang, '/guide.html'));
    await expect(page.locator('h1')).toHaveText(dicts[lang]['guide.h1']);
  }
  await context.close();
});

test('search engines are told of every page in every language', async ({ request }) => {
  const robots = await (await request.get('/robots.txt')).text();
  expect(robots).toContain('Sitemap: https://sermon-studio.app/sitemap.xml');
  const sitemap = await (await request.get('/sitemap.xml')).text();
  const locs = [...sitemap.matchAll(/<loc>([^<]+)<\/loc>/g)].map((m) => m[1]);
  expect(locs.sort()).toEqual(LANGS.flatMap((l) => PAGES.map((p) => 'https://sermon-studio.app' + at(l, p))).sort());
  for (const loc of locs) expect((await request.get(new URL(loc).pathname)).status(), loc).toBe(200);
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
    await page.locator('#what').screenshot({ path: path.join(shots, `site-what-${lang}.png`) });
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
