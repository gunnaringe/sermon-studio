// Storage, a section of the settings (the admins'): how much the church keeps, of its quota (set by the site
// administrator), by kind.
import { api, h, formatSize } from './util.js';
import { t } from './i18n.js';

// storageView fills box with the church's storage; call it again to show it anew (after the trash changed).
export async function storageView(box) {
  const data = await api('GET', 'api/storage');
  const { use, total, quota } = data;
  const share = quota ? total / quota : 0;
  const size = (n) => n ? formatSize(n) : '0 kB';
  window.dispatchEvent(new CustomEvent('storage-full', { detail: quota > 0 && total >= quota }));
  const kinds = [
    [t('Recordings'), use.recordings, t('as they came, with their waveform')],
    [t('Made files'), use.files, t('FLAC, MP3 and Opus')],
    [t('Intro and outro'), use.clips, ''],
    [t('Trash'), use.trash, ''],
  ];
  box.replaceChildren(
    h('div', { class: 'panel storage' },
      h('p', { class: 'storage-total' }, quota
        ? t('{used} of {quota} used', { used: size(total), quota: size(quota) })
        : t('{used} used', { used: size(total) })),
      quota ? h('meter', { min: 0, max: 1, high: 0.9, optimum: 0, value: Math.min(share, 1), 'aria-label': t('Storage used') }) : '',
      share >= 0.9 ? h('p', { class: 'warn', role: 'status' }, share >= 1
        ? t('The church has used all its storage. Delete recordings or files that are not needed, and empty the trash.')
        : t('The church has used {percent} % of its storage. Delete recordings or files that are not needed, and empty the trash.', { percent: Math.floor(share * 100) })) : '',
      h('dl', { class: 'storage-kinds' }, kinds.flatMap(([name, bytes, note]) => [
        h('dt', {}, name, note ? h('span', { class: 'muted' }, ' · ' + note) : ''),
        h('dd', {}, size(bytes)),
      ])),
      quota ? '' : h('p', { class: 'muted' }, t('The church has no storage limit.'))));
}
