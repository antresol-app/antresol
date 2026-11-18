// The default scrollbar look (design/tokens.css) is in a CSS layer, so a
// component that hides or changes its own scrollbar always wins. Unlayered,
// the universal `*` rule ties a zero-specificity component rule
// (Rail's :where(.list)) and the stylesheet linked last wins: in a
// production build that is the main one, and every shelf got its scrollbar
// back while the dev server (the other order) looked right.
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const TOKENS = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../tokens.css')

/** The text of the css with every `@layer name { … }` block cut out, and those blocks' contents. */
function splitLayers(css: string): { outside: string; layers: string[] } {
  const layers: string[] = []
  let outside = ''
  let i = 0
  const open = /@layer\s+[\w.-]+\s*\{/g
  for (let m = open.exec(css); m; m = open.exec(css)) {
    outside += css.slice(i, m.index)
    let depth = 1
    let j = open.lastIndex
    while (j < css.length && depth > 0) depth += css[j] === '{' ? 1 : css[j] === '}' ? -1 : 0, j++
    layers.push(css.slice(open.lastIndex, j - 1))
    i = j
    open.lastIndex = j
  }
  return { outside: outside + css.slice(i), layers }
}

describe('scrollbar defaults', () => {
  it('sit in a @layer, never as plain rules', () => {
    const css = readFileSync(TOKENS, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')
    const { outside, layers } = splitLayers(css)
    expect(outside).not.toMatch(/scrollbar-(width|color)|::-webkit-scrollbar/)
    expect(layers.join('\n')).toMatch(/scrollbar-width\s*:\s*thin/)
  })
})
