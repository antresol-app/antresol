// Design rule 1 (web/DESIGN.md): no literal values in module CSS. Colours,
// type, radii, shadows, weights, layers, motion, spacing and control sizes come
// from the tokens; the one `@keyframes spin` is design/motion.css's (ui/Spinner
// turns by it). The modules that still hold literals are in ratchet.json, with
// their counts: a count may fall, never rise, and a file that has none gets
// none. Video chrome and art are the exceptions (cssRules.ts EXEMPT, and a
// `/* ds-allow: why */` after the declaration).
import { describe, expect, it } from 'vitest'
import { CSS_RULES, countCss, skippedFor } from './cssRules'
import { nonZero, ratchet, read, sourceFiles, type Counts } from './ratchet'

describe('what each rule counts', () => {
  const one = (css: string) => countCss(css)

  it('a literal colour in a value, but not a token, a url or the colour of a shadow', () => {
    expect(one('.a { color: #fff; background: rgba(0 0 0 / 50%); border-color: hsl(0 0% 0%) }')).toEqual({ colour: 3 })
    expect(one('.a { color: var(--label, #fff); background: url(#grad) }')).toEqual({})
    expect(one('.a { box-shadow: 0 1px 2px rgba(0,0,0,.3) }')).toEqual({ shadow: 1 })
    expect(one('.a { box-shadow: var(--shadow-card); outline: 2px solid var(--focus-ring) }')).toEqual({})
  })

  it('a written-out font size, not a token', () => {
    expect(one('.a { font: 600 13px / 1.2 var(--font-text); font-size: 0.9rem }')).toEqual({ type: 2 })
    expect(one('.a { font: var(--t-callout); font-size: inherit }')).toEqual({})
  })

  it('a radius in px, but not a circle, a pill or zero', () => {
    expect(one('.a { border-radius: 14px; border-top-left-radius: 2px }')).toEqual({ radius: 2 })
    expect(one('.a { border-radius: 50%; border-radius: 0; border-radius: 999px; border-radius: var(--r-md) 0 0 var(--r-md) }')).toEqual({})
  })

  it('a numeric weight and a numeric layer, not the tokens', () => {
    expect(one('.a { font-weight: 600; z-index: 3 }')).toEqual({ weight: 1, zindex: 1 })
    expect(one('.a { font-weight: var(--w-semibold); z-index: var(--z-raised); z-index: 0; font-weight: normal }')).toEqual({})
  })

  it('a duration in ms or s, not a token or zero', () => {
    expect(one('.a { transition: opacity 160ms ease; animation: spin 0.8s linear infinite; animation-delay: 120ms }')).toEqual({ motion: 3 })
    expect(one('.a { transition: opacity var(--d-fast) var(--ease-out); animation-delay: 0s }')).toEqual({})
  })

  it('spacing in px above 2, and a control size of the scale', () => {
    expect(one('.a { padding: 0 14px; margin-top: -10px; gap: 6px 2px }')).toEqual({ space: 3 })
    expect(one('.a { padding: 0 var(--s-4); margin: 1px; gap: 2px; margin-inline: auto }')).toEqual({})
    expect(one('.a { height: 44px; min-width: 32px; width: 120px; height: var(--control-h-md) }')).toEqual({ control: 2 })
  })

  it('declarations inside @media, nesting and @keyframes count like any other', () => {
    expect(one('@media (max-width: 699px) { .a { color: #fff } } .b { .c { z-index: 9 } } @keyframes k { to { color: #000 } }')).toEqual({ colour: 2, zindex: 1 })
  })

  it('a second @keyframes spin is a copy; other names are not', () => {
    expect(one('@keyframes spin { to { transform: rotate(360deg) } } @keyframes pulse { 50% { opacity: .5 } }')).toEqual({ spinKeyframes: 1 })
  })

  it('a trailing ds-allow comment (or one that opens the rule) excuses the declaration, and says why', () => {
    expect(one('.a { color: #fff; /* ds-allow: art */\n background: #000 }')).toEqual({ colour: 1 })
    expect(one('.a { color: #fff /* ds-allow: art */ }')).toEqual({})
    expect(one('.a { /* ds-allow: a poster */ color: #fff; background: #000 }')).toEqual({})
    expect(one('.a { color: #fff; /* a note */\n }')).toEqual({ colour: 1 })
  })

  it('names every rule it counts', () => {
    expect(CSS_RULES).toHaveLength(10)
  })
})

describe('the stylesheets', () => {
  const files = sourceFiles((n) => n.endsWith('.css'))

  it('are found', () => {
    expect(files.length).toBeGreaterThan(150)
    expect(files).toContain('web/src/ui/Button.module.css')
  })

  it('hold no more literals than ratchet.json lets them (and the kit holds none of the rules it exists for)', () => {
    const found: Counts = {}
    for (const f of files) {
      const counts = nonZero(countCss(read(f), skippedFor(f)))
      if (Object.keys(counts).length > 0) found[f] = counts
    }
    ratchet('css', found, 'literal values in a stylesheet')
  })

  it('declare `@keyframes spin` once, in design/motion.css', () => {
    const copies = files.filter((f) => f !== 'web/src/design/motion.css' && countCss(read(f), CSS_RULES.filter((r) => r !== 'spinKeyframes')).spinKeyframes)
    // the kit and the addons' shared parts never carry one; the pages' copies are in ratchet.json until they take ui/Spinner
    expect(copies.filter((f) => f.startsWith('web/src/ui/'))).toEqual([])
    expect(read('web/src/design/motion.css')).toMatch(/@keyframes spin \{/)
  })
})
