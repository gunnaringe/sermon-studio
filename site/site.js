// The website's script: the texts in the chosen language, the language menu (the globe in the header), the
// studio's pictures in that language, and the header's menu on a phone. The texts are in lang/<code>.json, one file per language with the same keys
// (web/e2e/site checks that). An element with data-t="key" gets the text (which may hold a link or <strong>);
// data-t-alt, data-t-aria-label, data-t-content and data-t-title set those attributes; img data-shot="name" shows
// img/shots/<lang>/<name>.webp. Loaded in <head> (not deferred), so the page is hidden until its text is there.

const LANGS = { en: 'English', nb: 'Norsk bokmål', nn: 'Norsk nynorsk' };

document.documentElement.classList.add('js');

function stored() {
  try { return localStorage.getItem('lang'); } catch { return null; }
}
function store(lang) {
  try { localStorage.setItem('lang', lang); } catch { /* private mode: the choice lasts for this page */ }
}

// The language: ?lang= in the address (kept as the choice), else the one chosen before, else the browser's
// (Norwegian "no" is bokmål; nynorsk only when asked for), else English.
function detect() {
  const asked = new URLSearchParams(location.search).get('lang');
  if (asked in LANGS) {
    store(asked);
    return asked;
  }
  const kept = stored();
  if (kept in LANGS) return kept;
  for (const tag of navigator.languages || [navigator.language || 'en']) {
    const base = tag.toLowerCase().split('-')[0];
    if (base === 'nn') return 'nn';
    if (base === 'nb' || base === 'no') return 'nb';
    if (base === 'en') return 'en';
  }
  return 'en';
}

const texts = {};
async function load(lang) {
  if (!texts[lang]) {
    const resp = await fetch(`lang/${lang}.json`);
    if (!resp.ok) throw new Error(`lang/${lang}.json: ${resp.status}`);
    texts[lang] = await resp.json();
  }
  return texts[lang];
}

async function show(lang) {
  let dict;
  try {
    dict = await load(lang);
  } catch {
    lang = 'en';
    dict = await load(lang);
  }
  const root = document.documentElement;
  root.lang = lang;
  for (const el of document.querySelectorAll('[data-t]')) {
    const text = dict[el.dataset.t] ?? '';
    if (el.tagName === 'TITLE') el.textContent = text;
    else el.innerHTML = text;
  }
  for (const attr of ['alt', 'aria-label', 'content', 'title']) {
    for (const el of document.querySelectorAll(`[data-t-${attr}]`)) el.setAttribute(attr, dict[el.getAttribute(`data-t-${attr}`)] ?? '');
  }
  for (const img of document.querySelectorAll('img[data-shot]')) img.src = `img/shots/${lang}/${img.dataset.shot}.webp`;
  const menu = document.getElementById('lang');
  if (menu) {
    menu.querySelector('.code').textContent = lang.toUpperCase();
    for (const b of menu.querySelectorAll('button')) b.setAttribute('aria-pressed', b.lang === lang);
  }
  root.classList.add('ready');
}

document.addEventListener('DOMContentLoaded', () => {
  const menu = document.getElementById('lang');
  if (menu) {
    menu.querySelector('.menu').replaceChildren(...Object.entries(LANGS).map(([code, name]) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.lang = code;
      b.textContent = name;
      b.addEventListener('click', () => {
        menu.open = false;
        store(code);
        const url = new URL(location.href);
        if (url.searchParams.has('lang')) {
          url.searchParams.delete('lang');
          history.replaceState(null, '', url);
        }
        show(code);
      });
      return b;
    }));
  }
  // On a phone the pages are behind the menu button.
  const header = document.querySelector('header');
  const toggle = document.querySelector('.menu-button');
  const setOpen = (open) => {
    header.classList.toggle('open', open);
    toggle?.setAttribute('aria-expanded', open);
  };
  toggle?.addEventListener('click', () => setOpen(!header.classList.contains('open')));
  // A click elsewhere, or Escape, closes the menus.
  document.addEventListener('click', (e) => {
    if (menu?.open && !menu.contains(e.target)) menu.open = false;
    if (header?.classList.contains('open') && !header.contains(e.target)) setOpen(false);
  });
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape') return;
    if (menu) menu.open = false;
    if (header) setOpen(false);
  });
  // On the front page, the header's link to the section in view is marked as it scrolls (aria-current).
  const links = [...document.querySelectorAll('nav a[href^="#"]')];
  const sections = links.map((a) => document.getElementById(a.hash.slice(1))).filter(Boolean);
  if (sections.length && 'IntersectionObserver' in window) {
    const seen = new Set();
    const mark = () => {
      const current = sections.find((s) => seen.has(s));
      for (const a of links) {
        if (current && a.hash === '#' + current.id) a.setAttribute('aria-current', 'location');
        else a.removeAttribute('aria-current');
      }
    };
    const watch = new IntersectionObserver((entries) => {
      for (const e of entries) {
        if (e.isIntersecting) seen.add(e.target);
        else seen.delete(e.target);
      }
      mark();
    }, { rootMargin: '-30% 0px -60% 0px' }); // a section counts when it crosses a line a third down the window
    sections.forEach((s) => watch.observe(s));
  }
  // A choice in the phone's menu closes it.
  for (const a of document.querySelectorAll('nav a')) a.addEventListener('click', () => setOpen(false));
  // The browser goes to #section before the texts are in, so the place moves: go there again once they are.
  show(detect()).then(() => {
    const target = location.hash.length > 1 && document.getElementById(location.hash.slice(1));
    if (target) target.scrollIntoView({ behavior: 'instant' });
  }).catch(() => document.documentElement.classList.add('ready'));
});
// Never leave the page hidden, whatever happens to the texts.
setTimeout(() => document.documentElement.classList.add('ready'), 3000);
