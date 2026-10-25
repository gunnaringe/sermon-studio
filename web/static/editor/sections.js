// The editor's sections: the parts of the recording that make the sermon, drawn on the waveform and listed with
// their times, labels and pauses. Also the selected section or cut, and the rows' fields both lists use.
import { t, tn } from '../i18n.js';
import { h, parseTime, formatTime } from '../util.js';
import { css } from './common.js';

// rebuild replaces a list's rows. Removing a focused field makes the browser
// send it a change event; edits ignore those (rebuilding), as the rows are new.
let rebuilding = false;
export function rebuild(list, ...rows) {
  rebuilding = true;
  try { list.replaceChildren(...rows); } finally { rebuilding = false; }
}
export const isRebuilding = () => rebuilding;

// showLabel writes a section's or cut's label on its region in the waveform.
export const showLabel = (x) => x.region.setContent(x.label || undefined);

/** @param {import('./common.js').Editor} ed */
export function labelInput(ed, x, name) {
  return h('input', {
    type: 'text', class: 'span-label', maxlength: 60, value: x.label || '', placeholder: t('label (optional)'), 'aria-label': t('{name} label', { name }),
    oninput: (e) => { x.label = e.target.value; showLabel(x); ed.changed(); },
  });
}

// pauseInput is the field for the silence (seconds) put in at the end of a section or the start of a cut.
/** @param {import('./common.js').Editor} ed */
export function pauseInput(ed, x, name, title) {
  return h('label', { class: 'pause', title }, t('Pause'), ' ', h('input', {
    type: 'number', class: 'pause', min: 0, max: 30, step: 0.5, value: x.pause || 0, 'aria-label': t('{name} pause', { name }),
    oninput: (e) => { x.pause = Math.min(30, Math.max(0, Number(e.target.value) || 0)); ed.syncCuts(); ed.changed(); },
  }));
}

