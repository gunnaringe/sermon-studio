// The editor's output: the formats, what is there, the check of the edit, and Save, which keeps the edit as the
// saved version and makes the files (a job, followed here).
import { confirmDialog } from '../dialog.js';
import { t, tr } from '../i18n.js';
import { api, q, h, debounce, forTenant } from '../util.js';
import { spanOf } from './common.js';

const FORMATS = [['flac', 'FLAC'], ['mp3', 'MP3'], ['opus', 'Opus']];

/** @param {import('./common.js').Editor} ed */
export function outputPart(ed) {
  const { path, info, opts, tags, sections, cuts, cleanups } = ed;
  let job = info.job;
  let lastCheck = { error: t('Checking…') };
  let events = null; // the job's event stream
  cleanups.push(() => events && events.close());
  const pills = h('div', { class: 'pills', role: 'group', 'aria-label': t('Formats') });
  const outputNote = h('p', { class: 'muted' });
  const checkOut = h('div', {});
  const saveButton = h('button', { class: 'primary', disabled: true, title: t('Make this the saved version and make the files'), onclick: save }, t('Save'));
  const jobPanel = h('section', {});

  // request is the run as the server takes it, and as it checks it.
  /** @returns {Record<string, any>} */
  function request(force = false) {
    return {
      ...opts,
      force,
      ...(info.tags ? tags : {}),
      noiseReduction: Math.round(opts.noiseReduction),
      sections: sections.map(spanOf),
      cuts: sections.length ? cuts.map(spanOf) : [],
    };
  }

  // check asks the server what the edit would make, and whether it can.
  const check = debounce(async () => {
    if (!ed.alive) return;
    const req = request();
    if ([req.fadeIn, req.fadeOut, req.noiseReduction, req.targetLufs, req.truePeak].some(Number.isNaN)) {
      showCheck({ error: t('Fill in all numbers.') });
      return;
    }
    try {
      // All three are checked: whether each is there and up to date does not
      // depend on which are chosen.
      showCheck(await api('POST', 'api/check?path=' + q(path), { ...req, flac: true, mp3: true, opus: true }));
    } catch (e) {
      showCheck({ error: e.message });
    }
  }, 250);

  function showCheck(res) {
    lastCheck = res;
    checkOut.replaceChildren(
      res.error ? h('p', { class: 'error' }, tr(res.error)) : '',
      ...(res.notices || []).map((n) => h('p', { class: 'notice' }, tr(n))));
    renderOutputs();
  }

  function busy() { return job && (job.state === 'queued' || job.state === 'running'); }

  // outputState is a format's file: there or not, and whether Save would make
  // it again (stale: made from other settings; unknown: no record).
  function outputState(format) {
    const file = info.outputs.find((o) => o.format === format) || null;
    const st = (lastCheck.outputs || {})[format];
    return { file, stale: Boolean(file && st && (st.stale || !st.known)), unknown: Boolean(file && st && !st.known) };
  }

  // toMake is what Save makes: the chosen formats that are missing or out
  // of date; toDelete what it deletes: the files of the formats not chosen.
  const toMake = () => FORMATS.map(([f]) => f).filter((f) => opts[f] && (!outputState(f).file || outputState(f).stale));
  const toDelete = () => FORMATS.map(([f]) => f).filter((f) => !opts[f] && outputState(f).file);
  const labels = (list) => list.map((f) => FORMATS.find(([x]) => x === f)[1]).join(', ');

  function renderOutputs() {
    pills.replaceChildren(...FORMATS.map(([format, label]) => {
      const { file, stale } = outputState(format);
      const chosen = Boolean(opts[format]);
      const note = !file ? (chosen ? t('will be made') : t('not made'))
        : !chosen ? t('will be deleted')
          : stale ? t('out of date, will be made again') : t('up to date');
      return h('div', { 'data-format': format, class: 'pill' + (file ? ' exists' : '') + (chosen ? ' chosen' : '') + (stale ? ' stale' : '') },
        h('button', {
          class: 'pick', 'aria-pressed': String(chosen), title: chosen ? t('Leave {format} out', { format: label }) : t('Make {format} too', { format: label }),
          onclick: () => { opts[format] = !opts[format]; renderOutputs(); },
        }, h('span', { class: 'box', 'aria-hidden': 'true' }, chosen ? '☑' : '☐'), ' ', label, file ? ' ✓' : ''),
        h('span', { class: 'state' + (file && !chosen ? ' delete' : chosen && (stale || !file) ? ' will' : '') }, note));
    }));
    const make = toMake(), del = toDelete(), keep = unsaved();
    const blocked = (make.length > 0 || keep) && Boolean(lastCheck.error); // deleting does not need a valid edit
    outputNote.textContent = blocked ? ''
      : make.length || del.length || keep
        ? t('Save {what}.', { what: [keep ? t('keeps the edit') : '', make.length ? t('makes: {formats}', { formats: labels(make) }) : '', del.length ? t('deletes: {formats}', { formats: labels(del) }) : '']
          .filter(Boolean).join(', ') })
        : t('Saved, and everything chosen is up to date.');
    saveButton.disabled = blocked || busy() || !(make.length || del.length || keep);
  }

  // unsaved: the edit on the page is not the saved version.
  const unsaved = () => Boolean(ed.sermon.adjusted) || (!ed.sermon.saved && sections.length > 0);

  // save makes the edit the saved version, then deletes the files of the
  // formats not chosen (after asking) and makes the chosen ones that are
  // missing or out of date. The edit is saved even when the files cannot be
  // made now: the recordings page shows them as out of date.
  async function save() {
    if (ed.proposed) ed.adopt(); // the start the page made has not been kept yet
    await ed.persistAdjusted();
    const make = toMake(), del = toDelete();
    const replaced = make.map(outputState).filter((x) => x.file).map((x) => x.file.name);
    const deleted = del.map((f) => outputState(f).file.name);
    if (deleted.length) {
      const ok = await confirmDialog({
        title: t('Delete files?'),
        body: [h('p', {}, t('These formats are not chosen any more, so their files are deleted:')),
          h('ul', {}, deleted.map((n) => h('li', {}, n)))],
        confirm: t('Save and delete'),
        danger: true,
      });
      if (!ok) return;
    }
    saveButton.disabled = true;
    try {
      if (unsaved()) {
        ed.sermon = await api('POST', 'api/sermon/save?path=' + q(path));
        ed.setSaveNote(t('Saved.'));
        ed.renderVersion();
      }
    } catch (e) {
      ed.setSaveNote(t('Not saved: {error}', { error: e.message }));
      renderOutputs();
      return;
    }
    try {
      if (del.length) {
        const inf = await api('POST', 'api/outputs/remove?path=' + q(path) + del.map((f) => '&format=' + f).join(''));
        info.outputs = inf.outputs;
        renderOutputs();
        check();
      }
      if (!make.length) return;
      const req = request(replaced.length > 0);
      for (const [f] of FORMATS) req[f] = make.includes(f);
      job = await api('POST', 'api/run?path=' + q(path), req);
      showJob(job);
      follow(job.id);
    } catch (e) {
      showCheck({ ...lastCheck, error: t('Saved, but the files were not made: {error}', { error: e.message }) });
    }
  }

  function follow(id) {
    if (events) events.close();
    events = new EventSource(forTenant('api/jobs/' + id + '/events'));
    events.onmessage = (e) => {
      job = JSON.parse(e.data);
      showJob(job);
      if (!busy()) {
        events.close();
        events = null;
        ed.refreshInfo().then(check).catch(() => {}); // the outputs changed
      }
    };
  }

  function showJob(j) {
    const running = j.state === 'queued' || j.state === 'running';
    const title = { queued: t('Waiting for another run to finish…'), running: t('Processing…'), done: t('Done'), failed: t('Failed'), cancelled: t('Cancelled') }[j.state];
    const steps = j.steps.map((s, i) => {
      const last = i === j.steps.length - 1;
      const n = s.match(/^(\d+)\. (.*)$/s); // "2. Master (…)": the number stays, the rest is translated
      return h('li', { class: last && running ? 'current' : last && j.state === 'failed' ? 'failed' : '' }, n ? `${n[1]}. ${tr(n[2])}` : tr(s));
    });
    const log = h('pre', { class: 'log' }, j.log.join('\n'));
    jobPanel.replaceChildren(h('div', { class: 'panel' },
      h('h2', {}, title),
      h('ul', { class: 'steps' }, steps),
      running ? h('progress', { max: 1, value: j.progress }) : '',
      j.warnings.length ? h('div', {}, j.warnings.map((w) => h('p', { class: 'warn' }, '⚠ ' + tr(w)))) : '',
      j.error ? h('p', { class: 'error' }, tr(j.error)) : '',
      j.state === 'cancelled' ? h('p', {}, t('Cancelled. Nothing half-written was left behind.')) : '',
      j.state === 'done' ? h('p', {}, t('Done. Listen to and download the files under Recordings.')) : '',
      h('details', { open: running }, h('summary', {}, t('Log')), log),
      running ? h('button', { onclick: cancel }, t('Cancel')) : ''));
    log.scrollTop = log.scrollHeight;
    renderOutputs();
  }

  async function cancel() {
    try {
      job = await api('POST', 'api/jobs/' + job.id + '/cancel');
      showJob(job);
    } catch (e) {
      jobPanel.prepend(h('p', { class: 'error' }, tr(e.message)));
    }
  }

  // showJobs shows the job the page opened with, and follows it while it runs.
  function showJobs() {
    if (!job) return;
    showJob(job);
    if (busy()) follow(job.id);
  }

  return {
    outputEl: h('section', { class: 'step', 'aria-labelledby': 'step-output' }, h('h2', { id: 'step-output' }, t('Output')),
      h('div', { class: 'panel' }, pills),
      jobPanel),
    saveWhat: h('div', { class: 'save-what' }, outputNote, checkOut),
    saveButton, request, check, renderOutputs, showJobs,
  };
}
