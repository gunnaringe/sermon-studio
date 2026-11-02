// Transcripts: what is said in a sermon as text and captions, made by the studio from the files
// (web/transcribe.go) with the speech models in its models folder. transcriptSettings is the church's choice
// of language (Settings → Processing); transcriptPanel is a sermon's transcript, in its details on the
// recordings page and in the editor, where modelPicker chooses the language and the model it is made with.
import { api, q, h, can, formatDate, forTenant } from './util.js';
import { confirmDialog } from './dialog.js';
import { t, tr } from './i18n.js';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const fileURL = (f, download) => forTenant('api/file?' + (download ? 'download=1&' : '') + 'path=' + q(f.path));

// languageName is a language a church can have its transcripts in, by its code ('*': whisper finds it).
export function languageName(code) {
  return ({
    nb: t('Norwegian Bokmål'), nn: t('Norwegian Nynorsk'), sv: t('Swedish'), da: t('Danish'), en: t('English'),
    '*': t('Detect automatically'),
  })[code] || code;
}

// modelName is a model with the date of its version, when known.
const modelName = (m) => m.date ? t('{model} from {date}', { model: m.name, date: formatDate(m.date) }) : m.name;

// transcriptSettings fills box with the church's choice of the language of its transcripts (none = off),
// from the languages the studio has models for.
export async function transcriptSettings(box) {
  const data = await api('GET', 'api/transcription');
  if (!data.available) {
    box.replaceChildren();
    return;
  }
  const status = h('span', { class: 'muted', role: 'status' });
  const model = h('p', { class: 'muted transcript-model' });
  const known = data.languages.some((l) => l.code === data.language);
  const select = h('select', { name: 'transcript-language', 'aria-label': t('Language of the sermons') },
    h('option', { value: '' }, t('No transcripts')),
    data.languages.map((l) => h('option', { value: l.code, selected: data.language === l.code }, languageName(l.code))),
    // A language chosen earlier whose model is gone: kept, and said.
    data.language && !known ? h('option', { value: data.language, selected: true }, languageName(data.language)) : '');
  const draw = (d) => {
    const l = d.languages.find((x) => x.code === d.language);
    model.textContent = l ? t('Model: {model}.', { model: modelName(l.model) })
      : d.language ? t('No speech model for this language is installed.') : '';
  };
  select.addEventListener('change', async () => {
    status.textContent = '';
    try {
      draw(await api('PUT', 'api/transcription', { language: select.value }));
      status.textContent = t('Saved.');
    } catch (err) { status.textContent = err.message; }
  });
  draw(data);
  box.replaceChildren(
    h('h3', {}, t('Transcripts')),
    data.languages.length
      ? h('p', { class: 'muted' }, t('A transcript of every sermon, as text and captions, made on the server after the files are made. It can take longer than the sermon. Names and Bible references can come out wrong: read it before you publish it.'))
      : h('p', { class: 'muted' }, t('No speech models are installed, so there are no transcripts yet.')),
    data.languages.length || data.language ? h('div', { class: 'panel tags' },
      h('label', {}, h('span', { class: 'name' }, t('Language of the sermons')), h('span', {}, select, ' ', status)),
      model) : '');
}

