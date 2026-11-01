// Transcripts: the church's language in Settings → Processing (from the models in the models folder), and a sermon's
// transcript in its details: the text, its downloads, the notice when another model is there now, Make again.
// The model is not a real one (a transcript is not made here: that is whisper's work); the transcript is written
// as the studio writes it, and Make again shows how a failed one looks.
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

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
  const model = (sha, day) => ({
    name: 'nb-whisper-medium', file: 'nb-whisper-medium.bin', languages: ['nb', 'nn'], repo: 'NbAiLab/nb-whisper-medium',
    path: 'ggml-model-q5_0.bin', revision: 'main', sha256: sha, date: day,
  });
  fs.writeFileSync(path.join(models, 'models.json'), JSON.stringify({ models: [model('b'.repeat(64), '2025-03-02T10:00:00Z')] }));
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

test('transcripts: the language in the settings, a sermon\'s transcript, made with an earlier model', async ({ page }) => {
  await page.goto('c/first-church/settings/transcripts');
  const box = page.locator('section#transcripts');
  const select = box.locator('select[name=transcript-language]');
  await expect(select).toHaveValue('');
  await expect(select.locator('option')).toHaveText(['No transcripts', 'Norwegian Bokmål', 'Norwegian Nynorsk']);
  await select.selectOption('nb');
  await expect(box.getByRole('status')).toHaveText('Saved.');
  await expect(box).toContainText('Model: nb-whisper-medium from 2025-03-02.');
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
