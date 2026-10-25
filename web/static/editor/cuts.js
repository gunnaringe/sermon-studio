// The editor's cuts: parts of the sermon that are left out, drawn on the waveform (shift-drag, or c where one
// starts and again where it ends) and listed with their times, labels and pauses.
import { t } from '../i18n.js';
import { h, parseTime, formatTime } from '../util.js';
import { css } from './common.js';
import { rebuild, isRebuilding, showLabel, labelInput, pauseInput } from './sections.js';

/** @param {import('./common.js').Editor} ed */
export function cutsPart(ed) {
  const { sections, cuts } = ed;
  const cutList = h('div', { class: 'cuts' });
  const cutHint = h('p', { class: 'muted' });
  const cutHelp = h('p', { class: 'muted' }, t('Mark a part that should not be published, such as a cough, a song or something sensitive.'));
  const addCutButton = h('button', { title: t('Start a cut at the playhead; press again where it ends (c)'), onclick: addCutHere }, t('Cut out here'));
  let cutSeq = 0;
  let markSignature = '';

  const cutOf = (r) => cuts.find((c) => c.region === r);
  const sortCuts = () => { cuts.sort((a, b) => a.region.start - b.region.start); };

  // addCut adds a cut between two times; it returns the cut, or null if empty.
  function addCut(start, end, label = '', pause = 0) {
    start = Math.max(0, Math.min(start, ed.duration));
    end = Math.max(0, Math.min(end, ed.duration));
    if (end - start < 0.05) return null;
    const cut = { region: ed.regions.addRegion({ id: 'cut-' + (++cutSeq), start, end, color: css('--cut'), drag: true, resize: true }), label, pause };
    showLabel(cut);
    cuts.push(cut);
    sortCuts();
    renderCuts();
    ed.changed();
    return cut;
  }

  // ---- cutting while listening: c (or Cut out here) starts a cut at the playhead, which then follows the
  // playhead (played or moved) until c (End the cut here) ends it there; Escape lets it go. The open cut is
  // only drawn: it is not one of the cuts, so playback does not jump over it. ----

  let openCut = null; // { start, region }

  function addCutHere() {
    if (!ed.ws || !sections.length) return;
    if (openCut) { closeCut(); return; }
    const start = Math.min(Math.max(ed.ws.getCurrentTime(), ed.first()), ed.last() - 0.05);
    const region = ed.regions.addRegion({ id: 'cut-open', start, end: start + 0.05, color: css('--cut'), drag: false, resize: false });
    if (region.element) region.element.style.pointerEvents = 'none';
    openCut = { start, region };
    showOpenCut();
  }

  function followOpenCut(now) {
    if (!openCut) return;
    const a = Math.min(openCut.start, now), b = Math.max(openCut.start, now);
    openCut.region.setOptions({ start: a, end: Math.max(b, a + 0.05) });
  }

  function closeCut() {
    const { start, region } = openCut, now = ed.ws.getCurrentTime();
    region.remove();
    openCut = null;
    showOpenCut();
    if (Math.abs(now - start) >= 0.05) addCut(Math.min(start, now), Math.max(start, now));
  }

  // cancelCut lets an open cut go; false if there was none.
  function cancelCut() {
    if (!openCut) return false;
    openCut.region.remove();
    openCut = null;
    showOpenCut();
    return true;
  }

  function showOpenCut() {
    addCutButton.textContent = openCut ? t('End the cut here') : t('Cut out here');
    addCutButton.title = openCut ? t('The cut ends at the playhead (c); Escape lets it go') : t('Start a cut at the playhead; press again where it ends (c)');
    addCutButton.classList.toggle('primary', Boolean(openCut));
    syncCuts();
  }

  function removeCut(c) {
    c.region.remove();
    cuts.splice(cuts.indexOf(c), 1);
    renderCuts();
    ed.changed();
  }

  // renderCuts rebuilds the list of cuts; syncCuts only refreshes its values.
  function renderCuts() {
    rebuild(cutList, ...cuts.map((c, i) => {
      const r = c.region;
      c.startIn = h('input', { type: 'text', class: 'time', 'aria-label': t('Cut {n} from', { n: i + 1 }) });
      c.endIn = h('input', { type: 'text', class: 'time', 'aria-label': t('Cut {n} to', { n: i + 1 }) });
      c.info = h('span', { class: 'muted' });
      const edit = () => {
        if (isRebuilding()) return;
        const start = parseTime(c.startIn.value), end = parseTime(c.endIn.value);
        c.startIn.classList.toggle('invalid', Number.isNaN(start));
        c.endIn.classList.toggle('invalid', Number.isNaN(end) || end <= start);
        if (!Number.isNaN(start) && !Number.isNaN(end) && end > start) {
          r.setOptions({ start: Math.max(0, start), end: Math.min(end, ed.duration) });
          sortCuts();
          renderCuts();
          ed.changed();
        }
      };
      for (const input of [c.startIn, c.endIn]) {
        input.addEventListener('change', edit);
        input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { edit(); input.blur(); } });
      }
      const here = (which) => () => {
        const now = ed.ws.getCurrentTime();
        if (which === 'start' && now < r.end - 0.05) r.setOptions({ start: now });
        if (which === 'end' && now > r.start + 0.05) r.setOptions({ end: now });
        sortCuts();
        renderCuts();
        ed.changed();
      };
      c.row = h('div', { class: 'cut' },
        h('strong', {}, t('Cut {n}', { n: i + 1 })),
        h('label', {}, t('From'), ' ', c.startIn), h('label', {}, t('to'), ' ', c.endIn), c.info,
        labelInput(ed, c, t('Cut {n}', { n: i + 1 })),
        pauseInput(ed, c, t('Cut {n}', { n: i + 1 }), t('Silence put in where the cut is, in seconds')),
        h('button', { title: t('The cut starts at the playhead'), onclick: here('start') }, t('Set start')),
        h('button', { title: t('The cut ends at the playhead'), onclick: here('end') }, t('Set end')),
        h('button', { title: t('Play 3 s before and after the cut, as it will sound'), onclick: () => {
          const from = Math.max(r.start - 3, 0);
          ed.playFor(from, r.end + 3 - from, true);
        } }, t('Hear join')),
        h('button', { onclick: () => removeCut(c) }, t('Remove')),
        c.warn = h('p', { class: 'cut-warn', role: 'status' }));
      c.row.addEventListener('focusin', () => ed.select(c));
      c.row.addEventListener('pointerdown', () => ed.select(c));
      return c.row;
    }));
    ed.markSelected();
    syncCuts();
  }

  function syncCuts() {
    for (const c of cuts) {
      const r = c.region;
      if (document.activeElement !== c.startIn) c.startIn.value = formatTime(r.start);
      if (document.activeElement !== c.endIn) c.endIn.value = formatTime(r.end);
      // How much of the cut is in a section: none, some or all. A cut outside
      // the sections is allowed (what is outside is left out anyway), but it does nothing.
      const inside = sections.reduce((n, x) => n + Math.max(0, Math.min(r.end, x.region.end) - Math.max(r.start, x.region.start)), 0);
      const none = inside < 0.001, partly = !none && inside < r.end - r.start - 0.001;
      c.row.classList.toggle('outside', none);
      c.row.classList.toggle('partly', partly);
      c.info.textContent = t('length {time}', { time: formatTime(r.end - r.start, 1) });
      const n = cuts.indexOf(c) + 1;
      c.warn.textContent = none ? '⚠ ' + t('Cut {n} does not have any effect as it is not in an included section', { n })
        : partly ? '⚠ ' + t('Cut {n} is only partially part of a section', { n }) : '';
      c.warn.hidden = !c.warn.textContent;
    }
    if (!sections.length && openCut) cancelCut();
    cutHint.textContent = !sections.length ? t('Select the sermon first.')
      : openCut ? t('Play or move the playhead to where the cut ends, then press End the cut here (c). Escape lets it go.')
        : cuts.length ? '' : t('Nothing cut out. Press the button (or c) where a cut starts and again where it ends, or shift-drag on the waveform.');
    addCutButton.disabled = !sections.length;
    ed.showLength();
    ed.showClipLength();
    drawPauseMarks(ed.activePauses());
    ed.drawFades();
  }

  // drawPauseMarks puts a marker on the waveform where each pause is.
  function drawPauseMarks(pauses) {
    if (!ed.regions) return;
    const signature = JSON.stringify(pauses);
    if (signature === markSignature) return;
    markSignature = signature;
    for (const r of ed.regions.getRegions()) if (r.id.startsWith('pause-')) r.remove();
    pauses.forEach((p, i) => {
      const r = ed.regions.addRegion({ id: 'pause-' + i, start: p.at, content: h('span', { style: 'white-space: nowrap; padding: 0 0.3rem' }, '⏸ ' + t('{n} s', { n: p.seconds })), color: 'rgba(124, 58, 237, 0.9)', drag: false, resize: false });
      if (r.element) r.element.style.pointerEvents = 'none';
    });
  }

  const cutsGroup = () => h('div', { class: 'group' },
    h('strong', {}, t('Cut out')),
    h('div', { class: 'toolbar' }, addCutButton),
    cutHelp,
    cutHint,
    cutList);

  return { cutsGroup, cutOf, sortCuts, addCut, addCutHere, followOpenCut, cancelCut, removeCut, renderCuts, syncCuts };
}
