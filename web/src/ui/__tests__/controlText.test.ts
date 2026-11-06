// A control's label centres on its capitals (--control-text-box), the same
// way in every control and every engine, and no label is nudged by hand.
import { describe, expect, it } from 'vitest'
import { css, rule } from './cssRule'

/** Every element that holds a control's label, by the module that styles it. */
const LABELS: [string, string][] = [
  ['ui/Button.module.css', '.label'],
  ['ui/Select.module.css', '.value'],
  ['ui/Select.module.css', '.label'],
  ['ui/SegmentedControl.module.css', '.label'],
  ['ui/NumberField.module.css', '.text'],
  ['ui/Toast.module.css', '.actionLabel'],
  ['ui/Sheet.module.css', '.label'],
  ['components/TopNav.module.css', '.itemText'],
  ['components/LibraryScopeMenu.module.css', '.long'],
  ['components/LibraryScopeMenu.module.css', '.short'],
]

describe('control labels', () => {
  it.each(LABELS)('%s %s centres on its capitals', (file, selector) => {
    expect(rule(file, selector)).toContain('text-box: var(--control-text-box)')
  })

  it('are never nudged by a transform', () => {
    for (const f of new Set(LABELS.map(([f]) => f))) {
      expect(css(f), f).not.toMatch(/translateY\(\s*-?0?\.\d+px\s*\)/)
    }
  })

  it('the token is defined once, in the tokens', () => {
    expect(css('design/tokens.css')).toMatch(/--control-text-box:\s*trim-both cap alphabetic;/)
  })
})
