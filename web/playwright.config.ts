import { existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { defineConfig, devices } from '@playwright/test'
// Each test starts its own bin/vault (see e2e/vault.ts); run `make build` first.
const browsers = [
  // Headless Chromium on a Mac fails randomly with AUDIO_RENDERER_ERROR when it
  // opens the real audio device; a fake output keeps playback tests about playback.
  { name: 'chromium', use: { ...devices['Desktop Chrome'], launchOptions: { args: ['--disable-audio-output'] } } },
  { name: 'webkit', use: devices['Desktop Safari'] },
]

// A build's own preparation before any test (e2e/global-setup.full.ts), when
// the tree has one.
const buildSetup = fileURLToPath(new URL('./e2e/global-setup.full.ts', import.meta.url))

// Screenshots that judge the look (docs/screenshots, the tab bar's) run in
// their own project on new-headless Chrome: Playwright's default headless
// shell (and its WebKit) drop backdrop-filter on a fixed bar once the page
// scrolls, so glass shows crisp text through it that no real browser does.
const VISUAL = [/screenshots[^/]*\.spec\.ts$/]

export default defineConfig({
  testDir: './e2e',
  ...(existsSync(buildSetup) ? { globalSetup: buildSetup } : {}),
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
  projects: [
    ...browsers.map((b) => ({
      name: b.name,
      testIgnore: VISUAL,
      use: { ...b.use, viewport: { width: 1440, height: 900 } },
    })),
    {
      name: 'visual',
      testMatch: [...VISUAL, /tabbar\.spec\.ts$/],
      use: { ...browsers[0]!.use, channel: 'chromium', viewport: { width: 1440, height: 900 } },
    },
  ],
})
