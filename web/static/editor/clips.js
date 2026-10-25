// The editor's intro and outro: the clips around the sermon, the church's or the sermon's own (uploaded for it
// here), and the joins to hear them.
import { alertDialog } from '../dialog.js';
import { t, tr } from '../i18n.js';
import { api, q, h, INPUT_TYPES, formatTime, forTenant, isAdmin } from '../util.js';
import { CLIP_KEYS } from './common.js';

/** @param {import('./common.js').Editor} ed */
export function clipsPart(ed) {
  const { path, opts, sections, cleanups } = ed;
  const clipsBox = h('div', { class: 'panel clip-choice' });
  const clipsSection = h('section', { class: 'step', 'aria-labelledby': 'step-clips', hidden: true },
    h('h2', { id: 'step-clips' }, t('Intro and outro')), clipsBox);
  let clipData = null; // api/sermon/clips: the church's clips and defaults, the sermon's own, uploads under way
  let clipPoll = null;
  const awaitingOwn = {}; // an own clip uploaded for intro or outro: chosen once it is ready
  const clipLength = h('p', { class: 'muted', 'aria-live': 'polite' });
  const clipNote = h('p', { class: 'muted', role: 'status' });
  const joinPlayer = h('audio', { controls: true, hidden: true, 'aria-label': t('The join') });
  cleanups.push(() => URL.revokeObjectURL(joinPlayer.src));
  const ownInput = h('input', { type: 'file', name: 'own-clip', accept: INPUT_TYPES.join(','), hidden: true });
  let ownFor = null; // which of the two the file chosen is for
  const UPLOAD = '__upload';
  cleanups.push(() => clearTimeout(clipPoll));

  // clipChoices are the choices as they are kept with the sermon: none is left out.
  function clipChoices() {
    return Object.fromEntries(CLIP_KEYS.filter((k) => opts[k]).map((k) => [k, opts[k]]));
  }

  async function loadClips() {
    try {
      showClips(await api('GET', 'api/sermon/clips?path=' + q(path)));
    } catch { /* without clips the step stays hidden */ }
  }

  function showClips(data) {
    clipData = data;
    clipsSection.hidden = !data.available;
    for (const which of CLIP_KEYS) {
      const p = data.ownPending[which];
      if (!awaitingOwn[which] || (p && !p.error)) continue;
      delete awaitingOwn[which];
      if (!p && data.own[which]) { // ready: the sermon's own clip becomes the choice
        opts[which] = 'own';
        ed.changed();
      }
    }
    clearTimeout(clipPoll);
    if (ed.alive && Object.values(data.ownPending).some((p) => !p.error)) clipPoll = setTimeout(loadClips, 1000);
    drawClips();
  }

  // clipOf is what a choice means now: the clip (with its address to listen to), null for none, or
  // { missing: true } when the clip it chose is gone.
  function clipOf(which, choice) {
    const d = clipData;
    if (!d || !choice) return null;
    if (choice === 'own') {
      const c = d.own[which];
      return c ? { ...c, url: forTenant('api/sermon/clip/audio?path=' + q(path) + '&which=' + which) + '&v=' + c.version } : { missing: true, own: true };
    }
    const id = choice === 'default' ? d[which] : choice;
    if (!id) return null;
    const c = d.clips.find((x) => x.id === id);
    return c ? { ...c, url: forTenant('api/clips/' + c.id + '/audio') + '&v=' + c.version } : { missing: true };
  }

  function drawClips() {
    if (!clipData) return;
    const d = clipData;
    const rows = CLIP_KEYS.map((which) => {
      const choice = opts[which] || '';
      const label = which === 'intro' ? t('Intro') : t('Outro');
      const def = d.clips.find((c) => c.id === d[which]);
      const options = [
        ['default', def ? t('Church default: {name}', { name: def.name }) : t('Church default: none')],
        ['', t('None')],
        ...d.clips.filter((c) => !c.archived || c.id === choice).map((c) => [c.id, c.archived ? t('{name} (archived)', { name: c.name }) : c.name]),
      ];
      if (d.own[which] || choice === 'own') {
        options.push(['own', d.own[which] ? t("This sermon's own: {name}", { name: d.own[which].name }) : t("This sermon's own (missing)")]);
      }
      if (!options.some(([v]) => v === choice)) options.push([choice, t('A deleted clip')]);
      options.push([UPLOAD, t('Upload for this sermon only…')]);
      const select = h('select', { name: which, 'aria-label': label, onchange: (e) => {
        if (e.target.value === UPLOAD) {
          e.target.value = choice;
          ownFor = which;
          ownInput.click();
          return;
        }
        opts[which] = e.target.value;
        clipNote.textContent = '';
        ed.changed();
        drawClips();
      } }, options.map(([v, text]) => h('option', { value: v, selected: v === choice }, text)));
      const clip = clipOf(which, choice);
      const pending = d.ownPending[which];
      const parts = [h('label', {}, h('span', { class: 'name' }, label), select)];
      if (clip && clip.url) {
        parts.push(h('span', { class: 'muted' }, formatTime(clip.seconds, 0)),
          h('audio', { controls: true, preload: 'none', src: clip.url, 'aria-label': t('Listen to {name}', { name: clip.name }) }));
      }
      if (clip && clip.url) {
        parts.push(h('button', { type: 'button', name: 'join-' + which, disabled: !sections.length, onclick: (e) => hearJoin(which, e.target) },
          which === 'intro' ? t('Hear the intro join') : t('Hear the outro join')));
      }
      if (isAdmin() && choice === 'own' && d.own[which] && d.available && !d.clips.some((c) => c.id === d.own[which].copiedAs)) {
        parts.push(h('button', { type: 'button', name: 'add-' + which, onclick: async () => {
          try {
            showClips(await api('POST', 'api/sermon/clip/add?path=' + q(path) + '&which=' + which));
            clipNote.textContent = t("{name} is among the church's clips now.", { name: d.own[which].name });
          } catch (e) { alertDialog({ body: e.message }); }
        } }, t("Add to the church's clips")));
      }
      if (clip && clip.missing) {
        parts.push(h('p', { class: 'error' }, clip.own ? t("This sermon's own clip is missing. Upload it again, or choose another.")
          : which === 'intro' ? t('The intro this sermon used has been deleted. Choose another intro, or none.')
            : t('The outro this sermon used has been deleted. Choose another outro, or none.')));
      }
      if (pending) {
        parts.push(pending.error
          ? h('p', { class: 'error' }, t('{name}: {error}', { name: pending.name, error: tr(pending.error) }), ' ',
            h('button', { type: 'button', name: 'dismiss-' + which, onclick: async () => showClips(await api('POST', 'api/sermon/clip/dismiss?path=' + q(path) + '&which=' + which)) }, t('Remove')))
          : h('p', { class: 'muted' }, t('Preparing {name}…', { name: pending.name })));
      }
      return h('div', { class: 'clip-row', 'data-which': which }, parts);
    });
    clipsBox.replaceChildren(...rows, clipLength, joinPlayer, clipNote, ownInput);
    showClipLength();
  }

  // hearJoin makes and plays the end of the intro and the start of the sermon (or the end of the sermon and
  // the start of the outro), made as the files will be.
  async function hearJoin(which, button) {
    button.disabled = true;
    clipNote.className = 'muted';
    clipNote.textContent = t('Making the join…');
    try {
      const resp = await fetch(forTenant('api/sample/join?path=' + q(path) + '&which=' + which), {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(ed.request()),
      });
      if (!resp.ok) throw new Error(tr((await resp.json().catch(() => ({}))).error || `${resp.status} ${resp.statusText}`));
      URL.revokeObjectURL(joinPlayer.src);
      joinPlayer.src = URL.createObjectURL(await resp.blob());
      joinPlayer.hidden = false;
      clipNote.textContent = '';
      joinPlayer.play().catch(() => {});
    } catch (e) {
      clipNote.className = 'error';
      clipNote.textContent = t('Could not make the join: {error}', { error: e.message });
    } finally {
      button.disabled = false;
    }
  }

  // showClipLength tells how long the files with the intro and outro are.
  function showClipLength() {
    if (!clipData) return;
    const clips = CLIP_KEYS.map((w) => clipOf(w, opts[w])).filter((c) => c && !c.missing);
    if (!clips.length || !sections.length) {
      clipLength.textContent = clips.length ? '' : t('No intro or outro: the files are the sermon only.');
      return;
    }
    const sermon = ed.keptLength() + ed.activePauses().reduce((n, p) => n + p.seconds, 0);
    const total = sermon + clips.reduce((n, c) => n + c.seconds + clipData.pause, 0);
    clipLength.textContent = t('With the intro and outro: {time}. The sermon only is made too.', { time: formatTime(total, 1) });
  }

  ownInput.addEventListener('change', async () => {
    const file = ownInput.files[0];
    ownInput.value = '';
    if (!file || !ownFor) return;
    const which = ownFor;
    const url = 'api/sermon/clip?path=' + q(path) + '&which=' + which + '&name=' + q(file.name);
    let body;
    try {
      body = await api('POST', url, file);
    } catch (e) {
      alertDialog({ body: e.message });
      return;
    }
    awaitingOwn[which] = true;
    showClips(body);
  });


  return { clipsSection, loadClips, clipChoices, drawClips, showClipLength };
}
