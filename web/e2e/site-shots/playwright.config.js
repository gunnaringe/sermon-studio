import { defineConfig } from '@playwright/test';

// The website's screenshots and video: see run.sh.
export default defineConfig({
  testDir: '.',
  outputDir: './test-results', // its own, so it can run beside the other browser tests
  timeout: 900_000,
  workers: 1,
  reporter: [['list']],
  use: {
    baseURL: 'http://localhost:8080/',
    viewport: { width: 1280, height: 800 },
    // The video (RECORD, set by run.sh) is recorded from a real window on the screen run.sh makes, so its sound is too.
    headless: !process.env.RECORD,
    launchOptions: { args: ['--autoplay-policy=no-user-gesture-required', '--window-position=0,0', '--hide-scrollbars'] },
    trace: 'retain-on-failure',
    // The editor's folding steps start open here, so the tests reach what is in them (web.spec.js tests the folding).
    storageState: { cookies: [], origins: [{ origin: 'http://localhost:8080', localStorage: [{ name: 'editorOpen', value: '["step-edit","step-metadata","step-processing","step-clips","step-delete"]' }] }] },
  },
});
