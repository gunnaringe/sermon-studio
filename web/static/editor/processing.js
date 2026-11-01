// The editor's metadata (the tags written into the files) and processing (how the sermon is made into files:
// channels, noise, levels, loudness and bitrates, a sample to hear them, and the church's defaults).
import { confirmDialog } from '../dialog.js';
import { t, tr } from '../i18n.js';
import { api, q, h, forTenant } from '../util.js';
import { SOUND_KEYS, DEFAULT_KEYS } from './common.js';
import { verdict } from './info.js';

/** @param {import('./common.js').Editor} ed */
export function processingPart(ed) {
  const { view, info, path, config, opts, tags, sections } = ed;
  const channelWarning = h('p', { class: 'notice', role: 'status', hidden: true });
  // Going back to the church's defaults, or making this processing the default (see useDefaults).
  const sameAsDefaults = () => DEFAULT_KEYS.every((k) => String(opts[k]) === String(config.defaults.start[k]));
  const defaultsNote = h('span', { class: 'muted', role: 'status' });
  const useDefaultsButton = h('button', { type: 'button', name: 'use-defaults', onclick: useDefaults }, t('Use the defaults'));
  const makeDefaultsButton = h('button', { type: 'button', name: 'make-defaults', onclick: makeDefaults }, t('Make these the defaults'));
  let processingEl = processingPanel();

  // metadataPanel is the tags: title, artist and album, in every file.
  function metadataPanel() {
    const tag = (key, label) => h('label', { class: 'tag' }, h('span', { class: 'name' }, label), h('input', {
      type: 'text', name: key, value: tags[key], maxlength: 200,
      oninput: (e) => { tags[key] = e.target.value; ed.changed(); },
    }));
    return h('div', { class: 'panel' },
      info.tags
        ? h('div', { class: 'options' }, tag('title', t('Title')), tag('artist', t('Artist')), tag('album', t('Album')))
        : h('span', { class: 'muted' }, info.tagsError ? tr(info.tagsError) : t('No tags')),
      h('p', { class: 'muted' }, t('Written into the FLAC, MP3 and Opus files. A field left empty is left out.')));
  }

  // redrawProcessing shows the processing choices again, after they were set from outside (a revert).
  function redrawProcessing() {
    const next = processingPanel();
    processingEl.replaceWith(next);
    processingEl = next;
    showChannelWarning();
    showDefaults();
  }

  // ---- the church's default processing: this recording can go back to it, or make its own the default ----

  function showDefaults() {
    const same = sameAsDefaults();
    defaultsNote.textContent = same ? t('The same as the church\'s defaults.') : t('Not the same as the church\'s defaults.');
    useDefaultsButton.disabled = makeDefaultsButton.disabled = same;
  }

  function useDefaults() {
    ed.record(); // its own step, also right after another change
    const d = config.defaults.start;
    for (const k of SOUND_KEYS) opts[k] = d[k];
    for (const k of ['fadeIn', 'fadeOut']) {
      if (sections.length) ed.setFade(k, d[k]); // as long as the sermon allows
      else opts[k] = d[k];
    }
    redrawProcessing();
    ed.changed();
  }

  async function makeDefaults() {
    const ok = await confirmDialog({ title: t('Make these the defaults'), confirm: t('Make them the defaults'),
      body: t('New recordings start with this processing and these fades. Recordings that have their own keep it.') });
    if (!ok) return;
    const levels = { targetLufs: opts.targetLufs, mp3Bitrate: opts.mp3Bitrate, opusBitrate: opts.opusBitrate };
    const p = {
      ...config.defaults.processing,
      stereo: opts.stereo, noiseReduction: Math.round(opts.noiseReduction), noiseMethod: opts.noiseMethod, roomSound: opts.roomSound,
      leveling: opts.leveling, compression: opts.compression, truePeak: opts.truePeak,
      fadeIn: opts.fadeIn, fadeOut: opts.fadeOut,
      [opts.stereo ? 'stereoLevels' : 'mono']: levels,
    };
    try {
      await api('PUT', 'api/processing', { processing: p });
      Object.assign(config, await api('GET', 'api/config'));
      showDefaults();
    } catch (e) {
      defaultsNote.textContent = t('Not saved: {error}', { error: e.message });
    }
  }

  // processingPanel is how the sermon is made into files: channels, noise, levels, loudness and bitrates.
  // The choices are kept with the sermon.
  function processingPanel() {
    const field = (label, input) => h('label', {}, h('span', { class: 'name' }, label), input);
    const num = (key, label, step, min, max) => field(label, h('input', {
      type: 'number', name: key, value: opts[key], step, min, max,
      oninput: (e) => { opts[key] = e.target.value === '' ? NaN : Number(e.target.value); ed.changed(); },
    }));
    const text = (key, label) => field(label, h('input', {
      type: 'text', name: key, value: opts[key],
      oninput: (e) => { opts[key] = e.target.value; ed.changed(); },
    }));
    const choice = (key, label, values) => field(label, h('select', { name: key, onchange: (e) => { opts[key] = e.target.value; ed.changed(); } },
      values.map(([value, text]) => h('option', { value, selected: opts[key] === value }, text))));
    const group = (title, ...children) => h('div', { class: 'pgroup' }, h('strong', {}, title), ...children);
    const hint = (text) => h('p', { class: 'muted hint' }, text);
    // Mono or stereo: a choice between the two, not on and off.
    const channelButton = (stereo, label) => h('button', { type: 'button', class: 'seg', name: stereo ? 'stereo' : 'mono',
      'aria-pressed': String(opts.stereo === stereo), onclick: () => setStereo(stereo) }, label);
    const channels = h('div', { class: 'channels' },
      h('span', { class: 'mode', role: 'group', 'aria-label': t('Channels') }, channelButton(false, t('Mono')), channelButton(true, t('Stereo'))));
    const roomValue = h('output', { class: 'muted' }, `${opts.roomSound}%`);
    const room = field(t('Keep room sound'), h('span', { class: 'range' }, h('input', {
      type: 'range', name: 'roomSound', min: 0, max: 100, step: 5, value: opts.roomSound,
      oninput: (e) => { opts.roomSound = Number(e.target.value); roomValue.textContent = `${opts.roomSound}%`; ed.changed(); },
    }), roomValue));
    // The noise method: the dB are only the standard one's ('' = standard, kept out of what is saved).
    const strength = num('noiseReduction', t('Noise reduction (dB)'), 1, 0, 40);
    strength.hidden = opts.noiseMethod === 'speech';
    const method = field(t('Noise reduction'), h('select', { name: 'noiseMethod', onchange: (e) => {
      opts.noiseMethod = e.target.value || undefined;
      strength.hidden = e.target.value === 'speech';
      ed.changed();
    } }, h('option', { value: '', selected: opts.noiseMethod !== 'speech' }, t('noise|Standard')),
    h('option', { value: 'speech', selected: opts.noiseMethod === 'speech' }, t('noise|Speech'))));
    const strengths = (off, a, b, c) => [['off', off], ['gentle', a], ['normal', b], ['strong', c]];

    const sample = h('audio', { class: 'player', controls: true, hidden: true, 'aria-label': t('The sample') });
    const sampleStatus = h('span', { class: 'muted', role: 'status' });
    const sampleButton = h('button', { type: 'button', name: 'sample', onclick: async () => {
      sampleButton.disabled = true;
      sample.hidden = true;
      sampleStatus.className = 'muted';
      sampleStatus.textContent = t('Making the sample…');
      try {
        const body = Object.fromEntries(SOUND_KEYS.map((k) => [k, opts[k]]));
        const resp = await fetch(forTenant('api/sample?path=' + q(path) + '&at=' + (ed.ws ? ed.ws.getCurrentTime() : 0)), {
          method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
        });
        if (!resp.ok) throw new Error(tr((await resp.json().catch(() => ({}))).error || `${resp.status} ${resp.statusText}`));
        URL.revokeObjectURL(sample.src);
        sample.src = URL.createObjectURL(await resp.blob());
        sample.hidden = false;
        sampleStatus.textContent = '';
        sample.play().catch(() => {});
      } catch (e) {
        sampleStatus.className = 'error';
        sampleStatus.textContent = t('Could not make the sample: {error}', { error: e.message });
      } finally {
        sampleButton.disabled = false;
      }
    } }, t('Hear a sample'));

    return h('div', { class: 'panel processing' },
      group(t('Defaults'), h('p', {}, useDefaultsButton, ' ', makeDefaultsButton), defaultsNote),
      group(t('Channels'), channels, channelWarning),
      group(t('Noise'),
        method,
        strength,
        room,
        hint(t('Taking out noise also takes out some of the room\'s own sound, such as its reverb. Less reduction, or more room sound kept, sounds more natural; more sounds cleaner.')),
        hint(t('Speech keeps the voice and takes out what is not speech, more than the standard one does. Listen to a sample: it can also take out singing and make the voice sound thin.'))),
      group(t('Levels'),
        choice('leveling', t('Leveling'), strengths(t('Off'), t('Gentle'), t('Normal'), t('Strong'))),
        choice('compression', t('Compression'), [['off', t('Off')], ['light', t('compression|Light')], ['normal', t('Normal')], ['strong', t('Strong')]]),
        hint(t('Leveling brings quiet speech and loud parts closer. Compression narrows the loudness range further: light is usually enough.'))),
      group(t('Loudness'),
        num('targetLufs', t('Target loudness (LUFS)'), 0.5, -70, -5),
        num('truePeak', t('Max true peak (dBTP)'), 0.1, -20, 0),
        hint(t('Podcasts are usually -19 LUFS in mono and -16 in stereo. The peaks are kept under the true peak.'))),
      group(t('Encoding'), text('mp3Bitrate', t('MP3 bitrate')), text('opusBitrate', t('Opus bitrate'))),
      group(t('Try the settings'),
        h('p', {}, sampleButton, ' ', sampleStatus),
        sample,
        hint(t('Makes 30 seconds from the playhead with these settings, to listen to. The cuts, pauses and fades are not applied.'))));
  }

  // setStereo chooses mono or stereo.
  function setStereo(stereo) {
    if (opts.stereo === stereo) return;
    followChannels(stereo);
    opts.stereo = stereo;
    for (const b of view.querySelectorAll('.channels button.seg')) b.setAttribute('aria-pressed', String((b.name === 'stereo') === stereo));
    showChannelWarning();
    ed.changed();
  }

  // followChannels moves the loudness and bitrates to the new defaults when
  // switching mono/stereo, unless they were changed by hand.
  function followChannels(stereo) {
    const from = stereo ? config.defaults.mono : config.defaults.stereo;
    const to = stereo ? config.defaults.stereo : config.defaults.mono;
    for (const key of ['targetLufs', 'mp3Bitrate', 'opusBitrate']) {
      if (String(opts[key]) === String(from[key])) {
        opts[key] = to[key];
        const input = view.querySelector(`input[name=${key}]`);
        if (input) input.value = to[key];
      }
    }
  }

  // A recording that is mono in effect gains nothing from stereo, or worse.
  function showChannelWarning() {
    const v = info.recording && info.recording.channels;
    const mono = v && ['mono', 'identical', 'one-sided'].includes(v.kind);
    const warn = opts.stereo && mono;
    channelWarning.replaceChildren(...(warn ? [t(v.kind === 'one-sided'
      ? '⚠ The recording is {verdict}: stereo gives the sermon on one side only. Choose mono.'
      : '⚠ The recording is {verdict}: stereo gives the same sound in bigger files. Choose mono.', { verdict: verdict(v).toLowerCase() }),
    ' ', h('button', { type: 'button', name: 'use-mono', onclick: () => setStereo(false) }, t('Use mono'))] : []));
    channelWarning.hidden = !warn;
  }

  return { metadataEl: metadataPanel(), processingEl, channelWarning, redrawProcessing, showDefaults, setStereo, showChannelWarning };
}
