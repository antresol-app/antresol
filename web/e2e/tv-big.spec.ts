// «ТВ» with a big IPTV playlist (3000 channels in 40 groups, a guide for
// each) on a real bin/vault: the page shows its first rows fast, the
// categories are a level of their own (a list of them, then the channels of
// one, with a way back), the rows are one shape (a logo or the channel's
// initials, the programme's picture only when there is one), and it all
// stays inside a phone's width. TV_BIG_REPORT=1 prints the numbers (the
// list's bytes and server time, the time to the first rows, the main
// thread's long tasks over a re-poll).
import { createServer, type Server } from 'node:http'
import { gzipSync } from 'node:zlib'
import type { AddressInfo } from 'node:net'
import { devices, type APIRequestContext, type Page } from '@playwright/test'
import { expect, setupAdmin, test } from './vault'

test.use({ library: 'empty' })
test.setTimeout(240_000)

const N = Number(process.env.TV_BIG_N ?? 3000)
const GROUPS = 40
const report = (...a: unknown[]) => process.env.TV_BIG_REPORT && console.log('[tv-big]', ...a)

function playlist(base: string): string {
  let s = '#EXTM3U\n'
  for (let i = 1; i <= N; i++) {
    // every fifth channel has no logo (the initials stand in)
    const logo = i % 5 === 0 ? '' : ` tvg-logo="${base}/logo/${i}.svg"`
    s += `#EXTINF:-1 tvg-id="ch${i}"${logo} group-title="Группа ${(i % GROUPS) + 1}",Канал номер ${i}\nhttp://127.0.0.1:9/live/${i}.m3u8\n`
  }
  return s
}

function guide(base: string): string {
  const f = (t: number) => new Date(t).toISOString().replace(/[-:T]/g, '').slice(0, 14) + ' +0000'
  const desc = 'Подробное описание передачи для телегида. '.repeat(8)
  const parts = ['<?xml version="1.0" encoding="UTF-8"?><tv>']
  for (let i = 1; i <= N; i++) parts.push(`<channel id="ch${i}"><display-name>Канал номер ${i}</display-name></channel>`)
  const start = Math.floor((Date.now() - 12 * 3600_000) / 1800_000) * 1800_000
  for (let i = 1; i <= N; i++) {
    let t = start
    for (let k = 0; t < Date.now() + 24 * 3600_000; k++) {
      const d = (30 + ((i * 7 + k * 13) % 31)) * 60_000
      // half the channels' programmes carry a picture
      const icon = i % 2 ? `<icon src="${base}/art/${k % 7}.svg"/>` : ''
      parts.push(`<programme start="${f(t)}" stop="${f(t + d)}" channel="ch${i}"><title>Передача ${k}</title><sub-title>Выпуск ${k}</sub-title><desc>${desc}</desc>${icon}</programme>`)
      t += d
    }
  }
  parts.push('</tv>')
  return parts.join('')
}

const svg = (fill: string) => `<svg xmlns="http://www.w3.org/2000/svg" width="160" height="90"><rect width="160" height="90" fill="${fill}"/></svg>`

let provider: Server
let base = ''
test.beforeAll(async () => {
  let m3u = ''
  let xml = ''
  provider = createServer((req, res) => {
    const u = req.url ?? ''
    if (u.startsWith('/get.m3u')) return res.end(m3u)
    if (u.startsWith('/epg.xml')) return res.end(xml)
    if (u.startsWith('/logo/') || u.startsWith('/art/')) {
      res.setHeader('Content-Type', 'image/svg+xml')
      return res.end(svg(u.startsWith('/art/') ? '#345' : '#ddd'))
    }
    res.statusCode = 404
    res.end()
  })
  await new Promise<void>((r) => provider.listen(0, '127.0.0.1', r))
  base = `http://127.0.0.1:${(provider.address() as AddressInfo).port}`
  m3u = playlist(base)
  xml = guide(base)
})
test.afterAll(() => new Promise<void>((r) => provider.close(() => r())))

async function addSource(request: APIRequestContext) {
  const r = await request.post('/api/tv/sources', { data: { name: 'Большой', url: `${base}/get.m3u`, defaultShown: true, epg: [{ url: `${base}/epg.xml` }] } })
  expect(r.ok(), await r.text()).toBeTruthy()
  const src = (await r.json()) as { id: number; epg: { id: number }[] }
  await request.post(`/api/tv/sources/${src.id}/refresh`)
  await expect
    .poll(async () => {
      const l = (await (await request.get('/api/channels')).json()) as { channels: { now?: unknown }[] }
      if (l.channels.length >= N && !l.channels[0]?.now) for (const e of src.epg) await request.post(`/api/tv/epg/${e.id}/refresh`)
      return l.channels.length >= N && Boolean(l.channels[0]?.now)
    }, { timeout: 180_000, intervals: [2000] })
    .toBe(true)
}

