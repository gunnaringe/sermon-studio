// Makes the website's pages from the templates here (index, guide, privacy, 404) and the texts in lang/: one
// page per language, with its text in the HTML, so it reads without JavaScript and search engines see it.
// English at /, bokmål under /nb/, nynorsk under /nn/, plus sitemap.xml. The pages it makes are committed
// (the Worker has no build step); the site's test fails when they are not what this makes.
//
//   node site/src/build.mjs          writes the pages into site/
//   node site/src/build.mjs --check  only says which differ (exit 1)
//
// In a template: data-t="key" fills an element, data-t-<attribute>="key" sets an attribute, img/source
// data-shot="name" is the studio's picture in the page's language. Addresses are written as from the site's
// root (guide.html, img/logo.svg) and come out absolute, pages in the page's language.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const SRC = path.dirname(fileURLToPath(import.meta.url));
const ORIGIN = 'https://sermon-studio.app';
export const LANGS = { en: 'English', nb: 'Norsk bokmål', nn: 'Norsk nynorsk' };
export const PAGES = ['index', 'guide', 'privacy', '404'];
const OG_LOCALE = { en: 'en_GB', nb: 'nb_NO', nn: 'nn_NO' };

const prefix = (lang) => (lang === 'en' ? '' : lang + '/');
// address is a page's address in a language: /, /nb/, /nb/guide (as Cloudflare serves guide.html).
export const address = (lang, page) => '/' + prefix(lang) + (page === 'index' ? '' : page);
const attr = (s) => s.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;');

function render(page, lang, dict) {
  const t = (key) => {
    if (!(key in dict)) throw new Error(`${page}.html: no text "${key}" in ${lang}.json`);
    return dict[key];
  };
  let html = fs.readFileSync(path.join(SRC, page + '.html'), 'utf8');
  html = html.replace('<html lang="en">', `<html lang="${lang}">`);
  // An element's text.
  html = html.replace(/<(\w+)([^>]*?) data-t="([^"]+)"([^>]*)><\/\1>/g, (_, tag, before, key, after) =>
    `<${tag}${before}${after}>${tag === 'title' ? attr(t(key)) : t(key)}</${tag}>`);
  // Attributes, and the studio's pictures.
  html = html.replace(/<[a-z][^>]*>/g, (tag) => {
    const marks = [...tag.matchAll(/ data-t-([a-z-]+)="([^"]+)"/g)];
    tag = tag.replace(/ data-t-[a-z-]+="[^"]+"/g, '');
    for (const [, name, key] of marks) {
      const set = ` ${name}="${attr(t(key))}"`;
      const had = new RegExp(` ${name}="[^"]*"`);
      tag = had.test(tag) ? tag.replace(had, set) : tag.replace(/\s*\/?>$/, (end) => set + end.trimStart());
    }
    const shot = tag.match(/ data-shot="([^"]+)"/);
    if (shot) tag = tag.replace(/^<(img|source)/, `<$1 ${tag.startsWith('<img') ? 'src' : 'srcset'}="img/shots/${lang}/${shot[1]}.webp"`);
    return tag;
  });
  if (/data-t(-[a-z-]+)?=/.test(html)) throw new Error(`${page}.html: a data-t that was not filled: ${html.match(/<[^>]*data-t[^>]*>/)[0]}`);
  // The language menu: the same page in each language.
  html = html.replace('<span class="code"></span>', `<span class="code">${lang.toUpperCase()}</span>`);
  const target = page === '404' ? 'index' : page;
  html = html.replace('<div class="menu"></div>', '<div class="menu">' + Object.entries(LANGS).map(([l, name]) =>
    `<a href="${address(l, target)}" lang="${l}" hreflang="${l}"${l === lang ? ' aria-current="true"' : ''}>${name}</a>`).join('') + '</div>');
  // The head: where the page is, its other languages, and its link preview (Signal, Slack… run no script).
  const key = page === 'index' ? 'home' : page;
  const head = page === '404' ? ['<meta name="robots" content="noindex">'] : [
    `<link rel="canonical" href="${ORIGIN}${address(lang, page)}">`,
    ...Object.keys(LANGS).map((l) => `<link rel="alternate" hreflang="${l}" href="${ORIGIN}${address(l, page)}">`),
    `<link rel="alternate" hreflang="x-default" href="${ORIGIN}${address('en', page)}">`,
    '<meta property="og:type" content="website">',
    '<meta property="og:site_name" content="Sermon Studio">',
    `<meta property="og:locale" content="${OG_LOCALE[lang]}">`,
    `<meta property="og:title" content="${attr(t(key + '.title'))}">`,
    `<meta property="og:description" content="${attr(t(key + '.description'))}">`,
    `<meta property="og:url" content="${ORIGIN}${address(lang, page)}">`,
    `<meta property="og:image" content="${ORIGIN}/img/og.png">`,
    '<meta property="og:image:width" content="1200">',
    '<meta property="og:image:height" content="630">',
    `<meta property="og:image:alt" content="Sermon Studio: ${attr(t('home.h1'))}">`,
    '<meta name="twitter:card" content="summary_large_image">',
  ];
  html = html.replace('  <meta name="theme-color"', head.map((l) => `  ${l}\n`).join('') + '  <meta name="theme-color"');
  // Addresses: from the root, pages in this language.
  html = html.replace(/ (href|src|poster|srcset)="([^"]*)"/g, (all, name, url) => {
    if (/^([a-z]+:|#|\/)/.test(url)) return all;
    const [, file, rest] = url.match(/^([^#?]*)(.*)$/);
    if (file === './' || file === '') return ` ${name}="${address(lang, 'index')}${rest}"`;
    const p = file.match(/^(guide|privacy)\.html$/);
    return ` ${name}="${p ? address(lang, p[1]) : '/' + file}${rest}"`;
  });
  return html.replace('<!doctype html>\n', `<!doctype html>\n<!-- Made by src/build.mjs from src/${page}.html and src/lang/${lang}.json: change those, not this. -->\n`);
}

// files gives every file the site is made of here: its path under site/ and its content.
export function files() {
  const out = {};
  for (const lang of Object.keys(LANGS)) {
    const dict = JSON.parse(fs.readFileSync(path.join(SRC, 'lang', lang + '.json'), 'utf8'));
    for (const page of PAGES) out[prefix(lang) + page + '.html'] = render(page, lang, dict);
  }
  const pages = PAGES.filter((p) => p !== '404');
  out['sitemap.xml'] = '<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">\n' +
    pages.flatMap((page) => Object.keys(LANGS).map((lang) => `  <url>\n    <loc>${ORIGIN}${address(lang, page)}</loc>\n` +
      Object.keys(LANGS).map((l) => `    <xhtml:link rel="alternate" hreflang="${l}" href="${ORIGIN}${address(l, page)}"/>\n`).join('') + '  </url>\n')).join('') + '</urlset>\n';
  return out;
}

// stale lists the files in site/ that are not what files() gives.
export function stale(site) {
  return Object.entries(files()).filter(([name, content]) => {
    try { return fs.readFileSync(path.join(site, name), 'utf8') !== content; } catch { return true; }
  }).map(([name]) => name);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const site = path.dirname(SRC);
  if (process.argv.includes('--check')) {
    const old = stale(site);
    if (old.length) { console.error('not what site/src makes (run node site/src/build.mjs): ' + old.join(', ')); process.exit(1); }
  } else {
    for (const [name, content] of Object.entries(files())) {
      fs.mkdirSync(path.dirname(path.join(site, name)), { recursive: true });
      fs.writeFileSync(path.join(site, name), content);
    }
  }
}
