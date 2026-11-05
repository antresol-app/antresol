import { describe, expect, it } from 'vitest'
import { resolution } from '../InfoPanel'

describe('file info resolution', () => {
  it('names the tier by both sides: scope films are 1080p, not 720p', () => {
    expect(resolution(1920, 800)).toBe('1920×800 (1080p)')
    expect(resolution(2048, 858)).toBe('2048×858 (1080p)')
    expect(resolution(3840, 1600)).toBe('3840×1600 (4K)')
    expect(resolution(2560, 1072)).toBe('2560×1072 (1440p)')
    expect(resolution(1280, 536)).toBe('1280×536 (720p)')
    expect(resolution(1080, 1920)).toBe('1080×1920 (1080p)')
    expect(resolution(720, 576)).toBe('720×576 (576p)')
  })
})
