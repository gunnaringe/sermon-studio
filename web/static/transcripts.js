// Transcripts: what is said in a sermon as text and captions, made by the studio from the files
// (web/transcribe.go) with the speech models in its models folder. transcriptSettings is the church's choice
// of language (Settings → Processing); transcriptPanel is a sermon's transcript in its details on the
// recordings page.
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

// transcriptPanel is a sermon's transcript in its details (info.transcript from api/folder): the text to
// read, its files to download, and how far one being made is. It asks for the folder again by itself
// while one is being made, so the player above is not drawn again.
export function transcriptPanel(path, info) {
  const box = h('div', { class: 'transcript' });
  const busy = (tx) => tx && (tx.state === 'waiting' || tx.state === 'running');
  const mayMake = can('recording.transcribe');
  let polling = false;
  const act = (verb) => async () => {
    try {
      draw((await api('POST', 'api/transcript' + verb + '?path=' + q(path))).transcript);
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
  const makeButton = (label) => mayMake ? h('button', { type: 'button', name: 'make-transcript', onclick: act('') }, label) : '';

  function draw(tx) {
    if (!tx || (!tx.language && tx.state !== 'done')) {
      box.replaceChildren();
      return;
    }
    if (busy(tx)) {
      const what = tx.state === 'waiting' ? t('Transcript: waiting for its turn…')
        : t('Transcript: being made… {percent}%', { percent: Math.round(100 * (tx.progress || 0)) });
      box.replaceChildren(h('p', { class: 'muted', 'aria-live': 'polite' }, what, ' ',
        mayMake ? h('button', { type: 'button', name: 'stop-transcript', onclick: act('/cancel') }, t('Stop')) : ''));
      poll();
      return;
    }
    if (tx.state === 'failed') {
      box.replaceChildren(h('p', {}, h('span', { class: 'error' }, t('The transcript was not made: {error}', { error: tr(tx.error) })), ' ',
        tx.canMake ? makeButton(t('Try again')) : ''));
      return;
    }
    if (tx.state !== 'done') {
      box.replaceChildren(tx.canMake ? h('p', {}, makeButton(t('Make transcript'))) : '');
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
        tx.canMake && !notes.length ? makeButton(t('Make again')) : '', // else it is beside the notice
        mayMake ? h('button', { type: 'button', name: 'delete-transcript', onclick: remove }, t('Delete')) : ''),
      tx.model && !tx.otherModel ? h('p', { class: 'muted' }, t('Made with {model}.', { model: modelName(tx.model) })) : '',
      text);
    box.replaceChildren(details, notes.length && tx.canMake ? h('p', { class: 'transcript-note' }, makeButton(t('Make again'))) : '');
  }

  draw(info.transcript);
  return { box };
}
