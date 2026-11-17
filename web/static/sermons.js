// The details of a date folder in the folder list: what a sermon has (info,
// play, download, make a missing format), or, for a recording that has not
// been processed, only a way into the editor.
import { api, q, h, href, formatSize, formatTime, forTenant, timeLeft, liveBar, backgroundNote } from './util.js';
import { alertDialog, confirmDialog } from './dialog.js';
import { showCode, expiryInput, descriptionInput, recordingShares } from './shares.js';
import { t, tn, tr } from './i18n.js';
import { transcriptPanel } from './transcripts.js';
import { DOWNLOAD_ICON, iconButton, dropMenu } from './menu.js';

const FORMATS = [['flac', 'FLAC'], ['mp3', 'MP3'], ['opus', 'Opus']];
// SERMON_ONLY ends the format of the files without the intro and outro (flac_sermon ...).
const SERMON_ONLY = '_sermon';
const sermonOnly = (info) => info.outputs.filter((o) => o.format.endsWith(SERMON_ONLY));
const fileURL = (f, download) => forTenant('api/file?' + (download ? 'download=1&' : '') + 'path=' + q(f.path));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// SHARE_ICON is the header's share icon (Feather, MIT), for the buttons that share.
// EDIT_ICON, COPY_ICON and CHECK_ICON are Feather's edit-2, copy and check (MIT), on the icon buttons.
const SHARE_ICON = '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/>'
  + '<circle cx="18" cy="19" r="3"/><line x1="8.59" y1="13.51" x2="15.42" y2="17.49"/><line x1="15.41" y1="6.51" x2="8.59" y2="10.49"/></svg>';
const EDIT_ICON = '<svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><path d="M17 3a2.828 2.828 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5L17 3z"/></svg>';
const COPY_ICON = '<svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>'
  + '<path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>';
const CHECK_ICON = '<svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><polyline points="20 6 9 17 4 12"/></svg>';

// keptSeconds is what the saved version lasts: its sections without the cuts.
function keptSeconds(s) {
  const len = (x) => x.end - x.start;
  const inside = (c) => s.sections.reduce((n, x) => n + Math.max(0, Math.min(c.end, x.end) - Math.max(c.start, x.start)), 0);
  return s.sections.reduce((n, x) => n + len(x), 0) - s.cuts.reduce((n, c) => n + inside(c), 0);
}

// the format the one player plays: what every browser handles first
const PLAY_ORDER = ['mp3', 'opus', 'flac'];

const MAX_LABELS = 10;

