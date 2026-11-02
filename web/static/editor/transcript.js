// The editor's transcript: the one there or being made (transcriptPanel), and the choice of the language and the
// model to make it with (modelPicker), when the studio makes transcripts.
import { t } from '../i18n.js';
import { api, h } from '../util.js';
import { transcriptPanel, modelPicker } from '../transcripts.js';

/** @param {import('./common.js').Editor} ed */
export function transcriptPart(ed) {
  const { path, info } = ed;
  // A place on the page, which becomes the section once there are models.
  let transcriptEl = h('div', { hidden: true });
  let picker = null;
  const panel = transcriptPanel(path, info, { chosen: true, onDraw: () => picker && picker.draw() });

  // showTranscript shows it once the models are known: not without transcripts in the studio, or models.
  async function showTranscript() {
    if (!info.transcript) return;
    const data = await api('GET', 'api/transcription').catch(() => ({ available: false }));
    if (!data.available || !(data.models || []).length || !ed.alive) return;
    picker = modelPicker(data, panel);
    const section = h('section', { class: 'step', 'aria-labelledby': 'step-transcript' },
      h('h2', { id: 'step-transcript' }, t('Transcript')), picker.el); // with the transcript at its top
    transcriptEl.replaceWith(section);
    transcriptEl = section;
  }

  return { transcriptEl, showTranscript, renderTranscript: (tx) => panel.update(tx) };
}
