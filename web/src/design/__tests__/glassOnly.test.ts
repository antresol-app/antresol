// The glass material lives in one place (design/glass.css): no other
// stylesheet blurs what is behind it. A component that floats picks
// .glass, .glass-strong, .glass-clear or .glass-bar; content is solid.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const SRC = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')

function stylesheets(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = path.join(dir, name)
    if (statSync(p).isDirectory()) stylesheets(p, out)
    else if (name.endsWith('.css')) out.push(p)
  }
  return out
}

describe('glass', () => {
  it('only design/glass.css writes a backdrop-filter (other than none)', () => {
    const found: string[] = []
    for (const file of stylesheets(SRC)) {
      const rel = path.relative(SRC, file)
      if (rel === path.join('design', 'glass.css')) continue
      const code = readFileSync(file, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')
      code.split('\n').forEach((line, i) => {
        const m = /backdrop-filter\s*:\s*([^;]+)/.exec(line)
        if (m && m[1]!.trim() !== 'none') found.push(`${rel}:${i + 1}`)
      })
    }
    expect(found).toEqual([])
  })
})
