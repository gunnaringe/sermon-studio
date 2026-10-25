// The editor's versions and undo. The edit on the page (the sections, cuts, fades, processing and clips) is kept
// in sermon.json in the folder as it changes: the saved version, and the adjustments since. Undo and redo step
// through the edit, not the tags; in a field, ctrl+z is the field's own.
import { confirmDialog } from '../dialog.js';
import { t, tn } from '../i18n.js';
import { api, q, h, formatTime, typing, debounce, currentTenant, formatDateTime } from '../util.js';
import { SOUND_KEYS, CLIP_KEYS, spanOf } from './common.js';

/** @param {import('./common.js').Editor} ed */
export function historyPart(ed) {
  const { path, opts, sections, cuts, cleanups } = ed;
  const saveNote = h('span', { class: 'muted', role: 'status' });
  const versionOut = h('strong', {});
  const revertButton = h('button', { disabled: true, title: t('Throw away the adjustments and go back to the saved version'), onclick: revertVersion }, t('Revert…'));
  const undoButton = h('button', { disabled: true, title: t('Undo the last change to the edit (ctrl+z)'), 'aria-label': t('Undo'), onclick: () => undo() }, '↶', h('span', { class: 'wide-only' }, ' ' + t('Undo')));
  const redoButton = h('button', { disabled: true, title: t('Redo what was undone (ctrl+shift+z)'), 'aria-label': t('Redo'), onclick: () => redo() }, '↷', h('span', { class: 'wide-only' }, ' ' + t('Redo')));

  // ---- keeping the edit: the adjustments are kept as the page changes ----

  // currentSettings is what is on the page, or null while there is nothing (valid) to keep.
  function currentSettings() {
    if (!sections.length || ed.restoring) return null;
    const secs = sections.map(spanOf);
    const list = cuts.map(spanOf);
    if (secs.some((x) => x.end - x.start < 0.05)) return null;
    if (list.some((c) => c.end - c.start < 0.05)) return null; // one outside the sermon is fine
    if (!(opts.fadeIn >= 0) || !(opts.fadeOut >= 0)) return null;
    if ([opts.noiseReduction, opts.roomSound, opts.targetLufs, opts.truePeak].some(Number.isNaN)) return null;
    return { sections: secs, cuts: list, fadeIn: opts.fadeIn, fadeOut: opts.fadeOut, sound: Object.fromEntries(SOUND_KEYS.map((k) => [k, opts[k]])), ...ed.clipChoices() };
  }

  // persistAdjusted keeps what is on the page as the adjustments.
  async function persistAdjusted() {
    const state = currentSettings();
    if (!state || JSON.stringify(state) === ed.lastSent) return;
    saveNote.textContent = t('Keeping…');
    try {
      ed.sermon = await api('PUT', 'api/sermon?path=' + q(path), state);
      ed.lastSent = JSON.stringify(state);
      saveNote.textContent = t('Changes kept.');
    } catch (e) {
      saveNote.textContent = t('Not kept: {error}', { error: e.message });
    }
    renderVersion();
  }
  const saveSoon = debounce(() => { if (ed.alive) persistAdjusted(); }, 700);

  // applySettings puts settings on the page: the sections, the cuts and the fades.
  function applySettings(set, { show = true } = {}) {
    ed.restoring = true;
    try {
      for (const x of [...sections, ...cuts]) x.region.remove();
      sections.length = cuts.length = 0;
      for (const x of set.sections) ed.addSection(x.start, x.end, x.label, x.pause);
      for (const c of set.cuts) ed.addCut(c.start, c.end, c.label, c.pause);
      if (sections.length && show) ed.reveal(sections[0].region.start);
      for (const key of ['fadeIn', 'fadeOut']) opts[key] = set[key];
      for (const key of CLIP_KEYS) opts[key] = set[key] || '';
      ed.drawClips();
      if (set.sound) {
        Object.assign(opts, set.sound);
        ed.redrawProcessing();
      }
    } finally {
      ed.restoring = false;
    }
    ed.renderSections();
    ed.renderCuts();
    ed.check();
  }

  // ---- undo and redo ----

  const undoStack = [], redoStack = [];
  let present = null; // the edit as it was last recorded
  let pointerHeld = false; // a drag is going on: it becomes one step when it ends

  // snapshot is the edit on the page, also when there is no section.
  const snapshot = () => JSON.stringify({ sections: sections.map(spanOf), cuts: cuts.map(spanOf), fadeIn: opts.fadeIn || 0, fadeOut: opts.fadeOut || 0,
    sound: Object.fromEntries(SOUND_KEYS.map((k) => [k, opts[k]])), ...ed.clipChoices() });

  // record makes the edit as it is now a step that can be undone, if it changed.
  function record() {
    recordSoon.cancel();
    if (pointerHeld || ed.restoring || present === null) return;
    const now = snapshot();
    if (now === present) return;
    undoStack.push(present);
    if (undoStack.length > 200) undoStack.shift();
    redoStack.length = 0;
    present = now;
    showHistory();
  }
  const recordSoon = debounce(() => { if (ed.alive) record(); }, 400);
  window.addEventListener('pointerdown', onPointerDown, true);
  window.addEventListener('pointerup', onPointerUp, true);
  cleanups.push(() => {
    window.removeEventListener('pointerdown', onPointerDown, true);
    window.removeEventListener('pointerup', onPointerUp, true);
  });
  function onPointerDown() { pointerHeld = true; }
  function onPointerUp() { pointerHeld = false; recordSoon(); }

  function step(from, to) {
    record(); // a change not recorded yet comes first
    if (!from.length) return;
    to.push(present);
    present = from.pop();
    applySettings(JSON.parse(present), { show: false });
    if (ed.proposed) ed.adopt();
    showHistory();
    persistAdjusted(); // putting settings back is not kept by itself
  }
  const undo = () => step(undoStack, redoStack);
  const redo = () => step(redoStack, undoStack);

  function showHistory() {
    undoButton.disabled = !undoStack.length;
    redoButton.disabled = !redoStack.length;
    keepHistory();
  }

  // startHistory starts undo from the edit the page opened with (what undo goes back to, at most), with the
  // steps kept in this browser.
  function startHistory() {
    present = snapshot();
    loadHistory();
  }

  // The steps are kept in this browser, so they outlive a reload: for the last recordings edited, and only
  // while they lead to the edit the page opens with. Changed elsewhere (another browser or computer), they
  // would undo into something else, and are dropped.
  const historyKey = 'history:' + currentTenant() + ':' + path;
  function loadHistory() {
    try {
      const kept = JSON.parse(localStorage.getItem(historyKey));
      if (kept && kept.present === present) {
        undoStack.push(...kept.undo);
        redoStack.push(...kept.redo);
        showHistory();
      }
      // Only the last 20 recordings' steps are kept.
      const all = Object.keys(localStorage).filter((k) => k.startsWith('history:'))
        .map((k) => [k, JSON.parse(localStorage.getItem(k))?.at || 0]).sort((a, b) => b[1] - a[1]);
      for (const [k] of all.slice(20)) localStorage.removeItem(k);
    } catch { /* none kept, or not readable: start without */ }
  }
  // keepHistory is called with every step (they come at most every 0.4 s), so a reload loses none.
  function keepHistory() {
    if (!ed.alive || present === null) return;
    try {
      localStorage.setItem(historyKey, JSON.stringify({ present, undo: undoStack, redo: redoStack, at: Date.now() }));
    } catch { /* the browser's storage is full: undo still works until the page is left */ }
  }

  // ctrl+z undoes, ctrl+shift+z and ctrl+y redo; in a field they are the field's own.
  function onUndoKey(e) {
    if (typing(e) || !(e.ctrlKey || e.metaKey) || e.altKey || document.querySelector('dialog[open]')) return;
    const key = e.key.toLowerCase();
    if (key === 'z' && !e.shiftKey) undo();
    else if ((key === 'z' && e.shiftKey) || key === 'y') redo();
    else return;
    e.preventDefault();
  }
  document.addEventListener('keydown', onUndoKey);
  cleanups.push(() => document.removeEventListener('keydown', onUndoKey));

  // history is how many steps there are to undo and redo (for the tests).
  function history() {
    record();
    return { undo: undoStack.length, redo: redoStack.length };
  }

  // ---- the versions: saved, and adjusted since ----

  const describe = (set) => [
    set.sections.map((x) => t('{from} to {to}', { from: formatTime(x.start, 0), to: formatTime(x.end, 0) })).join(' ' + t('and') + ' '),
    tn(set.cuts.length, '{n} cut', '{n} cuts'),
    ...(set.sections.some((x) => x.pause > 0) || set.cuts.some((x) => x.pause > 0) ? [t('with pauses')] : []),
    set.fadeIn || set.fadeOut ? t('fade {in} s in, {out} s out', { in: set.fadeIn, out: set.fadeOut }) : t('no fades'),
  ].join(', ');

  // renderVersion shows in the bar whether the edit is saved; the versions themselves are in its tooltip.
  function renderVersion() {
    const { saved, adjusted } = ed.sermon;
    const at = ed.sermon.savedAt ? ' ' + formatDateTime(ed.sermon.savedAt) : '';
    versionOut.textContent = adjusted ? t('Adjusted, not saved') : saved ? t('Saved') + at : t('Nothing is saved yet.');
    versionOut.className = adjusted ? 'unsaved' : '';
    versionOut.title = [
      saved ? t('Saved') + at + ': ' + describe(saved) : t('Nothing is saved yet.'),
      adjusted ? t('Adjusted, not saved:') + ' ' + describe(adjusted) : '',
    ].filter(Boolean).join('\n');
    revertButton.disabled = !adjusted || !saved;
    ed.renderOutputs();
  }

  // revertVersion drops the adjustments and goes back to the saved version, after asking.
  async function revertVersion() {
    const { saved, adjusted } = ed.sermon;
    if (!adjusted || !saved) return;
    const ok = await confirmDialog({
      title: t('Go back to the saved version?'),
      body: [
        h('p', {}, t('Your adjustments are discarded:')), h('ul', {}, h('li', {}, describe(adjusted))),
        h('p', {}, t('Back to the saved version:')), h('ul', {}, h('li', {}, describe(saved))),
      ],
      confirm: t('Revert'),
      danger: true,
    });
    if (!ok) return;
    try {
      ed.sermon = await api('POST', 'api/sermon/revert?path=' + q(path));
      ed.lastSent = JSON.stringify(ed.sermon.saved);
      applySettings(ed.sermon.saved);
      record();
      saveNote.textContent = t('Back to the saved version.');
    } catch (e) {
      saveNote.textContent = t('Not reverted: {error}', { error: e.message });
    }
    renderVersion();
  }

  // setSaveNote tells in the bar how keeping or saving went.
  const setSaveNote = (text) => { saveNote.textContent = text; };

  return {
    saveState: h('div', { class: 'save-state' }, versionOut, ' ', saveNote), undoButton, redoButton, revertButton,
    currentSettings, persistAdjusted, saveSoon, applySettings, record, recordSoon, undo, redo, startHistory, history,
    renderVersion, setSaveNote,
  };
}
