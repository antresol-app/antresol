import { describe, expect, it } from 'vitest'
import { DEFAULT_PREFS } from '../prefs'
import { bookCss } from '../readerStyle'

describe('bookCss', () => {
  it('keeps the web look by default', () => {
    const css = bookCss(DEFAULT_PREFS)
    expect(css).toContain('line-height: 1.5')
    expect(css).toContain('text-align: justify')
    expect(css).not.toContain('font-weight: 600')
    expect(bookCss(DEFAULT_PREFS, true)).toContain('text-align: start')
  })

  it('applies line spacing, bold and justification', () => {
    const css = bookCss({ ...DEFAULT_PREFS, lineSpacing: 'loose', bold: true, justify: false })
    expect(css).toContain('line-height: 1.75')
    expect(css).toContain('font-weight: 600')
    expect(css).toContain('text-align: start')
    expect(bookCss({ ...DEFAULT_PREFS, justify: true }, true)).toContain('text-align: justify')
  })

  it('the Books themes: quiet is dark, bold is bold, focus is serif', () => {
    const quiet = bookCss({ ...DEFAULT_PREFS, theme: 'quiet' })
    expect(quiet).toContain('color-scheme: dark')
    expect(quiet).toContain('#4a4a4d')
    expect(bookCss({ ...DEFAULT_PREFS, theme: 'bold' })).toContain('font-weight: 600')
    expect(bookCss({ ...DEFAULT_PREFS, theme: 'focus' })).toContain('Iowan Old Style')
    expect(bookCss({ ...DEFAULT_PREFS, theme: 'focus', font: 'georgia' })).not.toContain('Iowan Old Style')
    expect(bookCss({ ...DEFAULT_PREFS, theme: 'calm' })).toContain('#ede0c8')
  })
})
