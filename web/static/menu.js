// Icon buttons and drop-down menus: the round buttons with an icon, and a <details> that opens a panel of
// choices under its button (the Download menus).
import { h } from './util.js';

// DOWNLOAD_ICON is Feather's download icon (MIT), on the Download buttons and on a file in their menus.
// TRASH_ICON is Feather's trash-2 (MIT), on Delete.
export const DOWNLOAD_ICON = '<svg viewBox="0 0 24 24" width="16" height="16"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>'
  + '<polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>';
export const TRASH_ICON = '<svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><polyline points="3 6 5 6 21 6"/>'
  + '<path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/>'
  + '<path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/></svg>';

// iconButton is a round button with only an icon; label is its name and its tooltip. With text, the main
// actions' form, the text is shown beside the icon (a touch screen has no tooltip to say what an icon does).
export function iconButton(tag, icon, label, attrs = {}, text = '') {
  const el = h(tag, { ...(tag === 'button' ? { type: 'button' } : {}), ...attrs,
    class: 'icon-button' + (text ? ' labelled' : '') + (attrs.class ? ' ' + attrs.class : ''), 'aria-label': label, title: label });
  el.innerHTML = icon;
  if (text) el.append(h('span', {}, text));
  return el;
}

// dropMenu makes menu (a details.menu: its summary, then a .menu-panel) a drop-down: while open, the panel is
// placed by the window under its button (a list or a scroll box around it would cut it off), again when the page
// scrolls, and it closes on a click outside it and on Escape. onToggle is told when it opens or closes.
/** @param {HTMLDetailsElement} menu @param {(open: boolean) => void} [onToggle] */
export function dropMenu(menu, onToggle = () => {}) {
  const place = () => {
    if (!menu.isConnected) return listen(false); // drawn again: a new menu took its place
    if (!menu.open) return;
    const at = menu.querySelector('summary').getBoundingClientRect();
    const box = /** @type {HTMLElement} */ (menu.querySelector('.menu-panel'));
    box.style.top = Math.round(at.bottom + 6) + 'px';
    box.style.left = Math.round(Math.max(8, Math.min(at.left, innerWidth - box.offsetWidth - 8))) + 'px';
  };
  const close = (/** @type {Event} */ e) => {
    if (!menu.isConnected) return listen(false);
    if (e instanceof KeyboardEvent ? e.key !== 'Escape' : menu.contains(/** @type {Node} */ (e.target))) return;
    menu.open = false;
    if (e instanceof KeyboardEvent) /** @type {HTMLElement} */ (menu.querySelector('summary')).focus();
  };
  let listening = false;
  const listen = (on) => {
    if (on === listening) return;
    listening = on;
    const f = on ? 'addEventListener' : 'removeEventListener';
    document[f]('pointerdown', close);
    document[f]('keydown', close);
    document[f]('scroll', place, true);
    window[f]('resize', place);
  };
  menu.addEventListener('toggle', () => {
    listen(menu.open);
    place();
    onToggle(menu.open);
  });
  return menu;
}
