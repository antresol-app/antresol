// The input player's API: the play decision asks again while the server
// probes (202), and the position goes to the input's own progress route.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { INPUT_PROBE_POLL_MS, getInput, inputPlay, saveInputProgress } from '../inputs'
import type { PlayRequest } from '../types'

const json = (b: unknown, status = 200) =>
  new Response(b === undefined ? null : JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const body: PlayRequest = { caps: { videoCodecs: ['h264'], audioCodecs: ['aac'], containers: ['mp4'] } as never }
const answer = { mode: 'direct', url: '/api/play/input/ab/direct', subtitles: [], audio: [], audioIndex: 0, startSec: 0 }

type Call = { url: string; init?: RequestInit }
let calls: Call[]

describe('input API', () => {
  beforeEach(() => {
    calls = []
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('getInput reads the input by its id', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => (calls.push({ url, init }), json({ id: 'a b', startSec: 12 }))))
    await expect(getInput('a b')).resolves.toMatchObject({ startSec: 12 })
    expect(calls[0]!.url).toBe('/api/play/input/a%20b')
    expect(calls[0]!.init?.method).toBe('GET')
  })

  it('asks again while the server answers 202 and resolves with the decision', async () => {
    vi.useFakeTimers()
    const answers = [json({ pending: true }, 202), json({ pending: true }, 202), json(answer)]
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => (calls.push({ url, init }), answers.shift()!)))
    let done: unknown
    void inputPlay('ab', body).then((r) => (done = r))
    await vi.advanceTimersByTimeAsync(0)
    expect(calls).toHaveLength(1)
    expect(calls[0]!.url).toBe('/api/play/input/ab')
    expect(calls[0]!.init?.method).toBe('POST')
    expect(JSON.parse(String(calls[0]!.init?.body))).toEqual(body)
    await vi.advanceTimersByTimeAsync(INPUT_PROBE_POLL_MS)
    expect(calls).toHaveLength(2)
    expect(done).toBeUndefined()
    await vi.advanceTimersByTimeAsync(INPUT_PROBE_POLL_MS)
    expect(calls).toHaveLength(3)
    expect(done).toEqual(answer)
  })

  it('stops asking once cancelled (AbortError)', async () => {
    vi.useFakeTimers()
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => (calls.push({ url, init }), json({ pending: true }, 202))))
    let gone = false
    const p = inputPlay('ab', body, () => gone)
    const caught = p.catch((e: unknown) => e)
    await vi.advanceTimersByTimeAsync(0)
    gone = true
    await vi.advanceTimersByTimeAsync(INPUT_PROBE_POLL_MS)
    const e = await caught
    expect(e).toBeInstanceOf(DOMException)
    expect((e as DOMException).name).toBe('AbortError')
    expect(calls).toHaveLength(1)
  })

  it('saves the position with PUT …/progress, keepalive when asked', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => (calls.push({ url, init }), json(undefined, 204))))
    await saveInputProgress('ab', 30_000, 60_000, true)
    await saveInputProgress('ab', 31_000, 60_000, false)
    expect(calls[0]!.url).toBe('/api/play/input/ab/progress')
    expect(calls[0]!.init?.method).toBe('PUT')
    expect(JSON.parse(String(calls[0]!.init?.body))).toEqual({ positionMs: 30_000, durationMs: 60_000, keepalive: true })
    expect(calls[0]!.init?.keepalive).toBe(true)
    expect(calls[1]!.init?.keepalive).toBeUndefined()
    expect(JSON.parse(String(calls[1]!.init?.body))).toMatchObject({ keepalive: false })
  })
})
