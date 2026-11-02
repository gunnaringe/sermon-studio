// Transcripts: what is said in a sermon as text and captions, made by the studio from the files of the cut
// sermon (web/transcribe.go) with the speech models in its models folder. transcriptSettings is the church's
// defaults (Settings → Processing): the language, and the model for each language; modelField is the choice
// of a model, there and in the editor (editor/transcript.js). transcriptPanel is a sermon's transcript, in its
// details on the recordings page and in the editor.
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

// transcriptSettings fills box with the church's defaults for its recordings' transcripts: the language (none =
// Save makes none), and the model for each language. Saved as they are changed.
export async function transcriptSettings(box) {
  const data = await api('GET', 'api/transcription');
  if (!data.available) {
    box.replaceChildren();
    return;
  }
  const status = h('span', { class: 'muted', role: 'status' });
  const defaults = { ...(data.defaults || {}) };
  let language = data.language;
  const save = async () => {
    status.textContent = '';
    try {
      Object.assign(data, await api('PUT', 'api/transcription', { language, defaults }));
      status.textContent = t('Saved.');
    } catch (err) { status.textContent = err.message; }
  };
  const langs = data.languages.map((l) => l.code);
  const select = h('select', { name: 'transcript-language', 'aria-label': t('Language of the sermons'),
    onchange: (e) => { language = e.target.value; save(); } },
  h('option', { value: '' }, t('No transcripts')),
  langs.map((l) => h('option', { value: l, selected: language === l }, languageName(l))),
  // A language chosen earlier whose model is gone: kept, and said.
  language && !langs.includes(language) ? h('option', { value: language, selected: true }, languageName(language)) : '');
  const has = (data.models || []).length > 0;
  box.replaceChildren(
    h('h3', {}, t('Transcripts')),
    has
      ? h('p', { class: 'muted' }, t('Whether Save in the editor makes a transcript of the sermon, as text and captions, and in which language: the default for every recording, which it can change. It is made on the server after the files, and can take longer than the sermon. Names and Bible references can come out wrong: read it before you publish it.'))
      : h('p', { class: 'muted' }, t('No speech models are installed, so there are no transcripts yet.')),
    has || language ? h('div', { class: 'panel tags' },
      h('label', {}, h('span', { class: 'name' }, t('Language of the sermons')), h('span', {}, select, ' ', status))) : '',
    has ? h('div', { class: 'panel model-defaults' },
      h('h4', {}, t('Model for each language')),
      h('p', { class: 'muted' }, t('The model a transcript in the language is made with, unless the recording chooses another.')),
      langs.map((l) => h('div', { class: 'model-default' }, h('strong', {}, languageName(l)),
        modelField(data, { lang: l, key: defaults[l] || '', mark: false, onChange: (key) => { defaults[l] = key; save(); } }).el))) : '');
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

// defaultModel is the key of the model a transcript in lang is made with when none is chosen: the church's for
// the language while it is there, else the first for it (web/transcribe.go defaultModel).
export function defaultModel(data, lang) {
  const models = data.models || [];
  const key = (data.defaults || {})[lang];
  if (key && models.some((m) => m.key === key && canDo(m, lang))) return key;
  return (usualModel(models, lang) || {}).key || '';
}

// modelField is the model a transcript in a language is made with: its name, and under Change model every
// installed model that can do the language, filtered by size and name. The model is not one often changed:
// the default for the language is chosen to begin with (and when the language changes), and marked unless
// mark is false (the church's own choice is the default). onChange gets the model the user chose.
export function modelField(data, { lang, key = '', mark = true, onChange = (_model) => {} }) {
  const models = data.models || [];
  let size = '', name = '';
  const current = h('span', { class: 'model-current' });
  const list = h('div', { class: 'model-list', role: 'radiogroup', 'aria-label': t('Speech model') });
  const field = (label, input) => h('label', {}, h('span', { class: 'name' }, label), input);
  const sizes = SIZES.filter((s) => models.some((m) => m.size === s));
  const filters = h('div', { class: 'options model-filters' },
    field(t('Size'), h('select', { name: 'model-size', onchange: (e) => { size = e.target.value; draw(); } },
      [['', t('All sizes')], ...sizes.map((s) => [s, s])].map(([v, text]) => h('option', { value: v }, text)))),
    field(t('Name'), h('input', { type: 'text', name: 'model-name', placeholder: t('Filter by name'),
      oninput: (e) => { name = e.target.value.trim().toLowerCase(); draw(); } })));
  const details = h('details', { class: 'model-change' }, h('summary', {}, t('Change model')), filters, list);

  function draw() {
    const def = defaultModel(data, lang);
    if (!models.some((m) => m.key === key && canDo(m, lang))) key = def;
    const chosen = models.find((m) => m.key === key);
    current.replaceChildren(chosen ? t('Model: {model}.', { model: modelName(chosen) }) : t('No speech model for this language is installed.'),
      mark && chosen && key === def ? h('span', { class: 'badge' }, t('default')) : '');
    const shown = models.filter((m) => canDo(m, lang) && (!size || m.size === size) &&
      (!name || (m.name + ' ' + (m.repo || '')).toLowerCase().includes(name)));
    list.replaceChildren(...(shown.length ? shown.map((m) => h('label', { class: 'model' + (m.key === key ? ' chosen' : '') },
      h('input', { type: 'radio', name: 'transcript-model-' + lang, value: m.key, checked: m.key === key, onchange: () => { key = m.key; draw(); onChange(key); } }),
      h('span', { class: 'model-name' }, m.name, mark && m.key === def ? h('span', { class: 'badge' }, t('default')) : ''),
      h('span', { class: 'muted model-facts' }, [m.size, m.languages.map(languageName).join(', '), megabytes(m.bytes),
        m.date ? formatDate(m.date) : ''].filter(Boolean).join(' · '))))
      : [h('p', { class: 'muted' }, t('No model matches.'))]));
  }
  draw();
  return {
    el: h('div', { class: 'model-field' }, h('p', {}, current), details),
    get model() { return key; },
    // setLanguage changes the language: its default model is chosen.
    setLanguage(l) { lang = l; key = ''; draw(); },
  };
}
