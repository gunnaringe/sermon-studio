// The repair of the sound before its noise is taken out: a low cut against rumble, and the removal of clicks
// (normalizer.Repair). A simple choice for each, with the exact values under Advanced. The editor's processing
// and the church's default processing both use it.
import { h } from './util.js';
import { t } from './i18n.js';

// REPAIR_KEYS are the sound's keys this sets.
export const REPAIR_KEYS = ['lowCut', 'lowCutSteep', 'declick', 'declickWindow'];

// The click window adeclick uses when none is given (ms).
const defaultWindow = 55;

/**
 * repairControls makes the controls for the repair in obj (the sound being edited); changed is called after
 * every change, row(label, input) lays a control out as its form does.
 * @param {Record<string, any>} obj
 * @param {() => void} changed
 * @param {(label: string, input: HTMLElement) => HTMLElement} row
 */
export function repairControls(obj, changed, row) {
  const lowCuts = [[0, t('Off')], [80, t('Rumble (80 Hz)')], [120, t('Strong rumble (120 Hz)')]];
  const clicks = [[0, t('Off')], [3, t('clicks|Light')], [2, t('Normal')], [1, t('Strong')]];

  // A choice of presets; one that the exact values below do not match shows as Custom.
  const presets = (name, list, key, label) => {
    const custom = h('option', { value: 'custom', hidden: true }, t('Custom'));
    const el = h('select', { name, 'aria-label': label, onchange: () => {
      if (el.value === 'custom') return;
      obj[key] = Number(el.value);
      show(true);
      changed();
    } }, list.map(([value, text]) => h('option', { value: String(value) }, text)), custom);
    const update = () => {
      const known = list.some(([value]) => value === (obj[key] || 0));
      custom.hidden = known;
      el.value = known ? String(obj[key] || 0) : 'custom';
    };
    return { el, update };
  };
  const lowCut = presets('lowCutChoice', lowCuts, 'lowCut', t('Low cut'));
  const declick = presets('declickChoice', clicks, 'declick', t('Remove clicks'));

  // The exact values: empty is off (the low cut, the clicks) or adeclick's own window.
  const number = (key, label, step, min, max, placeholder = '') => h('input', {
    type: 'number', name: key, step, min, max, placeholder, 'aria-label': label,
    oninput: (e) => {
      obj[key] = e.target.value === '' ? 0 : Number(e.target.value);
      show(false);
      changed();
    },
  });
  const hz = number('lowCut', t('Low cut (Hz)'), 5, 20, 300, t('off'));
  const steep = h('input', { type: 'checkbox', name: 'lowCutSteep', onchange: (e) => { obj.lowCutSteep = e.target.checked; changed(); } });
  const threshold = number('declick', t('Click threshold'), 0.5, 1, 20, t('off'));
  const width = number('declickWindow', t('Click window (ms)'), 5, 10, 100, String(defaultWindow));

  // show brings the controls in line with obj; inputs too when a choice set the values.
  function show(inputs) {
    lowCut.update();
    declick.update();
    if (inputs) {
      hz.value = obj.lowCut || '';
      threshold.value = obj.declick || '';
      width.value = obj.declickWindow || '';
    }
    steep.checked = !!obj.lowCutSteep;
    steep.disabled = !obj.lowCut;
    width.disabled = !obj.declick;
  }
  show(true);

  return {
    lowCut: row(t('Low cut'), lowCut.el),
    declick: row(t('Remove clicks'), declick.el),
    advanced: h('details', { class: 'advanced' }, h('summary', {}, t('Advanced')),
      row(t('Low cut (Hz)'), hz),
      row(t('24 dB per octave'), steep),
      row(t('Click threshold'), threshold),
      row(t('Click window (ms)'), width),
      h('p', { class: 'muted hint' }, t('The low cut takes out what is below that frequency; 24 dB per octave cuts more sharply. A lower click threshold finds more clicks, but also takes some speech for clicks; a longer window takes out longer crackle.'))),
  };
}
