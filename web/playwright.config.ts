import { defineConfig, devices } from '@playwright/test'
// Each test starts its own bin/vault (see e2e/vault.ts); run `make build` first.
const browsers = [
  // Headless Chromium on a Mac fails randomly with AUDIO_RENDERER_ERROR when it
  // opens the real audio device; a fake output keeps playback tests about playback.
  { name: 'chromium', use: { ...devices['Desktop Chrome'], launchOptions: { args: ['--disable-audio-output'] } } },
  { name: 'webkit', use: devices['Desktop Safari'] },
]

export default defineConfig({
  testDir: './e2e',
  // Tests are isolated (own server each), so they may run in parallel.
  fullyParallel: true,
  workers: 2,
  retries: 0,
  reporter: [['list']],
  timeout: 30_000,
  expect: { timeout: 7_000 },
  // The service worker (offline downloads) stays out of the other specs: page.route
  // mocks and fresh loads must see the network. e2e/offline*.spec.ts allow it.
  use: { trace: 'retain-on-failure', locale: 'ru-RU', colorScheme: 'dark', serviceWorkers: 'block' },
  projects: browsers.map((b) => ({
    name: b.name,
    use: { ...b.use, viewport: { width: 1440, height: 900 } },
  })),
})
