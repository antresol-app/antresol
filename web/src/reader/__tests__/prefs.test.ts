import { afterEach, describe, expect, it, vi } from 'vitest'
import { DEFAULT_PREFS, loadPrefs, savePrefs, type ReaderPrefs } from '../prefs'

afterEach(() => {
  vi.unstubAllGlobals()
  try {
    localStorage.clear()
  } catch {
    /* stubbed */
  }
})

describe('reader prefs', () => {
  it('loadPrefs returns defaults when storage throws', () => {
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new DOMException('denied', 'SecurityError')
      },
      setItem: () => {
        throw new DOMException('denied', 'SecurityError')
      },
    })
    expect(loadPrefs()).toEqual(DEFAULT_PREFS)
    expect(() => savePrefs({ ...DEFAULT_PREFS, theme: 'night' })).not.toThrow()
  })

  it('savePrefs round-trips', () => {
    const p: ReaderPrefs = { fontScale: 1.3, theme: 'paper', font: 'iowan', columns: 'one', margin: 'wide' }
    savePrefs(p)
    expect(localStorage.getItem('vault.reader.v1')).not.toBeNull()
    expect(loadPrefs()).toEqual(p)
  })

  it('ignores broken or out-of-range values', () => {
    localStorage.setItem('vault.reader.v1', '{"fontScale": 9, "theme": "neon", "font": "georgia"}')
    expect(loadPrefs()).toEqual({ ...DEFAULT_PREFS, fontScale: 2, font: 'georgia' })
    localStorage.setItem('vault.reader.v1', 'not json')
    expect(loadPrefs()).toEqual(DEFAULT_PREFS)
  })
})
