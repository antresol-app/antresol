import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { useState } from 'react'
import type { PlaybackPrefs } from '../../api/types'
import { LEGACY_AUTO_SKIP, useAutoSkip } from '../useAutoSkip'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const prefsWith = (autoSkip?: { recap?: boolean; intro?: boolean; credits?: boolean }): PlaybackPrefs => ({
  audioLangs: ['ru'],
  subMode: 'off',
  subLangs: [],
  subSize: 'm',
  subBackground: 'translucent',
  custom: false,
  autoSkip: autoSkip && { recap: false, intro: false, credits: false, ...autoSkip },
})

/** The server's side: what the PUTs carried, and whether they succeed (or hang until released). */
function server(status = 200) {
  const puts: Record<string, boolean>[] = []
  let release: () => void = () => {}
  const gate = { held: false }
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith('/auto-skip') && init?.method === 'PUT') {
        puts.push(JSON.parse(String(init.body)))
        if (gate.held) await new Promise<void>((r) => (release = r))
        return status === 200 ? json({}) : json({ error: 'нельзя' }, status)
      }
      return json({}, 404)
    }),
  )
  return { puts, hold: () => (gate.held = true), release: () => release() }
}

function mount(initial: PlaybackPrefs) {
  return renderHook(() => {
    const [prefs, setPrefs] = useState(initial)
    const [autoSkip, change] = useAutoSkip(prefs, setPrefs)
    return { autoSkip, change }
  })
}

const keys = () => [localStorage.getItem(LEGACY_AUTO_SKIP.recap), localStorage.getItem(LEGACY_AUTO_SKIP.intro)]

describe('useAutoSkip', () => {
  beforeEach(() => localStorage.clear())
  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
  })

  it('moves what the browser kept to the profile, and clears it only after the server took it', async () => {
    const s = server()
    s.hold()
    localStorage.setItem(LEGACY_AUTO_SKIP.intro, '1')
    localStorage.setItem(LEGACY_AUTO_SKIP.recap, '0')
    const { result } = mount(prefsWith({}))
    await waitFor(() => expect(s.puts).toEqual([{ intro: true }]))
    // sent, not answered: the browser still has its choice
    expect(keys()).toEqual(['0', '1'])
    await act(async () => s.release())
    await waitFor(() => expect(keys()).toEqual([null, null]))
    expect(result.current.autoSkip).toEqual({ recap: false, intro: true, credits: false })
    expect(s.puts).toHaveLength(1)
  })

  it('keeps the browser’s choice when the server refuses or is down', async () => {
    const s = server(500)
    localStorage.setItem(LEGACY_AUTO_SKIP.recap, '1')
    localStorage.setItem(LEGACY_AUTO_SKIP.intro, '1')
    const { result } = mount(prefsWith({}))
    await waitFor(() => expect(s.puts).toEqual([{ recap: true, intro: true }]))
    await act(async () => {})
    expect(keys()).toEqual(['1', '1'])
    expect(result.current.autoSkip).toEqual({ recap: false, intro: false, credits: false })
  })

  it('does not overwrite a choice the profile already made: the server wins', async () => {
    const s = server()
    localStorage.setItem(LEGACY_AUTO_SKIP.recap, '1')
    localStorage.setItem(LEGACY_AUTO_SKIP.intro, '1')
    // only credits is on there: a choice all the same
    const { result } = mount(prefsWith({ credits: true }))
    await waitFor(() => expect(keys()).toEqual([null, null]))
    expect(s.puts).toEqual([])
    expect(result.current.autoSkip).toEqual({ recap: false, intro: false, credits: true })
  })

  it('leaves the browser alone with an older server that keeps no choice', async () => {
    const s = server()
    localStorage.setItem(LEGACY_AUTO_SKIP.intro, '1')
    mount(prefsWith())
    await act(async () => {})
    expect(s.puts).toEqual([])
    expect(keys()).toEqual([null, '1'])
  })

  it('sends only the switch that changed, and takes it back when the server says no', async () => {
    const s = server(403)
    const { result } = mount(prefsWith({ intro: true }))
    await act(async () => result.current.change({ recap: true, intro: true, credits: false }))
    expect(s.puts).toEqual([{ recap: true }])
    await waitFor(() => expect(result.current.autoSkip).toEqual({ recap: false, intro: true, credits: false }))
  })

  it('shows the new choice at once and keeps it when the server takes it', async () => {
    const s = server()
    const { result } = mount(prefsWith({}))
    await act(async () => result.current.change({ recap: false, intro: false, credits: true }))
    expect(result.current.autoSkip.credits).toBe(true)
    expect(s.puts).toEqual([{ credits: true }])
    await act(async () => {})
    expect(result.current.autoSkip.credits).toBe(true)
  })
})
