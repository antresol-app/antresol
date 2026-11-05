// Renders the PWA PNG icons from icon.svg (docs/identity/make-icon.py). Run: node scripts/render-icons.mjs
import { chromium } from '@playwright/test'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import fs from 'node:fs'

const out = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'public', 'icons')
const art = fs.readFileSync(path.join(out, 'icon.svg'), 'utf8')
const inner = art.slice(art.indexOf('/>', art.indexOf('<rect')) + 2, art.lastIndexOf('</svg>'))
const defs = art.slice(art.indexOf('<defs>'), art.indexOf('</defs>') + 7)
const mark = (size, { full = false, maskable = false } = {}) => {
  // full: square tile with no corner radius (iOS masks it itself); maskable: mark inside the 80% safe zone.
  const pad = maskable ? size * 0.1 : 0
  const s = (size - pad * 2) / 1024
  return `<html><body style="margin:0;background:transparent">
<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 ${size} ${size}">
${defs}<rect width="${size}" height="${size}" rx="${full || maskable ? 0 : size * 0.2227}" fill="url(#lm-bg)"/>
<g transform="translate(${pad} ${pad}) scale(${s})">${inner}</g>
</svg></body></html>`
}
const jobs = [
  ['icon-192.png', 192, {}],
  ['icon-512.png', 512, {}],
  ['icon-maskable-512.png', 512, { maskable: true }],
  ['apple-touch-icon.png', 180, { full: true }],
]
const browser = await chromium.launch()
const page = await browser.newPage()
for (const [name, size, opt] of jobs) {
  await page.setViewportSize({ width: size, height: size })
  await page.setContent(mark(size, opt))
  await page.locator('svg').screenshot({ path: path.join(out, name), omitBackground: true })
}
await browser.close()
console.log('icons written to', out)
