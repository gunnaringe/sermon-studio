import { defineConfig } from '@playwright/test';

// The pictures of the documentation: see ../guide.sh.
export default defineConfig({
  testDir: '.',
  timeout: 600_000,
  workers: 1,
  reporter: [['list']],
  use: {
    baseURL: 'http://localhost:8080/',
    viewport: { width: 1280, height: 800 },
    launchOptions: { args: ['--autoplay-policy=no-user-gesture-required'] },
    trace: 'retain-on-failure',
    // The editor's folding steps start open here, so the tests reach what is in them (web.spec.js tests the folding).
    storageState: { cookies: [], origins: [{ origin: 'http://localhost:8080', localStorage: [{ name: 'editorOpen', value: '["step-edit","step-metadata","step-processing","step-clips","step-delete"]' }] }] },
  },
});
