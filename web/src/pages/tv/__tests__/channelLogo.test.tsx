// An IPTV channel's tvg-logo that does not load gives way to its guide
// icon (logoAlt), and with neither nothing is drawn (19.11: a playlist's
// dead logos hid the guide's).
import { describe, expect, it } from 'vitest'
import { fireEvent, render } from '@testing-library/react'
import { ChannelLogo, channelTone, TILE_TONES } from '../ChannelLogo'

describe('ChannelLogo', () => {
  it('falls back to the guide icon, then to nothing', () => {
    const { container } = render(<ChannelLogo url="http://pl.example.test/dead.png" fallback="http://epg.example.test/1.png" />)
    const img = () => container.querySelector('img')
    expect(img()?.getAttribute('src')).toBe('http://pl.example.test/dead.png')
    fireEvent.error(img()!)
    expect(img()?.getAttribute('src')).toBe('http://epg.example.test/1.png')
    fireEvent.error(img()!)
    expect(img()).toBeNull()
  })
  it('keeps a slot when asked', () => {
    const { container } = render(<ChannelLogo url={undefined} fallback={undefined} reserve />)
    expect(container.querySelector('[data-logo-slot]')).not.toBeNull()
  })
  it('a channel without a logo gets a coloured tile of its own: its initials, the same colour everywhere', () => {
    const { container } = render(<ChannelLogo name="Documentaries" />)
    const tile = container.querySelector('[data-channel-initials]')!
    expect(tile.textContent).toBe('D')
    expect(container.querySelector('img')).toBeNull()
    expect(tile.getAttribute('data-channel-tone')).toBe(String(channelTone('Documentaries')))
    expect(channelTone('Documentaries')).toBe(channelTone(' documentaries '))
    const tones = new Set(['All Series', 'Documentaries', 'Kids', 'Movies', 'My Library', 'Comedy', 'Drama'].map(channelTone))
    expect([...tones].every((t) => t >= 1 && t <= TILE_TONES)).toBe(true)
    expect(tones.size).toBeGreaterThan(2)
  })
})
