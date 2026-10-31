// The editor's waveform: the recording drawn from its peaks (the browser only streams a preview while playing),
// with the sections, cuts, silences and pauses on it; playing, zooming, and the keys of the editor.
import WaveSurfer from '../vendor/wavesurfer/wavesurfer.esm.js';
import RegionsPlugin from '../vendor/wavesurfer/plugins/regions.esm.js';
import TimelinePlugin from '../vendor/wavesurfer/plugins/timeline.esm.js';
import MinimapPlugin from '../vendor/wavesurfer/plugins/minimap.esm.js';
import HoverPlugin from '../vendor/wavesurfer/plugins/hover.esm.js';
import { infoDialog } from '../dialog.js';
import { t, tr } from '../i18n.js';
import { api, q, h, soloPlayer, converts, isVideo, formatTime, typing, forTenant, wheelZoomNeedsKey, kbdText } from '../util.js';
import { css } from './common.js';
import { skipped } from './spans.js';

const MAX_ZOOM = 200; // px per second: 5 ms per pixel, enough to cut between words

/** @param {import('./common.js').Editor} ed */
export function waveformPart(ed) {
  const { path, info, opts, sections, cuts, cleanups } = ed;
  const waveSection = h('section', {});
  let silenceEdges = [];
  let stopAt = null; // playback pauses here (hear start/end, play selection)
  let skipNow = false; // this playback jumps over the cuts even when the whole recording is played
  // What the Play button plays: what will be published (the sections without the cuts and the gaps, and the
  // pauses kept), or the recording as it is. The choice is the browser's.
  let playMode = localStorage.getItem('playMode') === 'recording' ? 'recording' : 'published';
  let pausing = false; // playback is waiting out a pause
  let lastTime = 0;

  // showWaveform prepares the waveform (the server converts the recording and reads its peaks), then shows it.
  function showWaveform() {
    if (!info.raw) {
      waveSection.replaceChildren(h('p', {}, t('Nothing to cut yet. Upload a recording first.')));
      return;
    }
    prepareWaveform().catch((e) => {
      if (ed.alive) waveSection.replaceChildren(h('p', { class: 'error' }, e.message));
    });
  }

  async function prepareWaveform() {
    const bar = h('progress', { max: 1, value: 0 });
    const note = h('p', { class: 'muted' }, t('Preparing the waveform…'));
    waveSection.replaceChildren(note, bar);
    let st = await api('POST', 'api/analysis?path=' + q(path));
    while (ed.alive && st.state === 'running') {
      bar.value = st.progress;
      note.textContent = st.stage === 'convert' ? (info.raw && isVideo(info.raw.name) ? t('Taking the sound out of the video…') : t('Converting the recording to FLAC…')) : t('Preparing the waveform…');
      await new Promise((r) => setTimeout(r, 500));
      st = await api('GET', 'api/analysis?path=' + q(path));
    }
    if (!ed.alive) return;
    if (st.state !== 'done') {
      waveSection.replaceChildren(h('p', { class: 'error' }, t('Could not read the recording: {error}', { error: st.error ? tr(st.error) : st.state })));
      return;
    }
    if (info.raw && converts(info.raw.name)) { // it became FLAC (or .mka) meanwhile
      await ed.refreshInfo();
      ed.check();
    }
    const peaks = await api('GET', 'api/peaks?path=' + q(path));
    if (!ed.alive) return;
    buildWaveform(peaks, forTenant('api/preview?path=' + q(path) + '&v=' + st.key));
  }

  function buildWaveform(data, previewURL) {
    ed.duration = data.duration;
    silenceEdges = data.silences.flatMap((s) => [s.start, s.end]);
    const minimapEl = h('div', { id: 'minimap' });
    const waveEl = h('div', { id: 'waveform' });
    const zoom = h('input', { type: 'range', min: 0, max: 100, step: 'any', value: 0, 'aria-label': t('Zoom'), oninput: () => applyZoom() });
    const clock = h('span', { class: 'clock' });
    const playButton = h('button', { onclick: () => ed.ws.playPause(), title: t('Play / pause (space)') }, t('▶ Play'));
    const button = (label, title, fn) => h('button', { title, onclick: fn }, label);
    // Play skips what is left out (the published parts), or plays the recording as it is.
    const skipBox = h('input', { type: 'checkbox', name: 'play-skip', checked: playMode === 'published', onchange: (e) => {
      playMode = e.target.checked ? 'published' : 'recording';
      localStorage.setItem('playMode', playMode);
    } });
    waveSection.replaceChildren(
      minimapEl, waveEl,
      h('div', { class: 'controls' },
        h('div', { class: 'group wide' },
          h('strong', {}, t('Navigate')),
          h('div', { class: 'toolbar' },
            playButton,
            h('label', { class: 'check play-skip', title: t('On: Play jumps over the cuts and the parts between the sections, and waits out the pauses, as the sermon will be published. Off: the recording plays as it is.') },
              skipBox, ' ', t('Skip what is left out')),
            h('span', { class: 'sep' }),
            button(t('◀ silence'), t('Previous silence edge ([)'), () => jump(-1)),
            button(t('silence ▶'), t('Next silence edge (])'), () => jump(1)),
            h('span', { class: 'sep' }),
            button(t('Hear start'), t('Play 5 s from the start of the sermon'), hearStart),
            button(t('Hear end'), t('Play the last 5 s of the sermon'), hearEnd),
            button(t('Hear the whole sermon'), t('Play all the sections as they will be published (p)'), playSelection),
            h('span', { class: 'sep' }),
            h('span', { class: 'zoom' },
              button('−', t('Zoom out (-)'), () => { zoom.value = Math.max(0, +zoom.value - 10); applyZoom(); }),
              zoom,
              button('+', t('Zoom in (+)'), () => { zoom.value = Math.min(100, +zoom.value + 10); applyZoom(); })),
            clock)),
        ed.sectionsGroup(),
        ed.cutsGroup()),
      h('p', { class: 'touch-hint' },
        t('Drag along the waveform to select the sermon. Drag on an empty part to add another section. Drag the edges to adjust. To cut a part out, press Cut out here where it starts and End the cut here where it ends. Grey marks silence.')),
      h('p', { class: 'keys' },
        kbdText(t('Drag on the waveform to select the sermon. Drag on an empty part to add another section, or hold <kbd>shift</kbd> and drag to cut out a part. Drag the edges to adjust. Grey marks silence.')), ' ',
        h('button', { type: 'button', class: 'link', title: t('All the keys of the editor (?)'), onclick: showKeys }, t('Keys…'))),
    );
    ed.renderCuts();

    const regions = ed.regions = RegionsPlugin.create();
    const ws = ed.ws = WaveSurfer.create({
      container: waveEl,
      height: 180,
      waveColor: css('--wave'),
      progressColor: css('--wave-progress'),
      cursorColor: css('--fg'),
      cursorWidth: 1,
      normalize: true,
      autoScroll: true,
      autoCenter: false,
      plugins: [
        regions,
        TimelinePlugin.create({ formatTimeCallback: (s) => formatTime(s, 0) }),
        MinimapPlugin.create({ container: minimapEl, height: 40, waveColor: css('--wave'), progressColor: css('--wave-progress'), overlayColor: 'rgba(128,128,128,0.25)' }),
        HoverPlugin.create({ formatTimeCallback: (s) => formatTime(s, 1), lineColor: css('--muted'), labelBackground: css('--panel'), labelColor: css('--fg') }),
      ],
    });
    cleanups.push(() => ws.destroy());
    // wavesurfer 8 keeps a seek made while the browser is still seeking for later, and the earlier seek's
    // 'seeking' event then drops it, so quick key presses were lost. Seek the media element as well.
    const setTime = ws.setTime.bind(ws);
    ws.setTime = (time) => {
      setTime(time);
      const media = ws.getMediaElement();
      if (media && media.readyState >= HTMLMediaElement.HAVE_METADATA) media.currentTime = ws.getCurrentTime();
    };
    // Peaks and ed.duration up front: the browser never downloads or decodes
    // the whole recording, it only streams the preview while playing.
    ws.load(previewURL, [data.peaks], data.duration).catch((e) => {
      if (ed.alive) waveSection.prepend(h('p', { class: 'error' }, t('Could not load the preview: {error}', { error: e.message })));
    });

    ws.on('play', () => { playButton.textContent = t('⏸ Pause'); });
    ws.on('pause', () => { playButton.textContent = t('▶ Play'); });
    ws.on('timeupdate', (now) => ed.followOpenCut(now));
    ws.on('timeupdate', (now) => {
      clock.textContent = `${formatTime(now, 1)} / ${formatTime(ed.duration, 0)}`;
      if (stopAt !== null && now >= stopAt) {
        stopAt = null;
        skipNow = false;
        ws.pause();
        return;
      }
      if ((playMode === 'published' || skipNow) && ws.isPlaying()) {
        // A pause is waited out where it falls, once.
        const wait = ed.activePauses().find((p) => lastTime < p.at && now >= p.at && now - p.at < 0.5);
        lastTime = now;
        if (wait && !pausing) {
          pausing = true;
          ws.pause();
          setTimeout(() => { if (pausing && ed.alive) { pausing = false; ws.play(); } }, wait.seconds * 1000);
          return;
        }
        const cut = skipped(sections, cuts).find((c) => now >= c.start - 0.03 && now < c.end);
        if (cut) ws.setTime(cut.end);
      }
      lastTime = now;
    });
    ws.on('pause', () => { if (!pausing) skipNow = false; });
    ws.on('play', () => { pausing = false; });
    // The waveform is one of the page's players: another that starts pauses it, also while it waits out a pause.
    cleanups.push(soloPlayer(ws.getMediaElement()));
    const otherPlays = (e) => { if (e.target !== ws.getMediaElement()) pausing = false; };
    document.addEventListener('play', otherPlays, true);
    cleanups.push(() => document.removeEventListener('play', otherPlays, true));
    clock.textContent = `0:00.0 / ${formatTime(ed.duration, 0)}`;

    ws.once('ready', () => {
      data.silences.forEach((s, i) => {
        const r = regions.addRegion({ id: 'silence-' + i, start: s.start, end: s.end, color: css('--silence'), drag: false, resize: false });
        if (r.element) r.element.style.pointerEvents = 'none'; // selections can start on a silence
      });
      regions.enableDragSelection({ color: css('--selection') });
      const initial = ed.sermon.adjusted || ed.sermon.saved;
      if (initial) {
        ed.applySettings(initial);
        ed.lastSent = JSON.stringify(ed.currentSettings()); // as the page shows it: opening is not an adjustment
      } else ed.proposeSection(data.silences);
      ed.startHistory();
    });
    ed.buildFades();
    // A drag on the waveform makes a region: it becomes a section. Sections
    // added by the page are created with their id, and are kept by ed.addSection.
    regions.on('region-created', (r) => {
      if (/^(silence|cut|section|pause)-/.test(r.id)) return;
      ed.addDrawn(r);
    });
    // Shift-drag draws a cut. It is handled here, not by the regions plugin,
    // because it must work on top of the selection, which a plain drag would move.
    waveEl.addEventListener('pointerdown', (down) => {
      if (!down.shiftKey || down.button !== 0 || !sections.length) return;
      down.preventDefault();
      down.stopPropagation();
      const timeAt = (e) => {
        const box = ws.getWrapper().getBoundingClientRect();
        return Math.max(0, Math.min(((e.clientX - box.left) / box.width) * ed.duration, ed.duration));
      };
      const from = timeAt(down);
      let cut = null;
      const move = (e) => {
        if (!cut && Math.abs(e.clientX - down.clientX) < 3) return;
        const start = Math.min(from, timeAt(e)), end = Math.max(from, timeAt(e));
        if (!cut) cut = ed.addCut(start, Math.max(end, start + 0.05));
        else { cut.region.setOptions({ start, end }); ed.syncCuts(); }
      };
      const up = () => {
        window.removeEventListener('pointermove', move);
        if (cut) { ed.sortCuts(); ed.renderCuts(); ed.changed(); }
        const noSeek = (e) => e.stopPropagation(); // the click that ends the drag must not move the playhead
        waveEl.addEventListener('click', noSeek, true);
        setTimeout(() => waveEl.removeEventListener('click', noSeek, true), 0);
      };
      window.addEventListener('pointermove', move);
      window.addEventListener('pointerup', up, { once: true });
    }, true);
    regions.on('region-update', (r) => {
      if (ed.sectionOf(r)) { ed.syncSections(); ed.syncCuts(); ed.changed(); } else if (ed.cutOf(r)) ed.syncCuts();
    });
    regions.on('region-updated', (r) => {
      if (ed.sectionOf(r)) ed.tidySections();
      else if (ed.cutOf(r)) { ed.sortCuts(); ed.renderCuts(); ed.changed(); }
    });
    // A click on a section or cut selects it (its row is marked); a double-click zooms to it.
    regions.on('region-clicked', (r) => ed.select(ed.sectionOf(r) || ed.cutOf(r) || null));
    regions.on('region-double-clicked', (r) => {
      const x = ed.sectionOf(r) || ed.cutOf(r);
      if (!x) return;
      ed.select(x);
      zoomTo(r.start, r.end);
    });

    // zoomTo zooms so start..end fills most of the waveform, and scrolls to it.
    function zoomTo(start, end) {
      const fit = waveEl.clientWidth / ed.duration;
      const want = (waveEl.clientWidth * 0.9) / Math.max(end - start, 0.05);
      const top = Math.max(MAX_ZOOM / fit, 1);
      zoom.value = top > 1 ? Math.min(100, Math.max(0, (100 * Math.log(Math.max(want / fit, 1))) / Math.log(top))) : 0;
      applyZoom({ time: start, x: waveEl.clientWidth * 0.05 });
    }

    // applyZoom zooms to the slider's value. With at ({ time, x }), time stays
    // x pixels from the left edge (under the mouse); without, the playhead is kept in view.
    function applyZoom(at) {
      const fit = waveEl.clientWidth / ed.duration;
      const px = fit * Math.pow(Math.max(MAX_ZOOM / fit, 1), zoom.value / 100);
      ws.zoom(px);
      if (at) ws.setScroll(Math.max(0, at.time * (ws.getWrapper().scrollWidth / ed.duration) - at.x));
      else reveal(ws.getCurrentTime());
    }
    // The wheel zooms at the mouse: always with ctrl/cmd (also a trackpad pinch),
    // without unless Settings asks for the key. Shift or sideways scrolling is
    // left to the browser, which moves along the waveform.
    waveEl.addEventListener('wheel', (e) => {
      const key = e.ctrlKey || e.metaKey;
      if (!key && (e.shiftKey || wheelZoomNeedsKey() || Math.abs(e.deltaX) > Math.abs(e.deltaY))) return;
      const pixels = e.deltaY * (e.deltaMode === 1 ? 33 : e.deltaMode === 2 ? 600 : 1);
      const next = Math.min(100, Math.max(0, +zoom.value - Math.max(-10, Math.min(pixels / 20, 10))));
      if (next === +zoom.value && !key) return; // all the way in or out: the page scrolls
      e.preventDefault();
      if (next === +zoom.value) return;
      const box = ws.getWrapper().getBoundingClientRect();
      const x = e.clientX - waveEl.getBoundingClientRect().left;
      const time = Math.max(0, Math.min(((e.clientX - box.left) / box.width) * ed.duration, ed.duration));
      zoom.value = next;
      applyZoom({ time, x });
    }, { passive: false });

    function onKey(e) {
      if (typing(e) || e.ctrlKey || e.metaKey || e.altKey || document.querySelector('dialog[open]')) return;
      const step = e.shiftKey ? 0.1 : 1;
      const keys = {
        ' ': () => ws.playPause(),
        s: ed.markStart, e: ed.markEnd, p: playSelection, c: ed.addCutHere, x: ed.splitHere,
        '[': () => jump(-1), ']': () => jump(1),
        '+': () => { zoom.value = Math.min(100, +zoom.value + 10); applyZoom(); },
        '=': () => { zoom.value = Math.min(100, +zoom.value + 10); applyZoom(); },
        '-': () => { zoom.value = Math.max(0, +zoom.value - 10); applyZoom(); },
        '0': () => { zoom.value = 0; applyZoom(); },
        ArrowLeft: () => seek(ws.getCurrentTime() - step),
        ArrowRight: () => seek(ws.getCurrentTime() + step),
        Home: () => seek(0),
        End: () => seek(ed.duration),
        Delete: ed.removeSelected,
        Backspace: ed.removeSelected,
        ',': () => ed.nudge(-step / 10),
        '.': () => ed.nudge(step / 10),
        '<': () => ed.nudge(-step / 10),
        '>': () => ed.nudge(step / 10),
        Escape: () => { if (!ed.cancelCut()) ed.select(null); },
        '?': showKeys,
      };
      const fn = keys[e.key];
      if (fn) {
        e.preventDefault();
        fn();
      }
    }
    document.addEventListener('keydown', onKey);
    cleanups.push(() => document.removeEventListener('keydown', onKey));

    const spans = (list) => list.map((x) => ({ start: x.region.start, end: x.region.end }));
    window.__editor = { ws, regions, get sections() { return spans(sections); }, get cuts() { return spans(cuts); }, addSection: ed.addSection, addCut: ed.addCut, splitHere: ed.splitHere, setFade: ed.setFade, undo: ed.undo, redo: ed.redo, get selected() { return ed.selection(); }, get history() { return ed.history(); }, get fades() { return { fadeIn: opts.fadeIn, fadeOut: opts.fadeOut }; } }; // for tests and the console
    cleanups.push(() => { delete window.__editor; });
  }

  // showKeys lists every key of the editor, and what the mouse wheel does.
  function showKeys() {
    const row = (keys, what) => h('tr', {}, h('th', { scope: 'row' }, kbdText(keys)), h('td', {}, what));
    infoDialog({
      title: t('Keys'),
      body: [
        h('table', { class: 'keys-table' }, h('tbody', {},
          row(t('<kbd>space</kbd>'), t('Play or pause')),
          row('<kbd>p</kbd>', t('Play the whole sermon, as it will be published')),
          row('<kbd>s</kbd> / <kbd>e</kbd>', t('The section at the playhead starts or ends there')),
          row('<kbd>x</kbd>', t('Split the section at the playhead')),
          row('<kbd>c</kbd>', t('Start a cut at the playhead; again: end it there')),
          row('<kbd>[</kbd> / <kbd>]</kbd>', t('The previous or next silence')),
          row('<kbd>←</kbd> / <kbd>→</kbd>', t('1 s back or on (shift: 0.1 s)')),
          row('<kbd>Home</kbd> / <kbd>End</kbd>', t('The start or the end of the recording')),
          row('<kbd>+</kbd> / <kbd>-</kbd> / <kbd>0</kbd>', t('Zoom in, out, or all the way out')),
          row('<kbd>Delete</kbd>', t('Remove the selected section or cut')),
          row('<kbd>,</kbd> / <kbd>.</kbd>', t('Move the edge of the selected one that is nearest the playhead by 0.1 s (shift: 0.01 s)')),
          row('<kbd>Esc</kbd>', t('Let go of the selection, or of a cut being made')),
          row('<kbd>ctrl</kbd>+<kbd>z</kbd> / <kbd>ctrl</kbd>+<kbd>shift</kbd>+<kbd>z</kbd>', t('Undo or redo')),
          row('<kbd>?</kbd>', t('This list')))),
        h('p', { class: 'muted' }, kbdText(wheelZoomNeedsKey()
          ? t('<kbd>ctrl</kbd>+scroll zooms at the mouse, <kbd>shift</kbd>+scroll moves along (Settings › Waveform).')
          : t('Scroll zooms at the mouse, <kbd>shift</kbd>+scroll moves along (Settings › Waveform).'))),
      ],
    });
  }

  function seek(t) {
    t = Math.max(0, Math.min(t, ed.duration));
    ed.ws.setTime(t);
    reveal(t);
  }

  // reveal scrolls a zoomed waveform so t is in view.
  function reveal(t) {
    if (!ed.ws) return;
    const wrapper = ed.ws.getWrapper();
    const visible = (wrapper.parentElement?.clientWidth || wrapper.clientWidth) / (wrapper.scrollWidth / ed.duration);
    const left = ed.ws.getScroll() / (wrapper.scrollWidth / ed.duration);
    if (t < left || t > left + visible) ed.ws.setScrollTime(Math.max(0, t - visible / 2));
  }

  function jump(dir) {
    const t = ed.ws.getCurrentTime();
    const edge = dir < 0
      ? [...silenceEdges].reverse().find((e) => e < t - 0.05) ?? 0
      : silenceEdges.find((e) => e > t + 0.05) ?? ed.duration;
    seek(edge);
  }

  // playFor plays from a time and stops after some seconds; skipCuts makes it
  // jump over the cuts even when the whole recording is the Play button's choice.
  function playFor(from, seconds, skipCuts = false) {
    ed.ws.setTime(from);
    stopAt = from + seconds;
    skipNow = skipCuts;
    ed.ws.play();
  }

  // playSelection plays the sermon as it will be published: all the sections, without the gaps
  // between them and the cuts, with the pauses.
  function playSelection() {
    if (sections.length) playFor(ed.first(), ed.last() - ed.first(), true);
  }

  function hearStart() {
    if (sections.length) playFor(ed.first(), Math.min(5, ed.last() - ed.first()), true);
  }

  function hearEnd() {
    if (sections.length) playFor(Math.max(ed.last() - 5, ed.first()), Math.min(5, ed.last() - ed.first()), true);
  }


  return { waveSection, showWaveform, reveal, playFor };
}
