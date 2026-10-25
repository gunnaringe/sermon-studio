// The editor's input file (what the recording is: its file, format and channels) and Delete (the recording
// into the trash, or only its original, keeping the files, to free the space).
import { alertDialog, confirmDialog } from '../dialog.js';
import { t, tn, tr, lang } from '../i18n.js';
import { api, q, h, href, go, isVideo, formatTime, formatSize, forTenant, formatDateTime, can } from '../util.js';

// describeCodec names a recording's format the way people know it.
function describeCodec(fmt) {
  if (!fmt) return '';
  const c = fmt.codec || '';
  if (c.startsWith('pcm_')) return 'WAV';
  return { flac: 'FLAC', mp3: 'MP3', opus: 'Opus', vorbis: 'Ogg Vorbis', aac: 'AAC', alac: 'ALAC' }[c] || (c || fmt.container || '').toUpperCase();
}

// describeFormat is a recording's format on one line: "FLAC · 48 kHz · 24-bit · 2 channels · 1,412 kb/s".
function describeFormat(fmt) {
  return [
    describeCodec(fmt),
    fmt.sampleRate ? `${fmt.sampleRate / 1000} kHz` : '',
    fmt.bits ? `${fmt.bits}-bit` : '',
    tn(fmt.channels, '{n} channel', '{n} channels'),
    fmt.bitrate ? `${fmt.bitrate.toLocaleString(lang)} kb/s` : '',
  ].filter(Boolean).join(' · ');
}

// verdict is what the recording's channels are, for people.
export function verdict(v) {
  return {
    mono: t('Mono (one channel)'),
    stereo: t('Stereo'),
    identical: t('Mono (both channels are the same)'),
    more: t('More than two channels'),
    'one-sided': v.quietBy
      ? t('Mono (the {side} side is {by} dB quieter)', { side: t(v.quiet), by: v.quietBy })
      : t('Mono (the {side} side is silent)', { side: t(v.quiet) }),
  }[v.kind] || v.kind;
}

