// iPhone Safari (WebKit, mobile): the first play() comes after an await and
// is refused; WebKit then fires «pause». A tap on «Смотреть» long after that
// must play the same session — a reload would call play() after an await
// again, iOS would refuse it again, and the button would seem to do nothing.
import { devices } from '@playwright/test'
import { PLAYBACK } from './media'
import { expect, setupAdmin, test } from './vault'

test.use({ library: 'playback', ...devices['iPhone 13'] })
test.describe.configure({ timeout: 90_000 })

test('iPhone: «Смотреть» after a refused autoplay plays without a reload', async ({ page, browserName }) => {
  test.skip(browserName !== 'webkit', 'iOS autoplay policy is WebKit-only')
  await setupAdmin(page.request)
  let id = 0
  await expect
    .poll(
      async () => {
        const r = (await (await page.request.get('/api/items?kind=movie&limit=50')).json()) as { items: { id: number; title: string }[] }
        id = r.items.find((c) => c.title === PLAYBACK.hls.title)?.id ?? 0
        return id
      },
      { timeout: 20_000 },
    )
    .toBeGreaterThan(0)
  // iOS's policy, which desktop WebKit does not apply to every file: a
  // play() without a tap behind it is refused, with «play» then «pause».
  await page.addInitScript(() => {
    // a tap: from the capturing listener to the bubbling one on window
    let tap = false
    window.addEventListener('click', () => void (tap = true), true)
    window.addEventListener('click', () => void (tap = false))
    const play = HTMLMediaElement.prototype.play
    HTMLMediaElement.prototype.play = function (this: HTMLMediaElement) {
      if (tap) return play.call(this)
      this.dispatchEvent(new Event('play'))
      setTimeout(() => this.dispatchEvent(new Event('pause')), 50)
      return Promise.reject(new DOMException('refused', 'NotAllowedError'))
    }
  })
  await page.clock.install()
  const plays: string[] = []
  page.on('request', (r) => {
    if (r.method() === 'POST' && /\/api\/play\/\d+$/.test(r.url())) plays.push(r.url())
  })
  await page.goto(`/watch/${id}`)
  const tap = page.getByRole('button', { name: 'Смотреть', exact: true }).last()
  await expect(page.getByTestId('player')).toHaveAttribute('data-mode', /^(remux|transcode)$/, { timeout: 20_000 })
  await expect(tap).toBeVisible()
  // the viewer comes back to the phone a few minutes later
  await page.clock.fastForward('03:00')
  await tap.click()
  await expect
    .poll(() => page.getByTestId('player').locator('video').evaluate((v: HTMLVideoElement) => !v.paused && v.currentTime > 0.5), { timeout: 20_000 })
    .toBe(true)
  expect(plays).toHaveLength(1)
})
