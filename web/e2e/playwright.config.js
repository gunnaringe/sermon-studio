import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  timeout: 120_000,
  workers: 1, // one server, one encode at a time
  reporter: [['list']],
  use: {
    baseURL: process.env.BASE_URL || 'http://localhost:8080/',
    viewport: { width: 1280, height: 900 },
    launchOptions: { args: ['--autoplay-policy=no-user-gesture-required'] },
    trace: 'retain-on-failure',
    // The editor's folding steps start open here, so the tests reach what is in them (web.spec.js tests the folding).
    storageState: { cookies: [], origins: [{ origin: 'http://localhost:8080', localStorage: [{ name: 'editorOpen', value: '["step-edit","step-metadata","step-processing","step-clips","step-delete"]' }] }, { origin: 'http://localhost:8082', localStorage: [{ name: 'editorOpen', value: '["step-edit","step-metadata","step-processing","step-clips","step-delete"]' }] }] },
  },
});
