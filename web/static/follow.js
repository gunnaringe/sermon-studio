// Following a sermon's transcript as it plays, on the share page and in the studio: the captions (WebVTT)
// as text, the sentence being heard marked and kept in view, a sentence pressed played from there.
import { h } from './util.js';
import { t } from './i18n.js';

/** @typedef {{ start: number, end: number, text: string, el?: HTMLElement }} Cue */

// parseVTT is the cues of the studio's WebVTT captions: start and end in seconds, and the text.
/** @returns {Cue[]} */
function parseVTT(body) {
  const seconds = (s) => s.split(':').reduce((sum, part) => sum * 60 + Number(part), 0);
  /** @type {Cue[]} */
  const cues = [];
  for (const block of body.replace(/\r/g, '').split(/\n\s*\n/)) {
    const lines = block.split('\n');
    const at = lines.findIndex((l) => l.includes('-->'));
    if (at < 0) continue;
    const [start, end] = lines[at].split('-->').map((x) => seconds(x.trim().split(/\s/)[0]));
    const text = lines.slice(at + 1).join(' ').trim();
    if (text && Number.isFinite(start) && Number.isFinite(end)) cues.push({ start, end, text });
  }
  return cues;
}

// paragraphsOf is the cues in paragraphs, as the studio's text has them: a new one after a pause of 2 s.
/** @param {Cue[]} cues @returns {Cue[][]} */
function paragraphsOf(cues) {
  /** @type {Cue[][]} */
  const out = [];
  cues.forEach((c, i) => {
    if (!i || c.start - cues[i - 1].end >= 2) out.push([]);
    out[out.length - 1].push(c);
  });
  return out;
}

// showCues puts the transcript's captions in box as its text: a paragraph after each pause, a span per
// sentence (the cue's el). It returns the cues.
/** @param {HTMLElement} box @param {string} body @returns {Cue[]} */
export function showCues(box, body) {
  const cues = parseVTT(body);
  box.replaceChildren(...paragraphsOf(cues).map((para) => h('p', {}, ...para.flatMap((c, i) => {
    c.el = h('span', { class: 'cue' }, c.text);
    return i ? [' ', c.el] : [c.el];
  }))));
  return cues;
}

// follow lets the transcript follow the player: the sentence being heard is marked and kept in view in its box
// (not while the reader scrolls it, for some seconds), and a sentence pressed plays from there. The cues' times
// are the sermon's; offset is where the sermon starts in the file played. startPlayer gives the player (made
// and loaded if need be, not played); withPlayer calls its function with the player once there is one.
/**
 * @param {HTMLElement} box @param {Cue[]} cues @param {number} offset
 * @param {{ startPlayer: () => Promise<HTMLAudioElement>, withPlayer: (fn: (p: HTMLAudioElement) => void) => void, onError: (e: Error) => void }} how
 */
export function follow(box, cues, offset, { startPlayer, withPlayer, onError }) {
  box.classList.add('follows');
  let current = null;
  let readerUntil = 0; // the reader scrolls the box: it is not moved until then
  const reader = () => { readerUntil = Date.now() + 10000; };
  for (const ev of ['wheel', 'touchmove', 'keydown']) box.addEventListener(ev, reader, { passive: true });
  const calm = matchMedia('(prefers-reduced-motion: reduce)').matches;
  const mark = (time) => {
    const at = time - offset;
    // the last cue begun (a pause between two keeps the one before marked)
    let lo = 0, hi = cues.length - 1, found = -1;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      if (cues[mid].start <= at) { found = mid; lo = mid + 1; } else hi = mid - 1;
    }
    const cue = found >= 0 && at < cues[found].end + 2 ? cues[found] : null;
    if (cue === current) return;
    current?.el.classList.remove('now');
    current?.el.removeAttribute('aria-current');
    current = cue;
    if (!cue) return;
    cue.el.classList.add('now');
    cue.el.setAttribute('aria-current', 'true');
    if (Date.now() < readerUntil) return;
    const top = cue.el.offsetTop - box.clientHeight / 3;
    if (Math.abs(box.scrollTop - top) > box.clientHeight / 4) box.scrollTo({ top, behavior: calm ? 'auto' : 'smooth' });
  };
  withPlayer((p) => {
    p.addEventListener('timeupdate', () => mark(p.currentTime));
    p.addEventListener('seeked', () => mark(p.currentTime));
  });
  for (const c of cues) {
    c.el.title = t('Play from here');
    c.el.addEventListener('click', async () => {
      try {
        const p = await startPlayer();
        if (p.readyState < 1) {
          if (p.preload === 'none') {
            p.preload = 'metadata';
            p.load();
          }
          await new Promise((ok) => p.addEventListener('loadedmetadata', ok, { once: true }));
        }
        p.currentTime = c.start + offset;
        readerUntil = 0;
        p.play().catch(() => {});
      } catch (e) {
        onError(e);
      }
    });
  }
}
