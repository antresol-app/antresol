// Design rule 2 (web/DESIGN.md): one component per job. A screen does not draw
// its own button, field, switch or menu, or size things with a literal in
// `style={{ … }}`: it uses src/ui (Button, IconButton, TextField, Toggle, Menu,
// a class with tokens). The pages that still do are in ratchet.json with their
// counts: a count may fall, never rise, and a file that has none gets none, so a
// new raw control shows up in review as a line in the baseline.
import { describe, expect, it } from 'vitest'
import { nonZero, ratchet, read, sourceFiles, type Counts } from './ratchet'

const CONTROLS = {
  button: /<button(?=[\s>/])/g,
  input: /<input(?=[\s>/])/g,
  textarea: /<textarea(?=[\s>/])/g,
  select: /<select(?=[\s>/])/g,
  // a JSX attribute (after a space), not a selector in a string ('[role="menu"]')
  switch: /(?<=\s)role=(?:"switch"|'switch'|\{'switch'\})/g,
  menu: /(?<=\s)role=(?:"menu"|'menu'|\{'menu'\})/g,
} as const

/** The code without its comments (`//` after a space or at the line's start, so a url stays whole). */
const stripComments = (src: string) => src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|\s)\/\/.*$/gm, '$1')

/** `style={{ … }}` that writes a number (a margin, a width, a translate) rather than naming a token or a computed value. */
function literalStyles(src: string): number {
  // a template's `${…}` parts are computed values, and their braces would end the object early
  const code = src.replace(/\$\{(?:[^{}]|\{[^{}]*\})*\}/g, '')
  let n = 0
  // the object may hold one more level of braces (a spread of a ternary's object)
  for (const m of code.matchAll(/style=\{\{((?:[^{}]|\{[^{}]*\})*)\}\}/g)) {
    const body = m[1]!.replace(/var\([^)]*\)/g, '')
    // a digit that does not end a name (`h1`, `--col-2`); a minus that stands alone before it makes a negative number
    if (/(?<!\w)(?<!\w-)\d/.test(body)) n++
  }
  return n
}

export function countControls(source: string): Record<string, number> {
  const src = stripComments(source)
  const out: Record<string, number> = {}
  for (const [name, re] of Object.entries(CONTROLS)) out[name] = src.match(re)?.length ?? 0
  out.style = literalStyles(src)
  return out
}

describe('what is counted', () => {
  it('raw controls and inline literals, not the kit’s components, comments or menu items', () => {
    const src = `
      // <button> in a comment
      /* <input /> */
      export const A = () => (
        <div role="menu">
          <button type="button" role="menuitem">x</button>
          <Button>kit</Button>
          <input type="range" />
          <textarea />
          <select />
          <span role="switch" />
          <i style={{ marginTop: 10 }} />
          <i style={{ width: \`\${w}%\`, '--v': x }} />
          <i style={{ color: 'var(--red)', margin: 'var(--s-2)' }} />
        </div>
      )`
    expect(countControls(src)).toEqual({ button: 1, input: 1, textarea: 1, select: 1, switch: 1, menu: 1, style: 1 })
  })

  it('a negative number or a function of one is a literal like any other', () => {
    expect(countControls('<i style={{ marginTop: -10 }} />').style).toBe(1)
    expect(countControls("<i style={{ transform: 'translateX(-50%)' }} />").style).toBe(1)
    expect(countControls('<i style={{ opacity: 0.5 }} />').style).toBe(1)
  })

  it('a template in the object neither hides a literal nor makes one', () => {
    expect(countControls('<i style={{ width: `${w}%`, marginTop: 10 }} />').style).toBe(1)
    expect(countControls('<i style={{ marginTop: 10, width: `${w}%` }} />').style).toBe(1)
    expect(countControls('<i style={{ width: `${w}%`, height: `calc(${h}px - var(--s-2))` }} />').style).toBe(0)
    expect(countControls('<i style={{ animationDelay: `calc(${i} * var(--d-stagger))` }} />').style).toBe(0)
  })

  it('an object inside the object (a spread of a ternary) is read to its end', () => {
    expect(countControls('<i style={{ ...(open ? { opacity: 1 } : {}) }} />').style).toBe(1)
    expect(countControls('<i style={{ ...(open ? { opacity: x } : {}), color: c }} />').style).toBe(0)
  })

  it('names that hold a digit are not numbers', () => {
    expect(countControls("<i style={{ gridArea: 'h1', '--col-2': x }} />").style).toBe(0)
  })
})

describe('the screens', () => {
  const files = sourceFiles((n) => n.endsWith('.tsx') && !n.endsWith('.test.tsx')).filter((f) => !f.startsWith('web/src/ui/') && !f.startsWith('web/src/test/'))

  it('are found', () => {
    expect(files.length).toBeGreaterThan(200)
    expect(files).not.toContain('web/src/ui/Button.tsx')
  })

  it('hold no more raw controls than ratchet.json lets them', () => {
    const found: Counts = {}
    for (const f of files) {
      const counts = nonZero(countControls(read(f)))
      if (Object.keys(counts).length > 0) found[f] = counts
    }
    ratchet('controls', found, 'raw controls or inline literals in a component')
  })
})
