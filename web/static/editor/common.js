// What the parts of the editor share: the keys of its choices, and the editor itself (Editor), which each
// part is given. A part reads and sets the edit through it, and calls the other parts' functions on it.

// SOUND_KEYS are the processing choices that are kept with a sermon.
export const SOUND_KEYS = ['stereo', 'noiseReduction', 'roomSound', 'leveling', 'compression', 'targetLufs', 'truePeak', 'mp3Bitrate', 'opusBitrate'];

// DEFAULT_KEYS are what the church's default processing sets: the sound and the fades.
export const DEFAULT_KEYS = [...SOUND_KEYS, 'fadeIn', 'fadeOut'];

// CLIP_KEYS are the clips around the sermon.
export const CLIP_KEYS = ['intro', 'outro'];

// FORMAT_KEYS are the formats a sermon can be made into.
export const FORMAT_KEYS = ['flac', 'mp3', 'opus'];

// css is the value of a CSS variable of the page (the colours of the waveform).
export const css = (name) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();

// spanOf is a section or cut as it is kept: its times and its label and pause, if any.
export const spanOf = (x) => ({ start: x.region.start, end: x.region.end, ...(x.label ? { label: x.label } : {}), ...(x.pause > 0 ? { pause: x.pause } : {}) });

/**
 * The editor of one recording, as its parts share it.
 * @typedef {object} Editor
 * @property {HTMLElement} view the page
 * @property {string} path the recording's folder
 * @property {any} info the folder (api/folder): the recording, its outputs, tags, the kept sermon
 * @property {any} config the studio's config (api/config): the church's defaults
 * @property {Record<string, any>} opts the choices: the processing, the fades, the formats, the clips
 * @property {Record<string, string>} tags the tags written into the files
 * @property {(() => void)[]} cleanups what is undone when the page is left
 * @property {boolean} alive the page is still shown
 * @property {import('../vendor/wavesurfer/wavesurfer.esm.js').default} ws the waveform, once it is there
 * @property {Span[]} sections the parts of the recording that are the sermon, in time order
 * @property {Span[]} cuts the parts cut out of the sections, in time order
 * @property {{ saved: any, adjusted: any, savedAt?: string }} sermon the kept versions of the edit (sermon.json)
 * @property {string} lastSent the edit as it was last kept, as JSON
 * @property {boolean} restoring settings are being put on the page: that is not a change
 * @property {boolean} proposed the one section is the start the page made, untouched
 *
 * The parts' functions, called by the others:
 * @property {() => void} changed the edit changed: check it, keep it, make it a step of undo
 * @property {() => void} adopt make the start the page made the user's own
 * @property {(start: number, end: number, label?: string, pause?: number) => Span} addSection
 * @property {(start: number, end: number, label?: string, pause?: number) => Span} addCut
 * @property {() => void} renderSections
 * @property {() => void} renderCuts
 * @property {(time: number) => void} reveal scroll a zoomed waveform so the time is in view
 * @property {(key: string, seconds: number) => void} setFade
 * @property {() => number} keptLength the sections without the cuts
 * @property {() => { at: number, seconds: number }[]} activePauses
 * @property {() => any} currentSettings the edit on the page as it is kept, or null
 * @property {() => Promise<void>} persistAdjusted keep the edit on the page as the adjustments
 * @property {() => void} saveSoon persistAdjusted, after a moment without changes
 * @property {(set: any, o?: { show?: boolean }) => void} applySettings put a kept edit on the page
 * @property {() => void} record make the edit as it is now a step of undo, if it changed
 * @property {() => void} recordSoon record, after a moment without changes
 * @property {() => void} undo
 * @property {() => void} redo
 * @property {() => void} startHistory
 * @property {() => { undo: number, redo: number }} history
 * @property {() => void} renderVersion
 * @property {(text: string) => void} setSaveNote
 * @property {(force?: boolean) => Record<string, any>} request the run as the server takes it
 * @property {() => void} check ask the server what the edit would make
 * @property {() => void} renderOutputs
 * @property {() => Promise<void>} refreshInfo read the folder again
 * @property {() => void} renderDelete
 * @property {() => void} showChannelWarning
 * @property {(stereo: boolean) => void} setStereo
 * @property {() => void} redrawProcessing show the processing choices again, after they were set from outside
 * @property {() => void} showDefaults
 * @property {() => Record<string, string>} clipChoices
 * @property {() => void} drawClips
 * @property {() => void} showClipLength
 */

/**
 * A section or cut: its region on the waveform, its label and pause, and its row in the list.
 * @typedef {{ region: any, label?: string, pause?: number, row?: HTMLElement, startIn?: HTMLInputElement, endIn?: HTMLInputElement, info?: HTMLElement, warn?: HTMLElement }} Span
 */
