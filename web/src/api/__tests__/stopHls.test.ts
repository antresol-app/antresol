// Stopping an HLS session as the page leaves (pagehide, unmount): a beacon,
// which Safari still sends while it leaves a page (it cancels a keepalive
// fetch); a plain DELETE when the browser has no beacon or refuses it, and
// always a DELETE when the page stays.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { stopHls } from '../client'

type Call = { url: string; init?: RequestInit }

describe('stopHls', () => {
  afterEach(() => vi.unstubAllGlobals())

  const fetchCalls = () => {
    const calls: Call[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => (calls.push({ url, init }), new Response(null, { status: 204 }))))
    return calls
  }

  it('leaving: sends a beacon to the stop route and no fetch', async () => {
    const calls = fetchCalls()
    const beacon = vi.fn(() => true)
    vi.stubGlobal('navigator', { ...navigator, sendBeacon: beacon })
    await stopHls('a b', true)
    expect(beacon).toHaveBeenCalledWith('/api/hls/a%20b/stop')
    expect(calls).toHaveLength(0)
  })

  it('leaving without a beacon (refused or absent): a keepalive DELETE', async () => {
    const calls = fetchCalls()
    vi.stubGlobal('navigator', { ...navigator, sendBeacon: vi.fn(() => false) })
    await stopHls('s1', true)
    expect(calls).toHaveLength(1)
    expect(calls[0]!.url).toBe('/api/hls/s1')
    expect(calls[0]!.init).toMatchObject({ method: 'DELETE', keepalive: true })
  })

  it('staying: a DELETE, never a beacon', async () => {
    const calls = fetchCalls()
    const beacon = vi.fn(() => true)
    vi.stubGlobal('navigator', { ...navigator, sendBeacon: beacon })
    await stopHls('s1')
    expect(beacon).not.toHaveBeenCalled()
    expect(calls[0]!.init?.method).toBe('DELETE')
  })
})