// transcriptPanel is a sermon's transcript (info.transcript from api/folder): the text to read, its files to
// download, and how far one being made is, as a bar with ✕ to stop it. It asks for the folder again by itself
// while one is being made, so the page around it is not drawn again. In the recordings' details it has its
// own Make (again), in the language the transcript is in, else the church's; in the editor (chosen) the
// editor's model picker makes it, and the panel tells it the state (onDraw).
/** @param {string} path @param {any} info @param {{ chosen?: boolean, onDraw?: (tx: any) => void }} [o] */
export function transcriptPanel(path, info, { chosen = false, onDraw = (_tx) => {} } = {}) {
  const box = h('div', { class: 'transcript' });
  const busy = (tx) => tx && (tx.state === 'waiting' || tx.state === 'running');
  const mayMake = can('recording.transcribe');
  let polling = false;
  let current = info.transcript;
  const act = (verb, query = '') => async () => {
    try {
      draw((await api('POST', 'api/transcript' + verb + '?path=' + q(path) + query)).transcript);
    } catch (err) {
      box.append(h('p', { class: 'error' }, err.message));
    }
  };
  const poll = async () => {
    if (polling) return;
    polling = true;
    while (box.isConnected) {
      await sleep(2000);
      let tx;
      try { tx = (await api('GET', 'api/folder?path=' + q(path))).transcript; } catch { break; }
      if (!busy(tx)) {
        polling = false;
        draw(tx);
        return;
      }
      draw(tx);
    }
    polling = false;
  };
  // Make again in the language it is in (or was being made in), with the model for it.
  const makeButton = (tx, label) => mayMake && !chosen
    ? h('button', { type: 'button', name: 'make-transcript', onclick: act('', tx.spoken ? '&language=' + q(tx.spoken) : '') }, label) : '';

  // progressBar is one being made: what it is doing, the bar (moving, while it waits) and ✕ to stop it.
  function progressBar(tx) {
    const percent = Math.round(100 * (tx.progress || 0));
    const what = tx.state === 'waiting' ? t('Waiting for its turn…') : t('Transcribing… {percent}%', { percent });
    const bar = h('progress', { max: 1, 'aria-label': t('Transcript') });
    if (tx.state === 'running') bar.value = tx.progress || 0;
    return h('div', { class: 'transcript-progress', 'aria-live': 'polite' },
      h('span', { class: 'what' }, chosen ? '' : [h('strong', {}, t('Transcript')), ' '], h('span', { class: 'muted' }, what)),
      h('span', { class: 'bar' }, bar,
        mayMake ? h('button', { type: 'button', class: 'x', name: 'stop-transcript', title: t('Stop'), 'aria-label': t('Stop the transcript'), onclick: act('/cancel') }, '✕') : ''));
  }

  function draw(tx) {
    current = tx;
    onDraw(tx);
    if (!tx || (!chosen && !tx.language && tx.state !== 'done' && !busy(tx))) {
      box.replaceChildren();
      return;
    }
    if (busy(tx)) {
      box.replaceChildren(progressBar(tx));
      poll();
      return;
    }
    if (tx.state === 'failed') {
      // ffmpeg's error is its first line; what it wrote follows, folded away.
      const [first, ...rest] = tx.error.split('\n');
      box.replaceChildren(h('p', {}, h('span', { class: 'error' }, t('The transcript was not made: {error}', { error: tr(first.replace(/:$/, '')) })), ' ',
        tx.canMake ? makeButton(tx, t('Try again')) : ''),
      rest.length ? h('details', { class: 'transcript-log' }, h('summary', {}, t('Log')), h('pre', { class: 'log' }, rest.join('\n').trim())) : '');
      return;
    }
    if (tx.state !== 'done') {
      box.replaceChildren(tx.canMake ? h('p', {}, makeButton(tx, t('Make transcript'))) : '');
      return;
    }
    // Made from an earlier cut, or with another model than the one there now: said, and made again only
    // when asked.
    const notes = [
      tx.earlierCut ? t('Made from an earlier cut.') : '',
      tx.otherModel && tx.model ? t('Made with {model}.', { model: modelName(tx.model) }) : '',
    ].filter(Boolean);
    const text = h('div', { class: 'transcript-text' });
    const details = h('details', {},
      h('summary', {}, t('Transcript'), notes.length ? h('span', { class: 'badge warn' }, notes.join(' ')) : ''));
    details.addEventListener('toggle', async () => {
      if (!details.open || text.childElementCount) return;
      try {
        const res = await fetch(fileURL(tx.text));
        if (!res.ok) throw new Error(res.statusText);
        const paragraphs = (await res.text()).split(/\n\s*\n/).map((p) => p.trim()).filter(Boolean);
        text.replaceChildren(...(paragraphs.length ? paragraphs.map((p) => h('p', {}, p)) : [h('p', { class: 'muted' }, t('Nothing was heard.'))]));
      } catch (err) { text.replaceChildren(h('p', { class: 'error' }, err.message)); }
    });
    const remove = async () => {
      if (!await confirmDialog({ title: t('Delete transcript'), body: h('p', {}, t('Delete the transcript of this sermon? It can be made again.')), confirm: t('Delete'), danger: true })) return;
      await act('/remove')();
    };
    details.append(
      h('p', { class: 'actions transcript-actions' },
        tx.text ? h('a', { class: 'button', href: fileURL(tx.text, true), download: '' }, t('Download text')) : '',
        tx.captions ? h('a', { class: 'button', href: fileURL(tx.captions, true), download: '' }, t('Download captions (WebVTT)')) : '',
        tx.canMake && !notes.length ? makeButton(tx, t('Make again')) : '', // else it is beside the notice
        mayMake ? h('button', { type: 'button', name: 'delete-transcript', onclick: remove }, t('Delete')) : ''),
      tx.model && !tx.otherModel ? h('p', { class: 'muted' }, t('Made with {model}.', { model: modelName(tx.model) })) : '',
      text);
    box.replaceChildren(details, notes.length && tx.canMake && !chosen ? h('p', { class: 'transcript-note' }, makeButton(tx, t('Make again'))) : '');
  }

  draw(info.transcript);
  return {
    box,
    // update draws it from the folder read again, unless it is following one being made itself.
    update: (tx) => { if (!polling) draw(tx); },
    // make makes it in language with the model in file.
    make: (language, file) => act('', '&language=' + q(language) + '&model=' + q(file))(),
    get transcript() { return current; },
  };
}

