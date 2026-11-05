import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, render } from '@testing-library/react'
import { useEvents } from '../events'
import { setLanguagePref } from '../../i18n'

// EventSource cannot set headers: the language rides in ?lang=.
const urls: string[] = []
class FakeSource {
  constructor(url: string) {
    urls.push(url)
  }
  addEventListener() {}
  close() {}
  onmessage: unknown = null
}

function Listener() {
  useEvents(() => {})
  return null
}

describe('server events carry the interface language', () => {
  afterEach(() => setLanguagePref('system'))

  it('connects with ?lang= and reconnects after a change', () => {
    vi.stubGlobal('EventSource', FakeSource)
    const { unmount } = render(<Listener />)
    expect(urls.at(-1)).toBe('/api/events?lang=ru')
    act(() => setLanguagePref('en'))
    expect(urls.at(-1)).toBe('/api/events?lang=en')
    unmount()
    vi.unstubAllGlobals()
  })
})
