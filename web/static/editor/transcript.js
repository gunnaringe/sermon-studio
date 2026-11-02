// The editor's transcript: whether Save makes one of the sermon, in which language and with which model (the
// church's for the language, unless changed: modelLine), and the one there or being made (transcriptPanel). It
// starts as the recording has it: on, in its language and with its model, when it has one; else as the church's
// default. Like a format, Save makes it
// when it is on and the one there is not of this cut, language and model (after the files, when it makes
// them), and deletes it when it is off. It is never made from the recording, only from the files of the cut.
import { t } from '../i18n.js';
import { api, q, h, can } from '../util.js';
import { transcriptPanel, modelLine, languageName } from '../transcripts.js';

/** @param {import('./common.js').Editor} ed */
export function transcriptPart(ed) {
  const { path, info } = ed;
  // A place on the page, which becomes the section once there are models.
  let transcriptEl = h('div', { hidden: true });
  let chooser = null;
  let on = false;
  const panel = transcriptPanel(path, info, { chosen: true, onDraw: () => { if (chooser) { drawPill(); ed.renderOutputs(); } } });
  const pill = h('div', { class: 'pill' });

  const busy = (tx) => tx && (tx.state === 'waiting' || tx.state === 'running');

  // transcriptToDo is what Save does to the transcript: 'make', 'delete' or ''. stale (from the check) tells
  // whether the one there is of another cut than the edit's.
  function transcriptToDo(stale = false) {
    const tx = panel.transcript;
    if (!chooser || !tx || busy(tx)) return '';
    if (!on) return tx.state === 'done' && can('recording.delete') ? 'delete' : '';
    const same = tx.state === 'done' && !stale && tx.spoken === chooser.language && tx.model && tx.model.key === chooser.model;
    return same || !chooser.model ? '' : 'make';
  }

  function drawPill(stale = false) {
    const tx = panel.transcript || {};
    const todo = transcriptToDo(stale);
    const there = tx.state === 'done';
    const note = busy(tx) ? t('being made')
      : todo === 'make' ? (there ? t('out of date, will be made again') : t('will be made'))
        : todo === 'delete' ? t('will be deleted') : there ? t('up to date') : t('not made');
    pill.className = 'pill' + (there ? ' exists' : '') + (on ? ' chosen' : '') + (todo === 'make' && there ? ' stale' : '');
    pill.replaceChildren(
      h('button', { class: 'pick', name: 'transcript-on', 'aria-pressed': String(on), title: on ? t('Leave the transcript out') : t('Make a transcript too'),
        onclick: () => { on = !on; chooserEl.hidden = !on; drawPill(); ed.renderOutputs(); } },
      h('span', { class: 'box', 'aria-hidden': 'true' }, on ? '☑' : '☐'), ' ', t('Transcript'), there ? ' ✓' : ''),
      h('span', { class: 'state' + (todo === 'delete' ? ' delete' : todo === 'make' ? ' will' : '') }, note));
  }
  let chooserEl = h('div', {});

  // showTranscript shows it once the models are known: not without transcripts in the studio, or models.
  async function showTranscript() {
    if (!info.transcript) return;
    const data = await api('GET', 'api/transcription').catch(() => ({ available: false }));
    if (!data.available || !(data.models || []).length || !ed.alive) return;
    const tx = info.transcript;
    const own = tx.state === 'done' || busy(tx) || tx.state === 'failed';
    on = own || Boolean(data.language);
    const langs = data.languages.map((l) => l.code);
    let lang = own ? tx.spoken : data.language;
    if (!langs.includes(lang)) lang = langs.includes('*') ? '*' : langs[0];
    const changed = () => { drawPill(); ed.renderOutputs(); };
    const model = modelLine(data, { lang, key: own && tx.model ? tx.model.key : '', onChange: changed });
    const select = h('select', { name: 'transcript-language', onchange: (e) => { lang = e.target.value; model.setLanguage(lang); changed(); } },
      langs.map((l) => h('option', { value: l, selected: l === lang }, languageName(l))));
    chooser = { get language() { return lang; }, get model() { return model.model; } };
    chooserEl = h('div', { class: 'transcript-choice', hidden: !on },
      h('div', { class: 'options model-filters' }, h('label', {}, h('span', { class: 'name' }, t('Language of the sermon')), select)),
      model.el,
      h('p', { class: 'muted hint' }, t('Save makes it from the files of the cut sermon, after them. It runs on the server and can take longer than the sermon.')));
    drawPill();
    const section = h('section', { class: 'step', 'aria-labelledby': 'step-transcript' },
      h('h2', { id: 'step-transcript' }, t('Transcript')),
      h('div', { class: 'panel model-picker' }, panel.box, h('div', { class: 'pills' }, pill), chooserEl));
    transcriptEl.replaceWith(section);
    transcriptEl = section;
    ed.renderOutputs();
  }

  return {
    transcriptEl, showTranscript, transcriptToDo,
    renderTranscript: (tx) => panel.update(tx),
    // transcriptAsk is what a run is asked to make after the files; makeTranscript makes it without a run.
    transcriptAsk: () => ({ language: chooser.language, model: chooser.model }),
    makeTranscript: () => panel.make(chooser.language, chooser.model),
    removeTranscript: async () => panel.update((await api('POST', 'api/transcript/remove?path=' + q(path))).transcript),
    redrawTranscript: drawPill,
  };
}
