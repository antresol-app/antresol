// The list's tiles are one system (ui/SettingsIcon): a glyph of the set and a
// palette colour each, no glyph twice (the owner: «two gear icons»).
import { describe, expect, it } from 'vitest'
import { MANAGE_SECTIONS } from '../Manage'
import { SETTINGS_TINTS, isSettingsGlyph, settingsGlyph, settingsTint } from '../../../ui/SettingsIcon'

describe('Manage sections', () => {
  it('take a glyph of the tile set and a palette tint, every glyph once', () => {
    for (const s of MANAGE_SECTIONS) {
      expect(isSettingsGlyph(s.glyph), s.to).toBe(true)
      expect(SETTINGS_TINTS, s.to).toContain(s.tint)
    }
    const glyphs = MANAGE_SECTIONS.map((s) => s.glyph)
    expect(new Set(glyphs).size).toBe(glyphs.length)
  })

  it("draw an addon's line icon as the tile glyph of its meaning, anything else as the puzzle piece", () => {
    expect(settingsGlyph('arrowDownCircle')).toBe('arrowDownCircle')
    expect(settingsGlyph('download')).toBe('arrowDownCircle')
    expect(settingsGlyph('gear')).toBe('sliders')
    expect(settingsGlyph('nope')).toBe('puzzle')
    expect(settingsTint('green')).toBe('green')
    expect(settingsTint('var(--red)')).toBe('gray')
    expect(settingsTint(undefined)).toBe('gray')
  })
})
