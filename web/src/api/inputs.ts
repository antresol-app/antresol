// An addon's video played in the core player (/play/input/:id): the input,
// the play decision (the server probes the file first, answering 202
// meanwhile) and the position (/api/play/input/{id}/*).
import { request } from './client'
import type { PlayRequest, PlayResponse, PlayerInput } from './types'

/** How often the play decision is asked again while the server probes the file. */
export const INPUT_PROBE_POLL_MS = 2000

const base = (id: string) => `/api/play/input/${encodeURIComponent(id)}`

export const getInput = (id: string, signal?: AbortSignal) => request<PlayerInput>('GET', base(id), undefined, signal)

/** The play decision; asks again every INPUT_PROBE_POLL_MS while the answer is 202 (AbortError once `cancelled`). */
export async function inputPlay(id: string, body: PlayRequest, cancelled: () => boolean = () => false): Promise<PlayResponse> {
  for (;;) {
    const r = await request<PlayResponse | { pending: true }>('POST', base(id), body)
    if (!('pending' in r)) return r
    if (cancelled()) throw new DOMException('cancelled', 'AbortError')
    await new Promise((res) => setTimeout(res, INPUT_PROBE_POLL_MS))
    if (cancelled()) throw new DOMException('cancelled', 'AbortError')
  }
}

/** The player's position (`keepalive`: the page is going away, the request outlives it). */
export const saveInputProgress = (id: string, positionMs: number, durationMs: number, keepalive: boolean) =>
  request<void>('PUT', `${base(id)}/progress`, { positionMs, durationMs, keepalive }, undefined, keepalive)
