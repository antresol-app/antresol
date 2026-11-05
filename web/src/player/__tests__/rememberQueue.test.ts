import { afterEach, describe, expect, it, vi } from 'vitest'
import { queueRemember, rememberSettled } from '../rememberQueue'

afterEach(() => vi.unstubAllGlobals())

describe('rememberQueue', () => {
  it('sends the choices one after another, in order, and settles after the last', async () => {
    const order: string[] = []
    let release!: () => void
    const first = new Promise<void>((r) => (release = r))
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, init?: RequestInit) => {
        const body = String(init?.body)
        order.push(`start ${body}`)
        if (body.includes('audio')) await first
        order.push(`end ${body}`)
        return new Response(null, { status: 204 })
      }),
    )
    queueRemember(9, { audio: { lang: 'rus', title: 'TVShows' } })
    queueRemember(9, { subtitleOff: true })
    let settled = false
    const done = rememberSettled().then(() => (settled = true))
    await new Promise((r) => setTimeout(r, 10))
    // the second waits for the first
    expect(order).toEqual(['start {"audio":{"lang":"rus","title":"TVShows"}}'])
    expect(settled).toBe(false)
    release()
    await done
    expect(order).toEqual([
      'start {"audio":{"lang":"rus","title":"TVShows"}}',
      'end {"audio":{"lang":"rus","title":"TVShows"}}',
      'start {"subtitleOff":true}',
      'end {"subtitleOff":true}',
    ])
  })

  it('a failed send does not stop the next', async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ error: 'x' }), { status: 500 }))
    vi.stubGlobal('fetch', fetch)
    queueRemember(9, { subtitleOff: true })
    queueRemember(9, { subtitleOff: true })
    await rememberSettled()
    expect(fetch).toHaveBeenCalledTimes(2)
  })
})