/** @param {import('./common.js').Editor} ed */
export function infoPart(ed) {
  const { info, path, config } = ed;
  const inputPanel = h('div', { class: 'panel input-file' });
  const deletePanel = h('div', { class: 'panel delete-panel' });
  const folderName = path.split('/').pop();
  const originalGone = () => t('The original recording was deleted {date}, to free the space. The files made from it are kept, but the sermon can no longer be edited or regenerated.',
    { date: formatDateTime(info.originalDeleted) });
  // The original can go once files have been made from it, and nothing is being made.
  const canDeleteOriginal = () => can('recording.delete') && Boolean(info.raw) && (info.outputs || []).length > 0;
  const fileURL = (f, download) => forTenant('api/file?' + (download ? 'download=1&' : '') + 'path=' + q(f.path));

  function showInput() {
    const raw = info.raw, rec = info.recording, from = info.convertedFrom;
    if (!raw) {
      inputPanel.replaceChildren(h('p', {}, info.originalDeleted ? originalGone() : t('No recording yet.')));
      return;
    }
    const fmt = rec && rec.format;
    const rows = [
      [t('File'), [raw.name, h('span', { class: 'muted' }, ' · ' + formatSize(raw.size))]],
      [t('Format'), fmt ? describeFormat(fmt) : h('span', { class: 'muted' }, t('Reading…'))],
      [t('Length'), fmt ? formatTime(fmt.duration, 0) : h('span', { class: 'muted' }, '…')],
      [t('Source'), from && isVideo(from.name)
        ? t('{codec}, the sound of the original video ({name}, {size}), copied out unchanged; verified identical',
          { codec: describeCodec(fmt), name: from.name, size: formatSize(from.size) })
        : from
          ? t('{codec}, converted from the original {original} ({name}, {size}); verified identical',
            { codec: describeCodec(fmt) || 'FLAC', original: describeCodec(from.format) || 'WAV', name: from.name, size: formatSize(from.size) })
          : (fmt ? t('{codec} as it was received', { codec: describeCodec(fmt) }) : '…')],
      [t('Channels'), channelsText()],
    ];
    const md = fmt && fmt.metadata ? Object.entries(fmt.metadata) : [];
    const open = inputPanel.querySelector('details.input-more')?.open || false;
    inputPanel.replaceChildren(h('div', { class: 'input-line' },
      h('details', { class: 'input-more', open },
        h('summary', { title: t('Show the details of the recording') },
          h('strong', {}, raw.name), ' · ', formatSize(raw.size),
          ' · ', fmt ? formatTime(fmt.duration, 0) : h('span', { class: 'muted' }, t('Reading…')),
          ' · ', channelsText()),
        h('dl', { class: 'facts' }, rows.flatMap(([k, v]) => [h('dt', {}, k), h('dd', {}, v)])),
        md.length ? h('details', { class: 'metadata' }, h('summary', {}, t('Metadata in the file ({n})', { n: md.length })),
          h('dl', { class: 'facts' }, md.flatMap(([k, v]) => [h('dt', {}, k), h('dd', {}, v)]))) : ''),
      h('span', { class: 'input-actions' }, h('a', { class: 'button', href: fileURL(raw, true) }, t('Download original')),
        canDeleteOriginal() ? h('button', { type: 'button', class: 'delete', name: 'delete-original', onclick: deleteOriginal }, t('Delete original…')) : '')));
    ed.showChannelWarning();
  }

  function channelsText() {
    const rec = info.recording;
    if (!rec) return h('span', { class: 'muted' }, t('Checking…'));
    return rec.channels ? verdict(rec.channels) : h('span', { class: 'muted' }, rec.error ? tr(rec.error) : t('Not known'));
  }

  async function deleteOriginal() {
    const ok = await confirmDialog({
      title: t('Delete the original of {name}?', { name: folderName }),
      body: [
        h('p', {}, t('The original recording ({name}, {size}) is deleted for good, not moved to the trash. The files made from it are kept, and can still be downloaded and shared.',
          { name: info.raw.name, size: formatSize(info.raw.size) })),
        h('p', { class: 'notice' }, t('The sermon can no longer be edited or regenerated: no new cuts, formats, processing or intro and outro.')),
      ],
      confirm: t('Delete original'), danger: true,
    });
    if (!ok) return;
    try {
      await api('POST', 'api/recordings/delete-original?path=' + q(path));
      location.reload(); // the page without the waveform
    } catch (e) {
      alertDialog({ body: tr(e.message) });
    }
  }

  function renderDelete() {
    deletePanel.replaceChildren(
      h('p', { class: 'muted' }, t('Delete recording moves it to the trash, with its files and the original, for {days} days. Until then you can restore it under Settings → Trash.', { days: config.trashDays })),
      h('p', { class: 'actions' },
        h('button', { type: 'button', class: 'delete', name: 'delete-recording', onclick: deleteRecording }, t('Delete recording…')),
        canDeleteOriginal() ? h('button', { type: 'button', class: 'delete', name: 'delete-original', onclick: deleteOriginal }, t('Delete original…')) : ''),
      canDeleteOriginal() ? h('p', { class: 'muted' }, t('Delete original frees the space of the original recording and keeps the files made from it, but then the sermon can no longer be edited or regenerated.')) : '');
  }

  async function deleteRecording() {
    const ok = await confirmDialog({
      title: t('Delete {name}?', { name: folderName }),
      body: h('p', {}, t('It is moved to the trash, with its files and the original recording, and is removed for good after {days} days. Until then you can restore it under Settings → Trash.', { days: config.trashDays })),
      confirm: t('Delete'), danger: true,
    });
    if (!ok) return;
    try {
      await api('POST', 'api/recordings/delete?path=' + q(path));
      go(href());
    } catch (e) {
      alertDialog({ body: tr(e.message) });
    }
  }

  // refreshInfo reads the folder again: the recording and the outputs change
  // with conversion, analysis and runs.
  async function refreshInfo() {
    const inf = await api('GET', 'api/folder?path=' + q(path));
    if (!ed.alive) return;
    Object.assign(info, { raw: inf.raw, recording: inf.recording, convertedFrom: inf.convertedFrom, outputs: inf.outputs, originalDeleted: inf.originalDeleted });
    showInput();
    ed.renderOutputs();
    renderDelete();
  }

  // The facts come with the waveform's preparation: look again until they are there.
  async function waitForFacts() {
    while (ed.alive && info.raw && !info.recording) {
      await new Promise((r) => setTimeout(r, 2000));
      if (ed.alive) await refreshInfo().catch(() => {});
    }
  }

  return { inputPanel, deletePanel, showInput, waitForFacts, refreshInfo, renderDelete };
}