// sermonDetail returns the panel for a folder; it fills in when the folder
// has been read, and again after a format was made. ctx.suggestions() lists
// the labels already used; ctx.changed() is told when this one's labels change. ctx.shares: the public share
// page is on. Deleting is on the edit page.
export function sermonDetail(entry, ctx = {}) {
  const panel = h('div', { class: 'detail' }, h('p', { class: 'muted' }, 'Loading…'));
  const editURL = href('recordings', entry.slug + '/edit');

  async function show() {
    const info = await api('GET', 'api/folder?path=' + q(entry.path));
    panel.replaceChildren(...(info.outputs.length ? processed(info) : unprocessed(info)));
    if (makingNow(info)) followMaking(info);
  }

  // makingNow: the server is making the files, or they wait for other runs (maker.go).
  const makingNow = (info) => info.making === 'waiting' || Boolean(info.job && (info.job.state === 'queued' || info.job.state === 'running'));

  // followMaking looks again every 2 s while the server makes the files: the Download menu and the line under it
  // follow; when it is done, the details are drawn again with the new files.
  let following = false;
  async function followMaking(info) {
    if (following) return;
    following = true;
    try {
      while (panel.isConnected && makingNow(info)) {
        await sleep(2000);
        if (!panel.isConnected) return;
        const next = await api('GET', 'api/folder?path=' + q(entry.path));
        Object.assign(info, next);
        for (const fn of info.redraw || []) fn();
      }
      if (panel.isConnected) await show();
    } catch { /* the next opening shows it */ } finally {
      following = false;
    }
  }

  // makingLine says what the server does with the files: waiting for other runs, or making them (with its bar).
  function makingLine(info, el) {
    const draw = () => {
      const j = info.job;
      if (j && (j.state === 'queued' || j.state === 'running') && info.making !== 'waiting') {
        const left = timeLeft(j.remaining);
        el.replaceChildren(t('The files are being made…') + (left ? ' · ' + left : ''), liveBar(j.progress, j.state === 'running', t('The files are being made…')), backgroundNote('span'));
      } else if (info.making === 'waiting') {
        el.replaceChildren(t('The files wait for another run to finish…'));
      } else {
        el.replaceChildren(info.makeError ? h('span', { class: 'error' }, t('The files could not be made: {error}', { error: tr(info.makeError) })) : '');
      }
    };
    draw();
    (info.redraw ||= []).push(draw);
    return el;
  }

  // The recording's own labels: pills with a cross, and a field to add one. The
  // labels already used are suggested as you type (or all of them, on focus);
  // a label is added by picking one, the + button, Enter, a comma, or leaving
  // the field. Searched on the recordings page; separate from the tags written
  // into files.
  function labelEditor() {
    const box = h('div', { class: 'labels' });
    const input = h('input', { type: 'text', class: 'label-input', name: 'label', placeholder: t('Add a label'), maxlength: 30,
      'aria-label': t('Add a label'), autocomplete: 'off', enterkeyhint: 'done' });
    const add = h('button', { type: 'button', class: 'label-add', name: 'add-label', title: t('Add the label'), 'aria-label': t('Add the label'), hidden: true,
      onclick: () => commit() }, '+');
    const menu = h('ul', { class: 'suggest', role: 'listbox', hidden: true });
    // Keep the focus in the field while a suggestion or the button is pressed.
    for (const el of [menu, add]) el.addEventListener('mousedown', (e) => e.preventDefault());
    const matches = () => {
      const typed = input.value.trim().toLowerCase();
      return (ctx.suggestions ? ctx.suggestions() : [])
        .filter((l) => !entry.labels.includes(l) && l.toLowerCase().includes(typed)).slice(0, 8);
    };
    const drawMenu = () => {
      const found = document.activeElement === input ? matches() : [];
      menu.hidden = found.length === 0;
      menu.replaceChildren(...found.map((l) => h('li', { role: 'option', onclick: () => { input.value = ''; save((ls) => [...ls, l]); } }, l)));
      add.hidden = !input.value.trim();
    };
    const draw = () => {
      box.replaceChildren(
        ...entry.labels.map((l) => h('span', { class: 'chip own' }, l,
          h('button', { type: 'button', class: 'x', title: t('Remove {name}', { name: l }), 'aria-label': t('Remove label {name}', { name: l }),
            onclick: () => save((ls) => ls.filter((x) => x !== l)) }, '×'))),
        ...(entry.labels.length < MAX_LABELS ? [h('span', { class: 'label-add-box' }, input, add, menu)] : []));
      drawMenu();
    };
    // save makes a change to the labels. Changes are sent one after the other, each to the labels as the one
    // before left them: a label added while another is being saved is not lost. typed goes back in the
    // field if the change fails.
    let queue = Promise.resolve();
    function save(change, typed = '') {
      queue = queue.then(async () => {
        try {
          const e = await api('PUT', 'api/labels?path=' + q(entry.path), { labels: change(entry.labels) });
          entry.labels = e.labels;
          if (ctx.changed) ctx.changed();
        } catch (err) {
          if (typed && !input.value) input.value = typed;
          alertDialog({ body: err.message });
        }
        draw();
        if (entry.labels.length < MAX_LABELS) input.focus();
      });
      return queue;
    }
    // commit adds what is typed; the field is free for the next one at once.
    const commit = () => {
      const typed = input.value;
      if (!typed.trim()) return;
      input.value = '';
      drawMenu();
      return save((ls) => [...ls, typed], typed);
    };
    input.addEventListener('input', () => {
      if (input.value.includes(',')) {
        const [first, ...rest] = input.value.split(',');
        input.value = first;
        commit();
        input.value = rest.join(',');
        return;
      }
      drawMenu();
    });
    input.addEventListener('focus', drawMenu);
    input.addEventListener('blur', () => { menu.hidden = true; if (input.value.trim()) commit(); });
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') { input.value = ''; menu.hidden = true; return; }
      if (e.key !== 'Enter') return;
      e.preventDefault();
      commit();
    });
    draw();
    return box;
  }

  // copyLink copies the address of this recording: it opens the recordings page at it, in this church, for those
  // with a login. A tick shows for a moment that it was copied.
  function copyLink() {
    const url = href('recordings', entry.slug);
    const note = h('span', { class: 'muted', role: 'status' });
    const b = iconButton('button', COPY_ICON, t('Copy link'), { name: 'copy-link', onclick: async () => {
      try {
        await navigator.clipboard.writeText(url);
      } catch {
        alertDialog({ title: t('Link to this recording'), body: url });
        return;
      }
      b.innerHTML = CHECK_ICON;
      note.textContent = t('Link copied.');
      setTimeout(() => { b.innerHTML = COPY_ICON; note.textContent = ''; }, 2500);
    } });
    return [b, note];
  }

  // The download menu stays open, and on the same version, when the panel is drawn again after a format was made.
  let menuOpen = false;
  let menuVersion = '';

  // downloadMenu is one Download button that opens the files: each format with its size, or Make for one not made
  // yet. With an intro or outro, the version is chosen first: with them, or the sermon only.
  function downloadMenu(info, saved, status) {
    const only = sermonOnly(info).length > 0;
    if (!only) menuVersion = '';
    const made = (format) => info.outputs.find((o) => o.format === format);
    // Being made by the server now, or waiting to be: shown so, not offered to make.
    const making = (format) => makingNow(info) && (info.makingFormats || []).includes(format);
    const list = h('div', { class: 'menu-items' });
    const buttons = [];
    const draw = () => {
      buttons.length = 0;
      list.replaceChildren(...FORMATS.flatMap(([format, label]) => {
        if (menuVersion && !made(format)) return ''; // the sermon only is made with its format
        const o = made(format + menuVersion);
        const name = menuVersion ? t('Download {format}, sermon only', { format: label }) : t('Download {format}', { format: label });
        const gone = !info.raw && info.originalDeleted; // nothing to make it from
        const busy = making(format);
        if (o) {
          const go = h('span', { class: 'go icon', 'aria-hidden': 'true' });
          go.innerHTML = DOWNLOAD_ICON;
          // One being made again stays there until the new one replaces it.
          const note = busy ? ' · ' + (info.making === 'waiting' ? t('waiting') : t('being updated')) : o.stale ? ' · ' + t('out of date') : '';
          const link = h('a', { class: 'menu-item', href: fileURL(o, true), 'aria-label': name },
            h('strong', {}, label), h('span', { class: o.stale && !busy ? 'warn' : 'muted' }, formatSize(o.size) + note), go);
          if (!o.stale || busy || gone || !saved) return link;
          // Made from another cut or sound than the saved version: made again from it, here.
          const again = h('button', { type: 'button', class: 'menu-item again', name: 'make-again-' + format, disabled: makingNow(info),
            title: t('Make {format} again from the saved version', { format: label }),
            onclick: () => make(format, label, again, status, buttons, true) },
          h('span', {}), h('span', {}), h('span', { class: 'go' }, t('Make again')));
          buttons.push(again);
          return [link, again];
        }
        if (busy) {
          return h('div', { class: 'menu-item making', 'data-format': format },
            h('strong', {}, label), h('span', { class: 'muted' }, info.making === 'waiting' ? t('waiting') : t('being made')), h('span', {}));
        }
        const b = h('button', { type: 'button', class: 'menu-item', name: 'make-' + format, disabled: !saved || gone || makingNow(info),
          title: gone ? t('The original was deleted, so it cannot be made') : saved ? t('Not made yet: make {format}', { format: label }) : t('Save the sermon in the editor first'),
          onclick: () => make(format, label, b, status, buttons) },
        h('strong', {}, label), h('span', { class: 'muted' }, t('not made')), h('span', { class: 'go' }, t('Make')));
        buttons.push(b);
        return b;
      }));
    };
    const versionButton = (value, label) => h('button', { type: 'button', class: 'seg', 'aria-pressed': String(menuVersion === value),
      onclick: (e) => {
        menuVersion = value;
        for (const b of e.currentTarget.parentElement.children) b.setAttribute('aria-pressed', String(b === e.currentTarget));
        draw();
      } }, label);
    draw();
    (info.redraw ||= []).push(draw); // followMaking draws it again
    return dropMenu(/** @type {HTMLDetailsElement} */ (h('details', { class: 'menu download-menu', open: menuOpen },
      iconButton('summary', DOWNLOAD_ICON, t('Download'), { class: 'button primary' }, t('Download')),
      h('div', { class: 'menu-panel' },
        only ? h('span', { class: 'mode', role: 'group', 'aria-label': t('Version') },
          versionButton('', t('With intro and outro')), versionButton(SERMON_ONLY, t('Sermon only'))) : '',
        list, status))), (open) => { menuOpen = open; });
  }

  // shareButton makes a download share of a saved sermon's files: a code and a link that let someone without a
  // login download them on the public share page until it expires (in 30 days unless changed). The recording, or with its transcript;
  // all formats (also ones made later) unless limited. It makes nothing: Save in the editor makes the files.
  function shareButton(info) {
    return iconButton('button', SHARE_ICON, t('Share for download…'), { name: 'share-download', onclick: async () => {
      const tx = info.transcript; // none when the studio makes no transcripts
      const offerTranscript = Boolean(tx && tx.text);
      let withTranscript = false;
      const modeButton = (on, label) => h('button', { type: 'button', class: 'seg', 'aria-pressed': String(withTranscript === on),
        onclick: (e) => {
          withTranscript = on;
          for (const b of e.currentTarget.parentElement.children) b.setAttribute('aria-pressed', String(b === e.currentTarget));
          draw();
        } }, label);
      const mode = offerTranscript ? h('span', { class: 'mode share-mode', role: 'group', 'aria-label': t('What is shared') },
        modeButton(false, t('Recording')), modeButton(true, t('Recording and transcript'))) : '';
      const boxes = FORMATS.map(([f]) => h('input', { type: 'checkbox', name: 'format', value: f, checked: true, onchange: () => draw() }));
      // With an intro or outro, the share gives one of the two versions.
      const version = sermonOnly(info).length ? h('select', { name: 'version', id: 'download-version', onchange: () => draw() },
        h('option', { value: '' }, t('With intro and outro')), h('option', { value: 'sermon' }, t('Sermon only'))) : null;
      const label = h('input', { type: 'text', name: 'label', id: 'download-label', maxlength: 80, value: entry.name });
      const expires = expiryInput();
      const month = new Date();
      month.setDate(month.getDate() + 30);
      month.setHours(23, 59, 0, 0);
      expires.set(month.toISOString()); // a share ends by itself unless the church says otherwise
      const description = descriptionInput();
      const what = h('div', { class: 'share-what wide', 'aria-live': 'polite' });
      // draw says what the share gives: the files there, and those not made (shared once made, with all formats).
      const draw = () => {
        const suffix = version && version.value === 'sermon' ? SERMON_ONLY : '';
        const names = (list) => list.map((f) => FORMATS.find(([x]) => x === f)[1]).join(', ');
        const checked = boxes.filter((x) => x.checked).map((x) => x.value);
        const ready = checked.filter((f) => info.outputs.some((o) => o.format === f + suffix));
        const missing = checked.filter((f) => !ready.includes(f));
        const lines = [];
        if (ready.length) lines.push(h('p', {}, '✓ ' + t('Ready: {formats}', { formats: names(ready) })));
        if (missing.length) {
          lines.push(h('p', { class: 'warn' }, checked.length === FORMATS.length
            ? t('Not made yet: {formats}. Shared once made (from the Download menu).', { formats: names(missing) })
            : t('Not made yet: {formats}. Make them from the Download menu first.', { formats: names(missing) })));
        }
        if (withTranscript) lines.push(h('p', {}, '✓ ' + t('Transcript: ready')));
        what.replaceChildren(...lines);
      };
      draw();
      for (;;) {
        const ok = await confirmDialog({ title: t('Share for download'), confirm: t('Make share'), body: [
          h('p', { class: 'muted' }, t('Makes a code and a link that let someone without a login download this sermon on the share page.')),
          h('div', { class: 'share-form' },
            mode ? h('div', { class: 'wide' }, mode) : '',
            what,
            h('details', { class: 'wide share-formats' }, h('summary', {}, t('Limit formats')),
              h('p', { class: 'muted' }, t('All formats are shared, also ones made later. Untick the ones not to share.')),
              h('span', {}, boxes.map((b, i) => h('label', { class: 'check' }, b, ' ' + FORMATS[i][1] + ' ')))),
            h('label', { for: 'download-label' }, t('Name')), label,
            h('span', {}, t('Description')), h('span', {}, description, h('span', { class: 'muted hint' }, t('Not shown to those who use the code.'))),
            version ? [h('label', { for: 'download-version' }, t('Version')), version] : '',
            h('span', {}, t('Expires')), expires.el),
        ] });
        if (!ok) return;
        try {
          const formats = boxes.filter((b) => b.checked).map((b) => b.value);
          const done = await api('POST', 'api/shares', {
            kind: 'download', recording: entry.path, label: label.value, description: description.value,
            allFormats: formats.length === FORMATS.length, formats, transcript: withTranscript,
            sermonOnly: Boolean(version && version.value === 'sermon'),
            expires: expires.value(),
          });
          await showCode(done.url, done.code, { download: true, expires: done.share.expires });
          await recordingShares(entry.path, sharesBox);
          return;
        } catch (err) {
          await alertDialog({ body: err.message }); // then the form again, as it was filled in
        }
      }
    } });
  }

  // sharesBox lists the recording's own download shares (only when it has any).
  const sharesBox = h('div', { class: 'recording-shares' });

  function unprocessed(info) {
    return [
      labelEditor(),
      makingNow(info) || info.makeError ? makingLine(info, h('p', { class: 'muted making', 'aria-live': 'polite' })) : '',
      h('p', { class: 'actions' }, iconButton('a', EDIT_ICON, t('Open in the editor'), { class: 'button primary', href: editURL }, t('Open in the editor')), copyLink()),
    ];
  }

  function processed(info) {
    if (ctx.shares) recordingShares(entry.path, sharesBox).catch(() => {});
    const saved = info.sermon && info.sermon.saved;
    const tags = info.tags || {};
    const facts = [
      saved
        ? t('Length {time}', { time: formatTime(keptSeconds(saved), 0) }) + (saved.sections.length > 1 ? ' (' + tn(saved.sections.length, '{n} section', '{n} sections') + ')' : '')
        : t('Not saved in the editor yet'),
      tags.title && t('Title: {value}', { value: tags.title }),
      tags.artist && t('Artist: {value}', { value: tags.artist }),
      tags.album && t('Album: {value}', { value: tags.album }),
      !info.raw && info.originalDeleted && t('Original deleted'),
    ].filter(Boolean);
    // The intro and outro the files were made with, and whether making them again would give others.
    const mw = info.madeWith;
    const clipFacts = mw && [
      mw.intro || mw.outro
        ? t('Made with: {clips}', { clips: [mw.intro && t('intro {name}', { name: mw.intro.name }), mw.outro && t('outro {name}', { name: mw.outro.name })].filter(Boolean).join(', ') })
        : t('Made without intro or outro'),
      mw.changed ? t('The intro or outro has changed since: press Save in the editor to make the files again.') : '',
    ].filter(Boolean).join(' · ');
    const status = makingLine(info, h('p', { class: 'muted making', 'aria-live': 'polite' }));
    const best = PLAY_ORDER.map((f) => info.outputs.find((x) => x.format === f)).find(Boolean);
    // What the player plays: the format, and with the intro and outro if the files have them.
    const playing = FORMATS.find(([f]) => f === best.format)[1] + (mw && (mw.intro || mw.outro) ? ' · ' + t('with intro and outro') : '');
    const audio = /** @type {HTMLAudioElement} */ (h('audio', { class: 'player', controls: true, preload: 'none', src: fileURL(best), 'aria-label': t('The sermon') }));
    return [
      h('div', { class: 'facts-line' }, h('p', { class: 'facts' }, facts.join(' · ')), labelEditor()),
      clipFacts ? h('p', { class: 'facts' + (mw.changed ? ' warn' : '') }, clipFacts) : '',
      h('div', { class: 'player-row' },
        audio,
        h('span', { class: 'muted player-what' }, t('Plays: {what}', { what: playing }))),
      h('div', { class: 'actions' }, downloadMenu(info, saved, status), iconButton('a', EDIT_ICON, t('Edit sermon'), { class: 'button', href: editURL }, t('Edit')),
        copyLink(), ctx.shares && info.outputs.length && saved ? shareButton(info) : ''), // only a saved sermon is shared
      // info follows the transcript as it is made, so Share for download offers it once it is there.
      transcriptPanel(entry.path, info, { onDraw: (tx) => { info.transcript = tx; }, player: { el: audio, format: best.format } }).box,
      ctx.shares ? sharesBox : '',
    ];
  }

  async function make(format, label, button, status, buttons, again = false) {
    for (const b of buttons) b.disabled = true;
    button.classList.add('busy');
    status.textContent = t('Making {format}: the conversion is running…', { format: label });
    try {
      let job = await api('POST', 'api/make?path=' + q(entry.path) + '&format=' + format + (again ? '&again=1' : ''));
      while (job.state === 'queued' || job.state === 'running') {
        await sleep(1000);
        const left = timeLeft(job.remaining);
        status.replaceChildren(t('Making {format}: the conversion is running…', { format: label }) + (left ? ' · ' + left : ''),
          liveBar(job.progress, job.state === 'running', label), backgroundNote('span'));
        job = await api('GET', 'api/jobs/' + job.id);
      }
      if (job.state !== 'done') throw new Error(job.error ? tr(job.error) : t('{format} was not made ({state})', { format: label, state: job.state }));
      await show();
    } catch (err) {
      button.classList.remove('busy');
      status.replaceChildren(h('span', { class: 'error' }, err.message));
      for (const b of buttons) b.disabled = false;
    }
  }

  show().catch((err) => panel.replaceChildren(h('p', { class: 'error' }, err.message)));
  return panel;
}
