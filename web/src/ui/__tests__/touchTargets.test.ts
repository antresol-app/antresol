// The 44 pt finger targets must never change a desktop: every use of
// --touch-target in a module sits inside `@media (pointer: coarse)` (or a
// phone-width block), apart from the few listed with their reason.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const SRC = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')

/** Files allowed to use it everywhere, and why. */
const ALLOWED: Record<string, string> = {
  // the episode tile's download button had a 44 pt ::before for every pointer before the round
  'components/EpisodeList.module.css': 'pre-existing extension for all pointers',
}

function cssFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((n) => {
    const p = path.join(dir, n)
    if (statSync(p).isDirectory()) return n === 'node_modules' ? [] : cssFiles(p)
    return n.endsWith('.module.css') ? [p] : []
  })
}

/** [start, end) of every @media block whose query is touch-only or phone-width. */
function touchBlocks(css: string): [number, number][] {
  const out: [number, number][] = []
  const re = /@media([^{]*)\{/g
  for (let m = re.exec(css); m; m = re.exec(css)) {
    const q = m[1]!
    const touch = /pointer:\s*coarse/.test(q) || /max-width:\s*(\d+)px/.test(q) && Number(/max-width:\s*(\d+)px/.exec(q)![1]) <= 699
    let depth = 1
    let i = re.lastIndex
    while (depth > 0 && i < css.length) {
      if (css[i] === '{') depth++
      else if (css[i] === '}') depth--
      i++
    }
    if (touch) out.push([m.index, i])
  }
  return out
}

describe('the 44 pt finger targets', () => {
  it('stay inside touch-only (or phone-width) blocks, so a desktop never changes', () => {
    const offenders: string[] = []
    for (const f of cssFiles(SRC)) {
      const rel = path.relative(SRC, f)
      if (ALLOWED[rel]) continue
      const css = readFileSync(f, 'utf8')
      const blocks = touchBlocks(css)
      for (let i = css.indexOf('var(--touch-target)'); i >= 0; i = css.indexOf('var(--touch-target)', i + 1)) {
        if (!blocks.some(([a, b]) => i > a && i < b)) offenders.push(`${rel}:${css.slice(0, i).split('\n').length}`)
      }
    }
    expect(offenders).toEqual([])
  })
})
