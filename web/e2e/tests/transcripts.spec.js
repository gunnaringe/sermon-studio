// Transcripts: the church's default in Settings → Processing (a language and a model from the models folder), a
// sermon's transcript in its details: the text, its downloads, the notice when another model is there now, Make
// again; and in the editor, Transcript on or off, the language and its model (another chosen in a dialog,
// filtered by size and name), made by Save, and the bar of one being made.
// The model is not a real one (a transcript is not made here: that is whisper's work); the transcript is written
// as the studio writes it, and Make again shows how a failed one looks.
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { editPage } from './pages.js';

const DATA = process.env.DATA;
const shots = path.join(import.meta.dirname, '..', 'screenshots');
fs.mkdirSync(shots, { recursive: true });
const models = path.join(DATA, 'churches', '.models');
const date = '2026-05-17';
const folder = '2026/' + date;
const dir = path.join(DATA, 'root', folder);
const q = '?path=' + encodeURIComponent(folder);

test.beforeAll(async ({ request }) => {
  fs.mkdirSync(dir, { recursive: true });
  fs.copyFileSync(path.join(DATA, 'upload.flac'), path.join(dir, date + '_raw.flac'));
  const settings = { sections: [{ start: 3, end: 25 }], cuts: [], fadeIn: 0, fadeOut: 0 };
  expect((await request.put('api/sermon' + q, { data: settings })).ok()).toBe(true);
  expect((await request.post('api/sermon/save' + q)).ok()).toBe(true);
  const run = await request.post('api/run' + q, {
    data: { ...settings, flac: false, mp3: false, opus: true, stereo: false, noiseReduction: 0, targetLufs: -16, truePeak: -1.5, mp3Bitrate: '128k', opusBitrate: '64k', force: false },
  });
  expect(run.ok(), await run.text()).toBe(true);
  const { id } = await run.json();
  await expect.poll(async () => (await (await request.get('api/jobs/' + id)).json()).state, { timeout: 90_000 }).toBe('done');

  // The models folder, as `sermon-studio models pull` leaves it.
  fs.mkdirSync(models, { recursive: true });
  fs.writeFileSync(path.join(models, 'nb-whisper-medium.bin'), 'not a model');
  fs.writeFileSync(path.join(models, 'turbo.bin'), 'not a model either');
  const model = (sha, day) => ({
    name: 'nb-whisper-medium', file: 'nb-whisper-medium.bin', languages: ['nb', 'nn'], repo: 'NbAiLab/nb-whisper-medium',
    path: 'ggml-model-q5_0.bin', revision: 'main', sha256: sha, date: day,
  });
  const turbo = { name: 'whisper.cpp ggml-large-v3-turbo-q8_0', file: 'turbo.bin', languages: ['*'], repo: 'ggerganov/whisper.cpp',
    path: 'ggml-large-v3-turbo-q8_0.bin', revision: 'main', sha256: 'c'.repeat(64), size: 18, date: '2024-10-29T17:49:50Z' };
  fs.writeFileSync(path.join(models, 'models.json'), JSON.stringify({ models: [model('b'.repeat(64), '2025-03-02T10:00:00Z'), turbo] }));
  // A transcript made with the model's earlier version.
  const segments = [
    { start: 3.2, end: 7.9, text: 'Grace to you and peace from God our Father.' },
    { start: 8.1, end: 12.5, text: 'Today we read from the gospel of Luke, the second chapter.' },
    { start: 16, end: 21.4, text: 'And there were shepherds in the fields, keeping watch over their flock by night.' },
  ];
  fs.writeFileSync(path.join(dir, date + '.transcript.json'), JSON.stringify({
    language: 'nb', model: model('a'.repeat(64), '2024-02-13T09:00:00Z'), made: '2026-09-13T12:00:00Z', segments,
  }));
  fs.writeFileSync(path.join(dir, date + '.txt'), segments[0].text + ' ' + segments[1].text + '\n\n' + segments[2].text + '\n');
  fs.writeFileSync(path.join(dir, date + '.vtt'), 'WEBVTT\n\n00:00:03.200 --> 00:00:07.900\n' + segments[0].text + '\n');
});

test.afterAll(async ({ request }) => {
  await request.put('api/transcription', { data: { language: '' } });
  fs.rmSync(models, { recursive: true, force: true });
});

