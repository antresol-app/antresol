// The sidebar's icon tiles take their colours from the palette tokens, never
// from literal values in the page.
import { describe, expect, it } from 'vitest'
import { MANAGE_SECTIONS } from '../Manage'

describe('Manage sections', () => {
  it('tint their icon tiles with palette tokens', () => {
    for (const s of MANAGE_SECTIONS) expect(s.tint, s.to).toMatch(/^var\(--[a-z0-9-]+\)$/)
  })
})
