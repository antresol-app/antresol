import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { MediaFile, SubTrack } from '../../api/types'
import { session } from '../../state/session'

vi.mock('hls.js', () => {
  class FakeHls {
    static Events = { ERROR: 'error' }
    static ErrorTypes = { NETWORK_ERROR: 'networkError' }
    static isSupported = () => true
    on() {}
    loadSource() {}
    attachMedia() {}
    destroy() {}
  }
  return { default: FakeHls }
})

import { VideoPlayer } from '../VideoPlayer'

const admin = { id: 1, name: 'Александр', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null }
const json = (b: unknown, status = 200) => new Response(b === undefined ? null : JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const embedded: SubTrack = { index: 2, codec: 'subrip', lang: 'en', title: '', forced: false, default: false, external: false, burnIn: false, url: '/api/subs/5/2.vtt' }
const fresh: SubTrack = { index: 1000, codec: 'subrip', lang: 'ru', title: '', forced: false, default: false, external: true, burnIn: false, url: '/api/subs/5/1000.vtt' }

describe('VideoPlayer: «Найти субтитры…»', () => {
  beforeEach(() => {
    session.reset()
    localStorage.clear()
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  })
  afterEach(() => vi.unstubAllGlobals())

  it('after a download the new track is listed and turned on, then the sheet closes', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        if (url.endsWith('/api/login')) return json(admin)
        if (url.includes('/api/subtitles/5/search')) return json({ results: [{ subtitleId: 3, lang: 'ru', release: 'Dune.ru', downloads: 10, hashMatch: true }] })
        if (url.endsWith('/api/subtitles/5') && init?.method === 'POST') return json(undefined, 204)
        if (url.endsWith('/api/items/9')) return json({ id: 9, files: [{ id: 5, subs: [embedded, fresh] }] })
        if (url.endsWith('.vtt')) return new Response('WEBVTT\n\n00:00.000 --> 00:05.000\nПривет\n', { status: 200 })
        return json({})
      }),
    )
    await session.login(1)
    const start = vi.fn(async () => ({ mode: 'direct', url: '/api/stream/5', startSec: 0, durationMs: 60000, audioIndex: 0, audio: [], subtitles: [embedded] }))
    render(<VideoPlayer itemId={9} title="Дюна" parts={[{ id: 5, name: 'dune.mkv', durationMs: 60000 } as MediaFile]} startSec={0} onClose={() => {}} start={start as never} />)
    await waitFor(() => expect(start).toHaveBeenCalled())

    await userEvent.click(screen.getByRole('button', { name: 'Аудио и субтитры' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Найти субтитры…' }))
    const sheet = await screen.findByRole('dialog', { name: 'Найти субтитры' })
    await userEvent.click(await within(sheet).findByRole('button', { name: /Скачать/ }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Найти субтитры' })).not.toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: 'Аудио и субтитры' }))
    const menu = await screen.findByRole('menu', { name: 'Аудио и субтитры' })
    const ru = within(menu).getByRole('menuitemradio', { name: /Русский/ })
    expect(ru).toHaveAttribute('aria-checked', 'true')
    expect(within(menu).getByRole('menuitemradio', { name: /Английский/ })).toHaveAttribute('aria-checked', 'false')
  })
})
