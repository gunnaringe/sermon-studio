// The work the server is doing for the church (api/tasks, web/tasks.go), on top of the recordings page: a line
// with what is being done and how many wait, which opens to the whole list. Hidden while there is none. It is
// read again every 3 s while there is work, else every 15 s, until the page is left.
import { t, tn, tr } from './i18n.js';
import { api, h, href } from './util.js';

const FORMAT_NAMES = { flac: 'FLAC', mp3: 'MP3', opus: 'Opus' };
const OPEN_KEY = 'tasksOpen';

// what says what a task is, with the recording's name as a link to it.
function what(task) {
  const name = h('a', { href: href('recordings', task.slug) }, task.name);
  const formats = new Intl.ListFormat(document.documentElement.lang || 'en').format((task.formats || []).map((f) => FORMAT_NAMES[f] || f));
  const running = task.state === 'running';
  const text = {
    files: running ? (formats ? t('Making {formats} of {name}') : t('Making the files of {name}'))
      : (formats ? t('Waiting to make {formats} of {name}') : t('Waiting to make the files of {name}')),
    convert: t('Converting the recording {name}'),
    analyze: t('Preparing the waveform of {name}'),
    transcript: running ? t('Transcribing {name}') : t('Waiting to transcribe {name}'),
  }[task.kind] || task.kind;
  // {name} becomes the link; {formats} the formats.
  return text.replace('{formats}', formats).split('{name}').flatMap((part, i) => (i ? [name, part] : [part]));
}

function line(task) {
  const running = task.state === 'running';
  const detail = running
    ? [task.step ? tr(task.step.replace(/^\d+\.\s*/, '')) : '', task.progress ? Math.round(task.progress * 100) + ' %' : ''].filter(Boolean).join(' · ')
    : (task.ahead ? tn(task.ahead, '{n} ahead', '{n} ahead') : t('waiting'));
  return h('li', { class: 'task ' + task.state },
    h('span', { class: 'what' }, ...what(task)),
    detail ? h('span', { class: 'muted detail' }, detail) : '',
    running ? h('progress', { max: 1, value: task.progress || 0, 'aria-label': t('How far it is') }) : '');
}

// tasksPanel is the panel; onWork is called when there is work, so the page can follow its recordings too.
export function tasksPanel({ onWork = () => {} } = {}) {
  const box = h('details', { class: 'tasks panel', hidden: true, open: localStorage.getItem(OPEN_KEY) === '1',
    ontoggle: () => localStorage.setItem(OPEN_KEY, box.open ? '1' : '0') });
  const summary = h('summary', {});
  const list = h('ul', { class: 'task-list' });
  box.append(summary, list);

  function draw({ tasks, running, waiting }) {
    box.hidden = tasks.length === 0;
    if (!tasks.length) return;
    const first = tasks[0];
    const counts = [running ? tn(running, '{n} task going on', '{n} tasks going on') : '', waiting ? tn(waiting, '{n} waiting', '{n} waiting') : '']
      .filter(Boolean).join(' · ');
    summary.replaceChildren(
      h('span', { class: 'live-dot' + (running ? ' running' : ''), 'aria-hidden': 'true' }),
      h('span', { class: 'now' }, ...what(first)),
      h('span', { class: 'muted counts' }, counts));
    list.replaceChildren(...tasks.map(line));
  }

  (async () => {
    while (true) {
      let data = null;
      try { data = await api('GET', 'api/tasks'); } catch { /* tried again */ }
      if (!box.isConnected) return; // the page was left
      if (data) {
        draw(data);
        if (data.tasks.length) onWork();
      }
      await new Promise((r) => setTimeout(r, data && data.tasks.length ? 3000 : 15000));
      if (!box.isConnected) return;
    }
  })();
  return box;
}
