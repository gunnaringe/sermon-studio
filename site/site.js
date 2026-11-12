// The website's script. The pages have their text in them, one page per language (src/build.mjs makes them:
// English at /, bokmål under /nb/, nynorsk under /nn/), so this only: sends a reader of an English page to
// their own language, remembers a choice in the language menu (the globe in the header), opens the header's
// menu on a phone, marks the section in view, and shows a picture larger. Loaded in <head> (not deferred), so
// a page that is about to be replaced by another language's is never shown.

const LANGS = ['en', 'nb', 'nn'];
const root = document.documentElement;

function stored() {
  try { return localStorage.getItem('lang'); } catch { return null; }
}
function store(lang) {
  try { localStorage.setItem('lang', lang); } catch { /* private mode: the choice lasts for this page */ }
}

// The language a reader wants: ?lang= in the address (kept as the choice), else the one chosen before, else
// the browser's (Norwegian "no" is bokmål; nynorsk only when asked for), else English.
function wanted() {
  const asked = new URLSearchParams(location.search).get('lang');
  if (asked && LANGS.includes(asked)) {
    store(asked);
    return asked;
  }
  const kept = stored();
  if (kept && LANGS.includes(kept)) return kept;
  for (const tag of navigator.languages || [navigator.language || 'en']) {
    const base = tag.toLowerCase().split('-')[0];
    if (base === 'nn') return 'nn';
    if (base === 'nb' || base === 'no') return 'nb';
    if (base === 'en') return 'en';
  }
  return 'en';
}

// A page's address in another language: /guide is /nb/guide in bokmål.
function inLang(lang) {
  const rest = location.pathname.replace(/^\/(nb|nn)(\/|$)/, '/');
  return (lang === 'en' ? '' : '/' + lang) + rest;
}

// An English page (or one asked for with ?lang=) goes to the reader's language; a page under /nb/ or /nn/
// was asked for as it is, and stays. Until then the page is hidden (style.css: html.js:not(.ready)).
root.classList.add('js');
(() => {
  const asked = new URLSearchParams(location.search).has('lang');
  const lang = root.lang === 'en' || asked ? wanted() : root.lang;
  if (lang !== root.lang) {
    location.replace(inLang(lang) + location.hash);
    setTimeout(() => root.classList.add('ready'), 3000); // never leave the page hidden
    return;
  }
  if (asked) history.replaceState(null, '', location.pathname + location.hash);
  root.classList.add('ready');
})();

document.addEventListener('DOMContentLoaded', () => {
  // A choice in the language menu is remembered; its links go to the same page in that language.
  const menu = /** @type {HTMLDetailsElement | null} */ (document.getElementById('lang'));
  for (const a of /** @type {NodeListOf<HTMLAnchorElement>} */ (menu?.querySelectorAll('a[lang]') ?? [])) a.addEventListener('click', () => store(a.lang));
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
    if (menu?.open && !menu.contains(/** @type {Node} */ (e.target))) menu.open = false;
    if (header?.classList.contains('open') && !header.contains(/** @type {Node} */ (e.target))) setOpen(false);
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
  // A click on a picture of the studio shows it larger, in a dialog; a click there, or Escape, closes it.
  const zoom = /** @type {HTMLDialogElement | null} */ (document.getElementById('zoom'));
  if (zoom) {
    const large = /** @type {HTMLImageElement} */ (zoom.querySelector('img'));
    for (const b of document.querySelectorAll('button.zoom')) {
      b.addEventListener('click', () => {
        const img = /** @type {HTMLImageElement} */ (b.querySelector('img'));
        large.src = img.currentSrc || img.src;
        large.alt = img.alt;
        zoom.showModal();
      });
    }
    zoom.addEventListener('click', () => zoom.close());
  }
  // A choice in the phone's menu closes it.
  for (const a of document.querySelectorAll('nav a')) a.addEventListener('click', () => setOpen(false));
});