// The sizes of Whisper, smallest first: the order of the size filter.
const SIZES = ['tiny', 'base', 'small', 'medium', 'large', 'large-v1', 'large-v2', 'large-v3', 'turbo', 'large-v3-turbo'];

// canDo tells whether a model can transcribe lang: it is for it, or for any language ('*': only those).
const canDo = (m, lang) => m.languages.includes(lang) || (lang !== '*' && m.languages.includes('*'));
// usualModel is the model the studio takes for lang by itself (models.Choose).
const usualModel = (list, lang) => (lang !== '*' && list.find((m) => m.languages.includes(lang))) || list.find((m) => m.languages.includes('*'));
const megabytes = (bytes) => bytes >= 1e6 ? t('{n} MB', { n: Math.round(bytes / 1e6) }) : '';

// modelPicker is the editor's choice of the model a transcript is made with: every installed model, filtered by
// the language of the sermon (which whisper is told too), the size and the name; the one the studio would
// take for the language is chosen to begin with, and marked. Make makes it with the one chosen (panel.make).
export function modelPicker(data, panel) {
  const models = data.models || [];
  const tx0 = panel.transcript || {};
  const langs = data.languages.map((l) => l.code);
  let lang = [tx0.spoken, data.language].find((l) => l && langs.includes(l)) || (langs.includes('*') ? '*' : langs[0]);
  let size = '', name = '';
  let file = tx0.model && models.some((m) => m.file === tx0.model.file && canDo(m, lang)) ? tx0.model.file : '';
  const mayMake = can('recording.transcribe');

  const list = h('div', { class: 'model-list', role: 'radiogroup', 'aria-label': t('Speech model') });
  const note = h('p', { class: 'muted' });
  const makeButton = h('button', { type: 'button', class: 'primary', name: 'make-transcript', onclick: () => panel.make(lang, file) }, t('Make transcript'));
  const select = (nm, label, options, value, onchange) => h('label', {}, h('span', { class: 'name' }, label),
    h('select', { name: nm, onchange: (e) => { onchange(e.target.value); draw(); } },
      options.map(([v, text]) => h('option', { value: v, selected: v === value }, text))));
  const sizes = SIZES.filter((s) => models.some((m) => m.size === s));
  const filters = h('div', { class: 'options model-filters' },
    select('transcript-language', t('Language of the sermon'), langs.map((l) => [l, languageName(l)]), lang, (v) => { lang = v; }),
    select('model-size', t('Size'), [['', t('All sizes')], ...sizes.map((s) => [s, s])], size, (v) => { size = v; }),
    h('label', {}, h('span', { class: 'name' }, t('Name')), h('input', { type: 'text', name: 'model-name', placeholder: t('Filter by name'),
      oninput: (e) => { name = e.target.value.trim().toLowerCase(); draw(); } })));

  function draw() {
    const usual = usualModel(models, lang);
    const shown = models.filter((m) => canDo(m, lang) && (!size || m.size === size) &&
      (!name || (m.name + ' ' + (m.repo || '')).toLowerCase().includes(name)));
    // The one chosen stays while it is there; else the usual one, else the first.
    if (!shown.some((m) => m.file === file)) file = (shown.find((m) => usual && m.file === usual.file) || shown[0] || {}).file || '';
    list.replaceChildren(...(shown.length ? shown.map((m) => h('label', { class: 'model' + (m.file === file ? ' chosen' : '') },
      h('input', { type: 'radio', name: 'transcript-model', value: m.file, checked: m.file === file, onchange: () => { file = m.file; draw(); } }),
      h('span', { class: 'model-name' }, m.name, usual && m.file === usual.file ? h('span', { class: 'badge' }, t('usual for the language')) : ''),
      h('span', { class: 'muted model-facts' }, [m.size, m.languages.map(languageName).join(', '), megabytes(m.bytes),
        m.date ? formatDate(m.date) : ''].filter(Boolean).join(' · '))))
      : [h('p', { class: 'muted' }, t('No model matches.'))]));
    const tx = panel.transcript || {};
    const busy = tx.state === 'waiting' || tx.state === 'running';
    makeButton.textContent = tx.state === 'done' ? t('Make again') : tx.state === 'failed' ? t('Try again') : t('Make transcript');
    makeButton.disabled = busy || !file || !tx.hasFiles;
    note.textContent = !tx.hasFiles ? t('Save makes the files first: the transcript is made from them.')
      : tx.state === 'done' ? t('Made again, it replaces the one there.') : t('It runs on the server and can take longer than the sermon.');
  }
  draw();
  return {
    el: h('div', { class: 'panel model-picker' }, panel.box, filters, list, mayMake ? h('p', { class: 'actions' }, makeButton) : '', note),
    draw,
  };
}
