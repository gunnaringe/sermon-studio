// The editor for one date folder: cut the sermon on the waveform, set the options, save, and follow the job.
// Its parts are in editor/, each given the editor (ed, Editor in editor/common.js): this puts them together.
import { t } from './i18n.js';
import { api, q, h, href, go, dateInput, can } from './util.js';
import { formDialog } from './dialog.js';
import { CLIP_KEYS, FORMAT_KEYS } from './editor/common.js';
import { keptLength, activePauses } from './editor/spans.js';
import { infoPart } from './editor/info.js';
import { waveformPart } from './editor/waveform.js';
import { sectionsPart } from './editor/sections.js';
import { cutsPart } from './editor/cuts.js';
import { fadesPart } from './editor/fades.js';
import { processingPart } from './editor/processing.js';
import { clipsPart } from './editor/clips.js';
import { historyPart } from './editor/history.js';
import { outputPart } from './editor/output.js';
import { transcriptPart } from './editor/transcript.js';

export async function editorView(view, slug, config) {
  const { path, slug: own } = await api('GET', 'api/recording?slug=' + q(slug));
  if (own !== slug) go(href('recordings', own + '/edit'), true); // an old address, from before a rename
  const info = await api('GET', 'api/folder?path=' + q(path));
  let alive = true;
  const cleanups = [];
  const teardown = () => {
    alive = false;
    for (const fn of cleanups) fn();
  };

  const opts = { ...config.defaults.start }; // the church's defaults
  // The processing kept with this recording comes before them.
  const keptSound = info.sermon && (info.sermon.adjusted || info.sermon.saved)?.sound;
  if (keptSound) Object.assign(opts, keptSound);
  // The intro and outro: a new sermon starts with the church's defaults, one kept from before without them has none.
  const kept = info.sermon && (info.sermon.adjusted || info.sermon.saved);
  if (kept) for (const which of CLIP_KEYS) opts[which] = kept[which] || '';
  // The formats are this recording's own: those it has, or the defaults when it
  // has none. Choosing here changes nothing elsewhere.
  if (info.outputs.length) for (const f of FORMAT_KEYS) opts[f] = info.outputs.some((o) => o.format === f);
  // Tags are per sermon: the church's defaults for this folder, editable for this run.
  const tags = { title: '', artist: '', album: '', ...(info.tags || {}) };
  const sections = [], cuts = [];

  // Another date or name for this sermon: renames the folder and its files. A pencil beside the title, a dialog.
  const renameButton = h('button', { type: 'button', class: 'title-action', title: t('Change the date or name of this sermon'), 'aria-label': t('Rename…'), onclick: async () => {
    const [, d = '', n = ''] = info.date.match(/^(\d{4}-\d{2}-\d{2})(?: (.*))?$/) || [];
    const date = dateInput({ name: 'date', value: d, required: true, 'aria-label': t('Date') });
    const name = h('input', { type: 'text', value: n, maxlength: 60, placeholder: t('optional, e.g. Evening'), 'aria-label': t('Name') });
    const res = await formDialog({
      title: t('Change the date or name of this sermon'),
      body: [
        h('div', { class: 'form-rows' }, h('label', {}, h('span', {}, t('Date')), date), h('label', {}, h('span', {}, t('Name')), name)),
        h('p', { class: 'muted' }, t('The folder and its files are renamed. Files already made keep their tags.')),
      ],
      confirm: t('Rename'),
      submit: () => api('POST', 'api/rename?path=' + q(path), { date: date.value, name: name.value }),
    });
    if (res) go(href('recordings', res.slug + '/edit'), true);
  } });
  renameButton.innerHTML = '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path d="M17 3a2.828 2.828 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5L17 3z"/></svg>'; // Feather's edit-2 (MIT)

  const ed = /** @type {import('./editor/common.js').Editor} */ ({
    view, path, info, config, opts, tags, cleanups, sections, cuts,
    get alive() { return alive; },
    ws: null, regions: null, duration: 0, // the waveform, once it is there
    sermon: info.sermon || { saved: null, adjusted: null },
    restoring: false, // putting settings back is not a change
    proposed: false, // the one section is the start the page made, untouched (see proposeSection)
    changed,
    keptLength: () => keptLength(sections, cuts),
    activePauses: () => activePauses(sections, cuts),
  });
  ed.lastSent = JSON.stringify(ed.sermon.adjusted || ed.sermon.saved);
  const inputFile = infoPart(ed), waveform = waveformPart(ed), processing = processingPart(ed), clips = clipsPart(ed),
    history = historyPart(ed), output = outputPart(ed), transcript = transcriptPart(ed);
  Object.assign(ed, inputFile, waveform, sectionsPart(ed), cutsPart(ed), fadesPart(ed), processing, clips, history, output, transcript);

  // changed: the edit changed. It is checked, kept in a moment, and becomes a step of undo.
  function changed() {
    if (ed.proposed && !ed.restoring) ed.adopt();
    if (!ed.restoring) ed.recordSoon();
    ed.showDefaults();
    ed.check();
    ed.saveSoon();
  }

  // The bar under the page: what the edit is (saved or not), what Save does, Revert and Save. It sticks to
  // the bottom of the window, so Save is at hand wherever the page is scrolled.
  // A phone shows one line (the state, undo, redo, Save); the button opens the rest (what Save does, Revert).
  const saveMore = h('button', { type: 'button', class: 'narrow-only more', 'aria-expanded': 'false', 'aria-label': t('More'), title: t('More'),
    onclick: () => saveMore.setAttribute('aria-expanded', String(saveBar.classList.toggle('open'))) }, '⋯');
  history.revertButton.classList.add('revert');
  const saveBar = h('div', { class: 'save-bar' }, history.saveState, output.saveWhat,
    h('div', { class: 'save-actions' }, history.undoButton, history.redoButton, history.revertButton, saveMore, output.saveButton));
  // The steps set once and seldom changed are folded: the heading opens one, and a line under it says what is set.
  // Which are open is kept in this browser (localStorage editorOpen), as Settings keeps its groups.
  const opened = new Set(JSON.parse(localStorage.getItem('editorOpen') || 'null') ?? []);
  // own: a step whose folding is its own (Edit, by the recording's state), not kept in the browser. keep: folded, it
  // keeps its width (the waveform is drawn to it) and is only out of sight.
  function foldable(section, summarize, mustShow = () => false, { own = null, keep = false } = {}) {
    const head = section.querySelector('h2');
    const summary = h('p', { class: 'muted fold-summary' });
    const toggle = h('button', { type: 'button', class: 'fold-toggle', onclick: () => {
      if (own) own.open = !own.open;
      else {
        if (opened.has(head.id)) opened.delete(head.id); else opened.add(head.id);
        localStorage.setItem('editorOpen', JSON.stringify([...opened]));
      }
      draw();
    } }, ...head.childNodes);
    head.replaceChildren(toggle);
    head.after(summary);
    section.classList.add('fold');
    if (keep) section.classList.add('keep');
    let drawing = false;
    function draw() {
      if (drawing) return;
      drawing = true;
      const open = (own ? own.open : false) || opened.has(head.id) || mustShow(); // a warning inside is never folded away
      section.classList.toggle('folded', !open);
      toggle.setAttribute('aria-expanded', String(open));
      const text = open ? '' : summarize();
      if (summary.textContent !== text) summary.textContent = text;
      queueMicrotask(() => { drawing = false; });
    }
    // What is set changes by typing, by undo and by what the server answers: follow the step itself.
    section.addEventListener('input', draw);
    section.addEventListener('change', draw);
    const watch = new MutationObserver(draw);
    watch.observe(section, { subtree: true, childList: true, attributes: true, attributeFilter: ['hidden', 'aria-pressed'] });
    cleanups.push(() => watch.disconnect());
    draw();
    return section;
  }
  const step = (id, title, ...body) => h('section', { class: 'step', 'aria-labelledby': id }, h('h2', { id }, title), ...body);
  const metadataStep = foldable(step('step-metadata', t('Metadata added to the file'), processing.metadataEl),
    () => ['title', 'artist', 'album'].map((k) => tags[k]).filter(Boolean).join(' · ') || t('No tags'));
  const processingStep = foldable(step('step-processing', t('Processing'), processing.processingEl),
    () => [opts.stereo ? t('Stereo') : t('Mono'), processing.defaultsNote.textContent].filter(Boolean).join(' · '),
    () => !processing.channelWarning.hidden);
  foldable(clips.clipsSection, () => [...clips.clipsSection.querySelectorAll('select')]
    .map((sel) => sel.getAttribute('aria-label') + ': ' + (sel.selectedOptions[0]?.textContent || '')).join(' · '));
  const deleteStep = can('recording.delete') ? foldable(step('step-delete', t('Danger zone'), inputFile.deletePanel), () => '') : '';
  // Edit is open while there is something to do in it: nothing saved yet, adjustments not saved, or files that
  // are out of date. Otherwise the page starts at what was made. Decided when the page opens: it does not fold
  // under the one editing when Save ends.
  const version = info.sermon || {};
  const editOpen = { open: !version.saved || Boolean(version.adjusted) || (info.outputs || []).some((o) => o.stale) };
  const editStep = foldable(step('step-edit', t('Edit'), waveform.waveSection),
    () => { const set = ed.sermon.adjusted || ed.sermon.saved; return set ? history.describe(set) : ''; },
    () => false, { own: editOpen, keep: true });

  view.replaceChildren(
    h('div', { class: 'crumbs' }, h('a', { href: href('recordings', own) }, t('← Recordings'))),
    h('h1', {}, info.date, ' ', renameButton),
    h('section', { class: 'step', 'aria-labelledby': 'step-input' }, h('h2', { id: 'step-input' }, t('Input file')), inputFile.inputPanel),
    editStep,
    metadataStep,
    processingStep,
    clips.clipsSection,
    output.outputEl,
    transcript.transcriptEl,
    deleteStep,
    saveBar,
  );

  inputFile.showInput();
  inputFile.waitForFacts();
  output.showJobs();
  transcript.showTranscript();
  waveform.showWaveform();
  inputFile.renderDelete();
  // Without the original there is nothing left to edit: the page has what it was made into, and Delete.
  const gone = !info.raw && info.originalDeleted;
  if (gone) {
    for (const id of ['step-edit', 'step-metadata', 'step-processing', 'step-clips']) view.querySelector('#' + id).closest('section').remove();
    saveBar.remove();
  }
  ed.renderVersion();
  ed.renderOutputs();
  if (!gone) {
    ed.check();
    clips.loadClips();
  }
  return teardown;
}