const NB = 'NbAiLab/nb-whisper-medium/ggml-model-q5_0.bin', TURBO = 'ggerganov/whisper.cpp/ggml-large-v3-turbo-q8_0.bin';

test('transcripts: the default in the settings, a sermon\'s transcript, made with an earlier model', async ({ page }) => {
  await page.goto('c/first-church/settings/transcripts');
  const box = page.locator('section#transcripts');
  const select = box.locator('select[name=transcript-language]');
  await expect(select).toHaveValue('');
  await expect(select.locator('option')).toHaveText(['No transcripts', 'Norwegian Bokmål', 'Norwegian Nynorsk', 'Detect automatically']);
  await select.selectOption('nb');
  await expect(box.getByRole('status')).toHaveText('Saved.');
  // The model for its language, on one line: the first for it, until the church chooses another in a dialog.
  const line = box.locator('.transcript-defaults > .model-line');
  await expect(line.locator('.model-current')).toHaveText('Model: nb-whisper-medium from 2025-03-02.');
  await line.getByRole('button', { name: 'Change…' }).click();
  const dialog = page.getByRole('dialog', { name: 'Model for Norwegian Bokmål' });
  await expect(dialog.locator('.model-list .model')).toHaveCount(2);
  await dialog.locator('.model-list .model', { hasText: 'turbo' }).click();
  await dialog.screenshot({ path: path.join(shots, 'transcript-model-dialog.png') });
  await dialog.getByRole('button', { name: 'Use this model' }).click();
  await expect(box.getByRole('status')).toHaveText('Saved.');
  await expect(line).toContainText('Model: whisper.cpp ggml-large-v3-turbo-q8_0 from 2024-10-29.changed');
  await expect.poll(async () => (await (await page.request.get('api/transcription')).json()).defaults).toEqual({ nb: TURBO });
  // The other languages: folded away, a line each.
  const others = box.locator('details.model-others');
  await expect(others.locator('summary')).toHaveText('Models for other languages (2)');
  await others.locator('summary').click();
  await expect(others.locator('th')).toHaveText(['Norwegian Nynorsk', 'Detect automatically']);
  await expect(others.locator('tr[data-language=nn] .model-current')).toHaveText('Model: nb-whisper-medium from 2025-03-02.');
  await box.screenshot({ path: path.join(shots, 'transcript-settings.png') });

  await page.goto('c/first-church/');
  const row = page.locator('.year tr.row', { hasText: date });
  await row.click();
  const panel = page.locator('tr.detail-row .transcript');
  // Made with the earlier version: said, with Make again, and not made again by itself.
  await expect(panel.locator('summary')).toContainText('Made with nb-whisper-medium from 2024-02-13.');
  await expect(panel.locator('button[name=make-transcript]')).toHaveText('Make again');
  await panel.locator('summary').click();
  await expect(panel.locator('.transcript-text p')).toHaveText([
    'Grace to you and peace from God our Father. Today we read from the gospel of Luke, the second chapter.',
    'And there were shepherds in the fields, keeping watch over their flock by night.',
  ]);
  await expect(panel.getByRole('link', { name: 'Download captions (WebVTT)' })).toHaveAttribute('href', /download=1.*\.vtt/);
  await page.screenshot({ path: path.join(shots, 'transcript-panel.png') });
  const vtt = await page.request.get('api/file?path=' + encodeURIComponent(folder + '/' + date + '.vtt'));
  expect(vtt.headers()['content-type']).toContain('text/vtt');

  // Make again, with a model that is not one: it fails, says so and keeps the transcript there.
  await panel.locator('button[name=make-transcript]').first().click();
  await expect(panel.locator('.error')).toContainText('The transcript was not made', { timeout: 30_000 });
  await expect(panel.locator('button[name=make-transcript]')).toHaveText('Try again');
  expect(fs.existsSync(path.join(dir, date + '.transcript.json'))).toBe(true);
});