/** Opens /tv and measures: the list's answer (bytes, server ms), the first rows, the long tasks. */
async function measure(page: Page, path: string, rows: ReturnType<Page['locator']>) {
  await page.addInitScript(() => {
    const w = window as unknown as { __long: number[] }
    w.__long = []
    new PerformanceObserver((l) => l.getEntries().forEach((e) => w.__long.push(e.duration))).observe({ type: 'longtask', buffered: true })
  })
  const list = page.waitForResponse((r) => /\/api\/channels(\?|$)/.test(r.url()) && r.request().method() === 'GET')
  const t0 = Date.now()
  await page.goto(path)
  const resp = await list
  const body = await resp.body()
  const timing = resp.request().timing()
  await expect(rows.first()).toBeVisible({ timeout: 60_000 })
  const first = Date.now() - t0
  const nodes = await page.evaluate(() => document.querySelectorAll('li, [role=listitem]').length)
  return { bytes: body.length, gzip: gzipSync(body).length, server: Math.round(timing.responseStart - timing.requestStart), first, nodes }
}

const longTasks = (page: Page) => page.evaluate(() => {
  const l = (window as unknown as { __long: number[] }).__long
  return { count: l.length, total: Math.round(l.reduce((a, b) => a + b, 0)), max: Math.round(Math.max(0, ...l)) }
})

test('a big playlist: fast first rows, categories as a level, one row shape', async ({ page, browserName, browser, vaultURL }) => {
  test.skip(browserName !== 'chromium', 'one engine measures')
  await setupAdmin(page.request)
  await addSource(page.request)

  // «Все»
  const all = await measure(page, '/tv?f=all', page.getByRole('list', { name: 'Каналы' }).getByRole('link'))
  report('all', all)
  await page.waitForTimeout(32_000) // a re-poll and the clock's ticks
  report('long tasks over 32 s', await longTasks(page))
  if (process.env.TV_BIG_BASELINE) {
    // the code before this round: the numbers and how the top looks
    await page.evaluate(() => window.scrollTo(0, 0))
    await page.screenshot({ path: test.info().outputPath('baseline-desktop.png') })
    await page.setViewportSize({ width: 390, height: 844 })
    await page.waitForTimeout(500)
    await page.screenshot({ path: test.info().outputPath('baseline-phone.png') })
    return
  }
  // a row without a programme picture has no empty box; a channel without a logo shows its initials
  const list = page.getByRole('list', { name: 'Каналы' })
  expect(await list.locator('[data-art-placeholder]').count()).toBe(0)
  expect(await list.locator('[data-channel-initials]').count()).toBeGreaterThan(0)
  expect(await list.locator('[data-channel-logo]').count()).toBeGreaterThan(0)
  await page.screenshot({ path: test.info().outputPath('tv-big-all-desktop.png') })

  // the categories' level, then one of them, and back
  await page.goto('/tv')
  const cats = page.getByRole('navigation', { name: 'Категории' })
  await expect(cats.getByRole('link', { name: /Группа 1\b/ })).toBeVisible()
  await expect(cats.getByRole('link', { name: /Все/ })).toBeVisible()
  await expect(cats.getByRole('link', { name: /Избранное/ })).toBeVisible()
  await page.screenshot({ path: test.info().outputPath('tv-big-categories-desktop.png') })
  await cats.getByRole('link', { name: /Группа 2\b/ }).click()
  await expect(page).toHaveURL(/f=k%3Agroup%3A/)
  const inCat = page.getByRole('list', { name: 'Каналы' }).getByRole('link')
  await expect(inCat.first()).toBeVisible()
  await page.screenshot({ path: test.info().outputPath('tv-big-category-desktop.png') })
  await inCat.first().click()
  await expect(page).toHaveURL(/\/tv\/\d+\?f=k%3Agroup%3A/)
  await page.goBack()
  await expect(page).toHaveURL(/f=k%3Agroup%3A/)
  await page.getByRole('link', { name: 'Категории' }).click()
  await expect(cats).toBeVisible()

  // a phone
  const { defaultBrowserType: _, ...phone } = devices['iPhone 13']
  const ctx = await browser.newContext({ ...phone, viewport: { width: 390, height: 844 }, baseURL: vaultURL, locale: 'ru-RU', colorScheme: 'dark', storageState: await page.context().storageState() })
  const p = await ctx.newPage()
  await p.goto('/tv')
  await expect(p.getByRole('navigation', { name: 'Категории' })).toBeVisible()
  expect(await p.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  await p.screenshot({ path: test.info().outputPath('tv-big-categories-phone.png') })
  await p.getByRole('navigation', { name: 'Категории' }).getByRole('link', { name: /Группа 3\b/ }).click()
  await expect(p.getByRole('list', { name: 'Каналы' }).getByRole('link').first()).toBeVisible()
  expect(await p.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  await p.screenshot({ path: test.info().outputPath('tv-big-category-phone.png') })
  await ctx.close()
})
