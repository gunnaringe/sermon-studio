// The editor for one date folder: cut the sermon on the waveform, set the options, save, and follow the job.
// Its parts are in editor/, each given the editor (ed, Editor in editor/common.js): this puts them together.
import { t } from './i18n.js';
import { api, q, h, href, go, dateInput } from './util.js';
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

  // Another date or name for this sermon: renames the folder and its files.
  const renamePanel = h('div', { class: 'panel', hidden: true });
  const renameButton = h('button', { class: 'link', title: t('Change the date or name of this sermon'), onclick: () => {
    const [, d = '', n = ''] = info.date.match(/^(\d{4}-\d{2}-\d{2})(?: (.*))?$/) || [];
    const date = dateInput({ name: 'date', value: d, required: true, 'aria-label': t('Date') });
    const name = h('input', { type: 'text', value: n, maxlength: 60, placeholder: t('optional, e.g. Evening'), 'aria-label': t('Name') });
    const msg = h('span', { class: 'error' });
    const save = h('button', { class: 'primary', onclick: async () => {
      try {
        const res = await api('POST', 'api/rename?path=' + q(path), { date: date.value, name: name.value });
        go(href('recordings', res.slug + '/edit'), true);
      } catch (err) { msg.textContent = err.message; }
    } }, t('Rename'));
    renamePanel.replaceChildren(
      h('label', {}, t('Date'), ' ', date), ' ', h('label', {}, t('Name'), ' ', name), ' ', save, ' ',
      h('button', { onclick: () => { renamePanel.hidden = true; } }, t('Cancel')), ' ', msg,
      h('p', { class: 'muted' }, t('The folder and its files are renamed. Files already made keep their tags.')));
    renamePanel.hidden = false;
  } }, t('Rename…'));

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
    history = historyPart(ed), output = outputPart(ed);
  Object.assign(ed, inputFile, waveform, sectionsPart(ed), cutsPart(ed), fadesPart(ed), processing, clips, history, output);

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
  const saveBar = h('div', { class: 'save-bar' }, history.saveState, output.saveWhat,
    h('div', { class: 'save-actions' }, history.undoButton, history.redoButton, history.revertButton, output.saveButton));
  view.replaceChildren(
    h('div', { class: 'crumbs' }, h('a', { href: href('recordings', own) }, t('← Recordings'))),
    h('h1', {}, info.date, ' ', renameButton),
    renamePanel,
    h('section', { class: 'step', 'aria-labelledby': 'step-input' }, h('h2', { id: 'step-input' }, t('Input file')), inputFile.inputPanel),
    h('section', { class: 'step', 'aria-labelledby': 'step-edit' }, h('h2', { id: 'step-edit' }, t('Edit')), waveform.waveSection),
    h('section', { class: 'step', 'aria-labelledby': 'step-metadata' }, h('h2', { id: 'step-metadata' }, t('Metadata added to the file')), processing.metadataEl),
    h('section', { class: 'step', 'aria-labelledby': 'step-processing' }, h('h2', { id: 'step-processing' }, t('Processing')), processing.processingEl),
    clips.clipsSection,
    output.outputEl,
    h('section', { class: 'step', 'aria-labelledby': 'step-delete' }, h('h2', { id: 'step-delete' }, t('Delete')), inputFile.deletePanel),
    saveBar,
  );

  inputFile.showInput();
  inputFile.waitForFacts();
  output.showJobs();
  waveform.showWaveform();
  inputFile.renderDelete();
  // Without the original there is nothing left to edit: the page has what it was made into, and Delete.
  const gone = !info.raw && info.originalDeleted;
  if (gone) {
    for (const id of ['step-edit', 'step-metadata', 'step-processing', 'step-clips']) view.querySelector('#' + id).parentElement.remove();
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
