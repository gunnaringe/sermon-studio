// The church's default processing: what a recording starts with until it has
// its own. A section of the settings; processingView fills its box.
import { api, h } from './util.js';
import { t } from './i18n.js';
import { repairControls } from './repair.js';

// Speech mixes the room sound kept back in, so from SPEECH_ROOM_WARN % it keeps most of the noise (80 %: only
// about 2 dB less), and at 100 % all of it: speechRoomNote says so.
export const SPEECH_ROOM_WARN = 80;
export const speechRoomNote = (n) => (n >= 100
  ? t('With Speech, keeping 100 % of the room sound takes no noise out: lower it, or choose Standard.')
  : t('With Speech, keeping {n} % of the room sound leaves most of the noise in.', { n }));

export async function processingView(box, saved) {
  const { defaults } = await api('GET', 'api/config');
  const p = structuredClone(defaults.processing);
  const status = h('span', { class: 'muted', role: 'status' });
  const touched = () => { status.textContent = ''; };

  // A field of p (or of p.mono / p.stereoLevels), as a number or as text.
  const input = (obj, key, attrs) => h('input', {
    name: key, value: obj[key], ...attrs,
    oninput: (e) => { obj[key] = attrs.type === 'number' ? Number(e.target.value) : e.target.value; touched(); },
  });
  const num = (obj, key, step, min, max, label) => input(obj, key, { type: 'number', step, min, max, 'aria-label': label });
  const text = (obj, key, label) => input(obj, key, { type: 'text', 'aria-label': label });
  const row = (label, ...inputs) => h('label', {}, h('span', { class: 'name' }, label), h('span', {}, ...inputs));
  const pair = (label, make) => row(label,
    h('span', { class: 'muted' }, t('Mono')), make(p.mono, label + ' ' + t('Mono')),
    h('span', { class: 'muted' }, t('Stereo')), make(p.stereoLevels, label + ' ' + t('Stereo')));
  const choice = (key, label, values) => row(label, h('select', { name: key, onchange: (e) => { p[key] = e.target.value; touched(); } },
    values.map(([value, text]) => h('option', { value, selected: p[key] === value }, text))));
  const strengths = (low) => [['off', t('Off')], low, ['normal', t('Normal')], ['strong', t('Strong')]];
  const channels = row(t('Channels'), h('select', { name: 'stereo', onchange: (e) => { p.stereo = e.target.value === 'stereo'; touched(); } },
    h('option', { value: 'mono', selected: !p.stereo }, t('Mono')), h('option', { value: 'stereo', selected: p.stereo }, t('Stereo'))));

  // The noise method: the dB are only the standard one's, the room sound not off's ('' = standard, kept out of what is saved).
  const strength = row(t('Noise reduction (dB)'), num(p, 'noiseReduction', 1, 0, 40, t('Noise reduction (dB)')));
  const room = row(t('Keep room sound') + ' (%)', num(p, 'roomSound', 5, 0, 100, t('Keep room sound')));
  room.addEventListener('input', () => showNoise());
  const roomNote = h('p', { class: 'notice', role: 'status', hidden: true });
  function showNoise() {
    strength.hidden = p.noiseMethod === 'speech' || p.noiseMethod === 'off';
    room.hidden = p.noiseMethod === 'off';
    roomNote.hidden = !(p.noiseMethod === 'speech' && p.roomSound >= SPEECH_ROOM_WARN);
    roomNote.textContent = roomNote.hidden ? '' : speechRoomNote(p.roomSound);
  }
  showNoise();
  const method = row(t('Noise reduction'), h('select', { name: 'noiseMethod', onchange: (e) => {
    p.noiseMethod = e.target.value || undefined;
    showNoise();
    touched();
  } }, [['off', t('noise|Off')], ['', t('noise|Standard')], ['speech', t('noise|Speech')]].map(([value, label]) =>
    h('option', { value, selected: (p.noiseMethod || '') === value }, label))));

  const repair = repairControls(p, touched, (label, el) => row(label, el));

  const save = h('button', { class: 'primary', name: 'save-processing', onclick: async () => {
    try {
      await api('PUT', 'api/processing', { processing: p });
      status.textContent = t('Saved.');
      await saved();
    } catch (err) { status.textContent = err.message; }
  } }, t('Save'));
  const builtin = h('button', { name: 'builtin-processing', onclick: async () => {
    try {
      await api('PUT', 'api/processing', { processing: null });
      await saved();
      await processingView(box, saved);
    } catch (err) { status.textContent = err.message; }
  } }, t('Use the built-in values'));

  box.replaceChildren(
    h('p', { class: 'muted' }, t('What a recording starts with in the editor. A recording keeps its own processing once it is changed there; Use the defaults in the editor brings these back.')),
    h('div', { class: 'panel tags processing-defaults' },
      channels,
      repair.lowCut,
      repair.declick,
      repair.advanced,
      method,
      strength,
      room,
      roomNote,
      choice('leveling', t('Leveling'), strengths(['gentle', t('Gentle')])),
      choice('compression', t('Compression'), strengths(['light', t('compression|Light')])),
      pair(t('Target loudness (LUFS)'), (o, label) => num(o, 'targetLufs', 0.5, -70, -5, label)),
      row(t('Max true peak (dBTP)'), num(p, 'truePeak', 0.1, -20, 0, t('Max true peak (dBTP)'))),
      pair(t('MP3 bitrate'), (o, label) => text(o, 'mp3Bitrate', label)),
      pair(t('Opus bitrate'), (o, label) => text(o, 'opusBitrate', label)),
      row(t('Fade in (s)'), num(p, 'fadeIn', 0.1, 0, 30, t('Fade in (s)'))),
      row(t('Fade out (s)'), num(p, 'fadeOut', 0.1, 0, 30, t('Fade out (s)'))),
      h('div', {}, save, ' ', builtin, ' ', status)));
}