test('transcripts: Transcript in the editor, the choice of the model, made by Save, and the bar of one being made', async ({ page }) => {
  await page.goto(editPage(folder));
  await expect(page.locator('#waveform')).toBeVisible({ timeout: 60_000 }); // the saved cut is on the page
  const box = page.locator('section[aria-labelledby=step-transcript]');
  const models = page.getByRole('dialog', { name: /^Model for/ }).locator('.model-list .model');
  const current = box.locator('.model-current');
  const on = box.locator('button[name=transcript-on]');
  const language = box.locator('select[name=transcript-language]');
  // The recording has one (the test before failed to make it again): on, in its language and with its model,
  // which is not the church's for the language (the default).
  await expect(on).toHaveAttribute('aria-pressed', 'true');
  await expect(language).toHaveValue('nb');
  await expect(current).toHaveText('Model: nb-whisper-medium from 2025-03-02.');
  // Change…: every model for the language in a dialog, filtered by size and name; the default marked.
  const dialog = page.getByRole('dialog', { name: /^Model for/ });
  const change = box.getByRole('button', { name: 'Change…' });
  await change.click();
  await expect(models).toHaveCount(2);
  await expect(models.nth(1)).toContainText('whisper.cpp ggml-large-v3-turbo-q8_0default');
  await expect(models.nth(1)).toContainText('large-v3-turbo · Detect automatically');
  await dialog.locator('select[name=model-size]').selectOption('large-v3-turbo');
  await expect(models).toHaveCount(1);
  await dialog.locator('select[name=model-size]').selectOption('');
  await dialog.locator('input[name=model-name]').fill('NB-');
  await expect(models).toHaveCount(1);
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(current).toHaveText('Model: nb-whisper-medium from 2025-03-02.');
  // Another language: its default model (Detect automatically: only a model that finds the language itself).
  await language.selectOption('*');
  await expect(current).toHaveText('Model: whisper.cpp ggml-large-v3-turbo-q8_0 from 2024-10-29.');
  await language.selectOption('nb');
  await expect(box.locator('.model-line')).toContainText('Model: whisper.cpp ggml-large-v3-turbo-q8_0 from 2024-10-29.default');
  await change.click();
  await models.first().click();
  await dialog.getByRole('button', { name: 'Use this model' }).click();
  await expect(current).toHaveText('Model: nb-whisper-medium from 2025-03-02.');
  // Off: Save leaves it out. On, not made: Save makes it, after the files when it makes them.
  await on.click();
  await expect(on).toHaveAttribute('aria-pressed', 'false');
  await expect(box.locator('.transcript-choice')).toBeHidden();
  await on.click();
  await expect(box.locator('.pill .state')).toHaveText('will be made');
  await expect(page.locator('.save-what')).toContainText(/Save makes: (Opus, )?transcript\./, { timeout: 30_000 });
  await box.screenshot({ path: path.join(shots, 'transcript-picker.png') });

  // Being made: a bar with ✕ (the work is not real here: the folder says so).
  const running = async (route) => {
    const res = await route.fetch();
    const info = await res.json();
    info.transcript = { ...info.transcript, state: 'running', progress: 0.42, remaining: 200 };
    await route.fulfill({ response: res, json: info });
  };
  await page.route(/api\/(folder|transcript)\?/, running);
  const asked = page.waitForRequest((r) => r.method() === 'POST' && /api\/(run|transcript)\?/.test(r.url()));
  await page.locator('.save-bar button.primary').click();
  const req = await asked;
  if (req.url().includes('api/run?')) expect(req.postDataJSON().transcript).toEqual({ language: 'nb', model: NB });
  else expect(req.url()).toContain('language=nb&model=' + encodeURIComponent(NB));
  const bar = box.locator('.transcript-progress');
  // How far it is shows on hover; the time left and that it runs in the background are said.
  await expect(bar).toContainText('Transcribing… · about 3 minutes left', { timeout: 60_000 }); // after the run, when there is one
  await expect(bar).not.toContainText('%');
  await expect(bar.locator('progress')).toHaveJSProperty('value', 0.42);
  await expect(bar.locator('progress')).toHaveAttribute('title', '42% done');
  await expect(bar.locator('.live-bar.running')).toBeVisible();
  await expect(bar.locator('.background-note')).toContainText('in the background');
  await expect(bar.locator('button[name=stop-transcript]')).toHaveText('✕');
  await expect(box.locator('.pill .state')).toHaveText('being made');
  await box.screenshot({ path: path.join(shots, 'transcript-progress.png') });
  await page.unroute(/api\/(folder|transcript)\?/);
  await bar.locator('button[name=stop-transcript]').click();
  await expect(bar).toHaveCount(0, { timeout: 30_000 });
});
