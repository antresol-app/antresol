// An IPTV channel's tvg-logo that does not load gives way to its guide
// icon (logoAlt), and with neither nothing is drawn (19.11: a playlist's
// dead logos hid the guide's).
import { describe, expect, it } from 'vitest'
import { fireEvent, render } from '@testing-library/react'
import { ChannelLogo } from '../ChannelLogo'

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
})
