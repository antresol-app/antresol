// The server-sent events the app asks for by name: the core's own, the same
// in every build (an addon's come as addon.<id>.<name>, listenEvent).
import { describe, expect, it, vi } from 'vitest'
import { render } from '@testing-library/react'
import { useEvents } from '../events'

const named: string[] = []
class FakeSource {
  addEventListener(type: string) {
    named.push(type)
  }
  close() {}
  onmessage: unknown = null
}

function Listener() {
  useEvents(() => {})
  return null
}

describe('events', () => {
  it('no edition events', () => {
    vi.stubGlobal('EventSource', FakeSource)
    const { unmount } = render(<Listener />)
    expect(named).toEqual(['library.updated', 'request.changed', 'disk.warning', 'migrate.progress', 'import.progress', 'addons.changed'])
    unmount()
    vi.unstubAllGlobals()
  })
})