/** @param {import('./common.js').Editor} ed */
export function sectionsPart(ed) {
  const { sections, cuts } = ed;
  const sectionList = h('div', { class: 'sections' });
  const sectionHint = h('p', { class: 'muted section-hint' });
  const lengthOut = h('p', { class: 'length' });
  let sectionSeq = 0;

  const sectionOf = (r) => sections.find((x) => x.region === r);
  const first = () => sections[0].region.start;
  const last = () => sections[sections.length - 1].region.end;
  const newRegion = (start, end) => ed.regions.addRegion({ id: 'section-' + (++sectionSeq), start, end, color: css('--selection'), drag: true, resize: true });

  // addSection adds a section between two times; it returns it, or null if empty. pause is the silence put in
  // where it ends (seconds).
  function addSection(start, end, label = '', pause = 0) {
    start = Math.max(0, Math.min(start, ed.duration));
    end = Math.max(0, Math.min(end, ed.duration));
    if (end - start < 0.05) return null;
    const section = { region: newRegion(start, end), label, pause };
    showLabel(section);
    sections.push(section);
    tidySections();
    return section;
  }

  // addDrawn makes a region drawn on the waveform a section.
  function addDrawn(r) {
    r.setOptions({ id: 'section-' + (++sectionSeq) });
    sections.push({ region: r });
    tidySections();
  }

  // tidySections sorts the sections and makes those that overlap one (as the server does; ones that only
  // touch stay two), then shows them.
  function tidySections() {
    sections.sort((a, b) => a.region.start - b.region.start);
    for (let i = 1; i < sections.length;) {
      const a = sections[i - 1].region, b = sections[i].region;
      if (b.start >= a.end) { i++; continue; }
      if (b.end >= a.end) sections[i - 1].pause = sections[i].pause; // the pause is where the section ends
      if (b.end > a.end) a.setOptions({ end: b.end });
      if (!sections[i - 1].label) { sections[i - 1].label = sections[i].label; showLabel(sections[i - 1]); }
      b.remove();
      sections.splice(i, 1);
    }
    renderSections();
    ed.changed();
  }

  // splitHere splits the section at the playhead into two, which touch. The pause (where the section
  // ends) goes with the second.
  function splitHere() {
    if (!ed.ws) return;
    const now = ed.ws.getCurrentTime();
    const x = sections.find((y) => now > y.region.start + 0.05 && now < y.region.end - 0.05);
    if (!x) return;
    const end = x.region.end, pause = x.pause || 0;
    x.pause = 0;
    x.region.setOptions({ end: now });
    const second = { region: newRegion(now, end), label: '', pause };
    showLabel(second);
    sections.push(second);
    tidySections();
  }

  // ---- the selected section or cut: clicked on the waveform, or its row in use; the row and the region are marked ----

  let selected = null;
  function select(x) {
    selected = x;
    markSelected();
  }
  function markSelected() {
    if (selected && !sections.includes(selected) && !cuts.includes(selected)) selected = null;
    for (const y of [...sections, ...cuts]) {
      const on = y === selected;
      if (y.row) y.row.classList.toggle('selected', on);
      if (!y.region.element) continue;
      y.region.element.style.boxShadow = on ? 'inset 0 0 0 2px var(--accent)' : '';
      // A cut is always above the section it is in, whichever was drawn last, so a click on it gets the cut.
      y.region.element.style.zIndex = sections.includes(y) ? '1' : '2';
    }
  }

  function removeSelected() {
    if (!selected) return;
    if (sections.includes(selected)) removeSection(selected);
    else ed.removeCut(selected);
    selected = null;
  }

  // nudge moves the edge of the selected section or cut that is nearest the playhead by seconds (0.1 s, shift 0.01 s).
  function nudge(seconds) {
    if (!selected || !ed.ws) return;
    const r = selected.region, now = ed.ws.getCurrentTime();
    const which = Math.abs(now - r.start) <= Math.abs(now - r.end) ? 'start' : 'end';
    const time = Math.max(0, Math.min(r[which] + seconds, ed.duration));
    if (which === 'start' ? time > r.end - 0.05 : time < r.start + 0.05) return;
    r.setOptions({ [which]: time });
    if (sections.includes(selected)) tidySections();
    else { ed.sortCuts(); ed.renderCuts(); ed.changed(); }
  }

  // selection is the selected section or cut, for the tests.
  const selection = () => selected ? { kind: sections.includes(selected) ? 'section' : 'cut', start: selected.region.start, end: selected.region.end } : null;

  function removeSection(x) {
    x.region.remove();
    sections.splice(sections.indexOf(x), 1);
    renderSections();
    ed.changed();
  }

  // setEdge moves the start or end of a section to t; false if it would be empty.
  function setEdge(x, which, time) {
    const r = x.region;
    if (which === 'start' ? time > r.end - 0.05 : time < r.start + 0.05) return false;
    r.setOptions({ [which]: time });
    tidySections();
    return true;
  }

  // renderSections rebuilds the list of sections; syncSections only refreshes its
  // values. With none, an empty row makes the first one from typed times.
  function renderSections() {
    if (!sections.length) {
      const startIn = h('input', { type: 'text', class: 'time', placeholder: t('start'), 'aria-label': t('{name} from', { name: t('Section') }) });
      const endIn = h('input', { type: 'text', class: 'time', placeholder: t('end'), 'aria-label': t('{name} to', { name: t('Section') }) });
      const edit = () => {
        if (rebuilding) return;
        const start = startIn.value.trim() === '' ? 0 : parseTime(startIn.value);
        const end = endIn.value.trim() === '' ? ed.duration : parseTime(endIn.value);
        startIn.classList.toggle('invalid', Number.isNaN(start));
        endIn.classList.toggle('invalid', Number.isNaN(end) || end <= start);
        if (!Number.isNaN(start) && !Number.isNaN(end) && addSection(start, end)) ed.reveal(start);
      };
      for (const input of [startIn, endIn]) {
        input.addEventListener('change', edit);
        input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { edit(); input.blur(); } });
      }
      rebuild(sectionList, h('div', { class: 'section' }, h('strong', {}, t('Section')),
        h('label', {}, t('From'), ' ', startIn), h('label', {}, t('to'), ' ', endIn)));
      syncSections();
      ed.syncCuts();
      return;
    }
    rebuild(sectionList, ...sections.map((x, i) => {
      const r = x.region;
      const name = sections.length > 1 ? t('Section {n}', { n: i + 1 }) : t('Section');
      const startIn = x.startIn = h('input', { type: 'text', class: 'time', 'aria-label': t('{name} from', { name }) });
      const endIn = x.endIn = h('input', { type: 'text', class: 'time', 'aria-label': t('{name} to', { name }) });
      x.info = h('span', { class: 'muted' });
      const edit = () => {
        if (rebuilding) return;
        const start = startIn.value.trim() === '' ? 0 : parseTime(startIn.value);
        const end = endIn.value.trim() === '' ? ed.duration : parseTime(endIn.value);
        startIn.classList.toggle('invalid', Number.isNaN(start));
        endIn.classList.toggle('invalid', Number.isNaN(end) || end <= start);
        if (!Number.isNaN(start) && !Number.isNaN(end) && end - start >= 0.05) {
          r.setOptions({ start: Math.max(0, start), end: Math.min(end, ed.duration) });
          tidySections();
          ed.reveal(start);
        }
      };
      for (const input of [startIn, endIn]) {
        input.addEventListener('change', edit);
        input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { edit(); input.blur(); } });
      }
      x.row = h('div', { class: 'section' },
        h('strong', {}, name),
        h('label', {}, t('From'), ' ', x.startIn), h('label', {}, t('to'), ' ', x.endIn), x.info,
        labelInput(ed, x, name),
        x === sections[sections.length - 1] ? '' : pauseInput(ed, x, name, t('Silence put in after this section, in seconds')),
        h('button', { title: t('Play this section'), onclick: () => ed.playFor(r.start, r.end - r.start) }, t('Play')),
        i < sections.length - 1 ? h('button', { title: t('Play 3 s before the end of this section and 3 s from the start of the next, as it will sound'), onclick: () => {
          const next = sections[i + 1].region, from = Math.max(r.end - 3, 0);
          ed.playFor(from, next.start + 3 - from, true);
        } }, t('Hear join')) : '',
        h('button', { onclick: () => removeSection(x) }, t('Remove')));
      x.row.addEventListener('focusin', () => select(x));
      x.row.addEventListener('pointerdown', () => select(x));
      return x.row;
    }));
    markSelected();
    syncSections();
    ed.syncCuts();
  }

  function syncSections() {
    for (const x of sections) {
      const r = x.region;
      if (document.activeElement !== x.startIn) x.startIn.value = formatTime(r.start);
      if (document.activeElement !== x.endIn) x.endIn.value = formatTime(r.end);
      x.info.textContent = t('length {time}', { time: formatTime(r.end - r.start, 1) });
    }
    sectionHint.textContent = !sections.length
      ? t('Drag on the waveform to select the sermon, or set its start and end at the playhead (s, e).')
      : ed.proposed
        ? t('The sermon is set to the recording without the silence at its start and end. Move its edges, or set its start and end at the playhead (s, e).')
        : t('Drag on an empty part of the waveform to add another section. Parts between sections are left out.');
  }

  // showLength tells how long the sermon is, and what it is made of.
  function showLength() {
    const parts = sections.length > 1 ? tn(sections.length, '{n} section', '{n} sections') : '';
    const pauses = ed.activePauses();
    const what = [parts, cuts.length ? tn(cuts.length, '{n} cut', '{n} cuts') : '', pauses.length ? tn(pauses.length, '{n} pause', '{n} pauses') : ''].filter(Boolean).join(' ' + t('and') + ' ');
    lengthOut.textContent = sections.length ? t('Length {time}', { time: formatTime(ed.keptLength() + pauses.reduce((n, p) => n + p.seconds, 0), 1) }) + (what ? ` (${what})` : '') : '';
  }

  // ---- the first section: a recording not cut yet opens with the sermon set to all of it but the
  // silence at its start and end. It is only a start: nothing is kept until something is changed. ----

  function proposeSection(silences) {
    const lead = silences.find((s) => s.start <= 0.5);
    const tail = [...silences].reverse().find((s) => s.end >= ed.duration - 0.5);
    const start = lead ? lead.end : 0, end = tail ? tail.start : ed.duration;
    ed.restoring = true;
    try {
      if (end - start >= 1) addSection(start, end);
    } finally {
      ed.restoring = false;
    }
    ed.proposed = sections.length > 0;
    ed.lastSent = JSON.stringify(ed.currentSettings());
    renderSections();
    ed.check();
  }

  // adopt makes that start the user's own: from here on it is kept like any edit, even when a change
  // leaves it as it was.
  function adopt() {
    ed.proposed = false;
    ed.lastSent = '';
    syncSections();
  }

  // ---- setting a section's edges at the playhead ----

  // sectionAt is the section around t, or null.
  const sectionAt = (t) => sections.find((x) => t >= x.region.start && t <= x.region.end) || null;

  // markStart starts the section at the playhead there, or else the next one;
  // with none after it, a new section runs from there to the end.
  function markStart() {
    const t = ed.ws.getCurrentTime();
    const x = sectionAt(t) || sections.find((y) => y.region.start > t);
    if (x) setEdge(x, 'start', t);
    else addSection(t, ed.duration);
  }

  // markEnd ends the section at the playhead there, or else the one before;
  // with none before it, a new section runs from the beginning to there.
  function markEnd() {
    const t = ed.ws.getCurrentTime();
    const x = sectionAt(t) || [...sections].reverse().find((y) => y.region.end < t);
    if (x) setEdge(x, 'end', t);
    else addSection(0, t);
  }

  const button = (label, title, fn) => h('button', { title, onclick: fn }, label);
  const sectionsGroup = () => h('div', { class: 'group' },
    h('strong', {}, t('Sections')),
    h('div', { class: 'toolbar' },
      button(t('Set start'), t('The section at the playhead (or the next one) starts there (s)'), markStart),
      button(t('Set end'), t('The section at the playhead (or the one before) ends there (e)'), markEnd),
      h('span', { class: 'sep' }),
      button(t('Split here'), t('Split the section at the playhead into two (x)'), splitHere)),
    sectionHint,
    sectionList,
    lengthOut);

  return {
    sectionsGroup, sectionOf, first, last, addSection, addDrawn, tidySections, splitHere, select, markSelected, removeSelected, nudge,
    selection, renderSections, syncSections, showLength, proposeSection, adopt, markStart, markEnd,
  };
}
