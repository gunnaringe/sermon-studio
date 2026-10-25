// The editor's fades: the sermon fades in from its start and out to its end; the knobs on the waveform set them.
import { t } from '../i18n.js';
import { h } from '../util.js';

/** @param {import('./common.js').Editor} ed */
export function fadesPart(ed) {
  const { opts, sections } = ed;
  let fadeLayer = null;
  const fadeKnobs = {};
  const fadeMax = () => Math.max(0, Math.min(30, Math.floor((ed.keptLength() / 2) * 10) / 10));

  // setFade sets a fade (opts.fadeIn or opts.fadeOut) to seconds, to a tenth, from 0 to what the sermon allows.
  function setFade(key, seconds) {
    if (!sections.length) return;
    const other = key === 'fadeIn' ? opts.fadeOut : opts.fadeIn;
    const v = Math.max(0, Math.min(Math.round(seconds * 10) / 10, fadeMax(), Math.max(ed.keptLength() - (other || 0), 0)));
    if (!(v >= 0) || v === opts[key]) return;
    opts[key] = v;
    ed.changed();
    drawFades();
  }

  // buildFades puts the shades and knobs on the waveform.
  function buildFades() {
    const { ws } = ed;
    fadeLayer = h('div', { style: 'position:absolute;inset:0;pointer-events:none;z-index:5' });
    ws.getWrapper().appendChild(fadeLayer);
    for (const [key, label, side] of [['fadeIn', t('Fade in (s)'), 'in'], ['fadeOut', t('Fade out (s)'), 'out']]) {
      const shade = h('div', { style: 'position:absolute;top:0;height:100%;background:linear-gradient(' + (side === 'in' ? '90deg' : '270deg') +
        ', rgba(124,58,237,0.38), rgba(124,58,237,0.05));clip-path:polygon(' + (side === 'in' ? '0 0,100% 0,0 100%' : '0 0,100% 0,100% 100%') + ')' });
      const text = h('span', { style: 'position:absolute;top:2px;font-size:11px;white-space:nowrap;color:var(--fg);background:var(--panel);border-radius:4px;padding:0 4px;opacity:0.9' });
      const knob = h('div', { role: 'slider', tabindex: 0, 'aria-label': label, 'aria-valuemin': 0, title: side === 'in' ? t('Drag to fade in from the start of the sermon') : t('Drag to fade out to the end of the sermon'),
        style: 'position:absolute;top:18px;width:16px;height:16px;margin-left:-8px;border-radius:50%;background:var(--grad);border:2px solid var(--panel);box-shadow:0 1px 4px rgba(0,0,0,0.4);cursor:ew-resize;pointer-events:auto;touch-action:none' });
      const timeAt = (e) => {
        const box = ws.getWrapper().getBoundingClientRect();
        return ((e.clientX - box.left) / box.width) * ed.duration;
      };
      knob.addEventListener('pointerdown', (down) => {
        down.preventDefault();
        down.stopPropagation();
        knob.setPointerCapture(down.pointerId);
        const move = (e) => setFade(key, side === 'in' ? timeAt(e) - ed.first() : ed.last() - timeAt(e));
        knob.addEventListener('pointermove', move);
        knob.addEventListener('pointerup', () => knob.removeEventListener('pointermove', move), { once: true });
      });
      knob.addEventListener('keydown', (e) => {
        const step = e.shiftKey ? 0.1 : 0.5;
        const dir = { ArrowRight: 1, ArrowUp: 1, ArrowLeft: -1, ArrowDown: -1 }[e.key];
        if (!dir) return;
        e.preventDefault();
        e.stopPropagation();
        setFade(key, (opts[key] || 0) + dir * step);
      });
      for (const el of [shade, text, knob]) fadeLayer.appendChild(el);
      fadeKnobs[key] = { shade, text, knob, side };
    }
    drawFades();
  }

  // drawFades puts the shades and knobs where the fades are: from the start of the first section, and to the
  // end of the last. Positions are in percent of the waveform, so zooming moves them along.
  function drawFades() {
    if (!fadeLayer) return;
    fadeLayer.hidden = !sections.length;
    if (!sections.length) return;
    for (const key of ['fadeIn', 'fadeOut']) {
      const { shade, text, knob, side } = fadeKnobs[key];
      const v = Math.min(opts[key] || 0, fadeMax());
      const pct = (time) => (time / ed.duration) * 100;
      const from = side === 'in' ? ed.first() : ed.last() - v, to = side === 'in' ? ed.first() + v : ed.last();
      shade.style.left = pct(from) + '%';
      shade.style.width = pct(to - from) + '%';
      shade.hidden = v <= 0;
      knob.style.left = pct(side === 'in' ? to : from) + '%';
      knob.setAttribute('aria-valuenow', String(v));
      knob.setAttribute('aria-valuemax', String(fadeMax()));
      knob.setAttribute('aria-valuetext', t('{n} s', { n: v }));
      text.textContent = v > 0 ? (side === 'in' ? t('fade in') : t('fade out')) + ' ' + t('{n} s', { n: v }) : '';
      text.style.left = side === 'in' ? 'calc(' + pct(to) + '% + 12px)' : 'auto';
      text.style.right = side === 'out' ? 'calc(' + (100 - pct(from)) + '% + 12px)' : 'auto';
      text.hidden = v <= 0;
    }
  }

  return { setFade, buildFades, drawFades };
}
