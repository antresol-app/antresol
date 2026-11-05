import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { NowPlaying } from '../NowPlaying'
import { audio, resetAudioForTests, type QueueItem } from '../audio/engine'
import { session } from '../../state/session'
import { msOf } from '../AudiobookBookmarks'

const part = (n: number): QueueItem => ({
  key: `b9f${n}`,
  fileId: 90 + n,
  title: 'Мастер и Маргарита',
  artist: 'Булгаков',
  cover: '',
  durationSec: 600,
  src: `/api/audio/${90 + n}`,
  bookStartMs: (n - 1) * 600_000,
})

const puts: { url: string; body: Record<string, unknown> }[] = []

beforeEach(async () => {
  localStorage.clear()
  puts.length = 0
  resetAudioForTests()
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const ok = (v: unknown) => new Response(JSON.stringify(v), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (url === '/api/me' || url === '/api/login') return ok({ id: 1, name: 'Александр', isAdmin: true, isKid: false, avatar: 'grad-1' })
      if (url === '/api/annotations/9' && !init?.method) return ok({ annotations: [] })
      if (url.startsWith('/api/annotations/9/') && init?.method === 'PUT') {
        const body = JSON.parse(String(init.body)) as Record<string, unknown>
        puts.push({ url, body })
        return ok(body)
      }
      return new Response('{}', { status: 404 })
    }),
  )
  session.reset()
  await session.login(1)
})
afterEach(() => {
  vi.unstubAllGlobals()
  resetAudioForTests()
})

describe('audiobook bookmarks', () => {
  it('«Закладки» marks the place playing now with a note and jumps back to it', async () => {
    render(
      <MemoryRouter>
        <NowPlaying open onClose={() => {}} />
      </MemoryRouter>,
    )
    act(() => audio.play([part(1), part(2)], 1, { kind: 'audiobook', bookId: 9, startSec: 65 }))
    fireEvent.click(await screen.findByRole('button', { name: 'Закладки' }))
    const sheet = await screen.findByRole('dialog', { name: 'Закладки' })
    expect(sheet).toHaveTextContent('Закладок пока нет.')
    fireEvent.change(screen.getByPlaceholderText('Заметка (необязательно)'), { target: { value: 'Бал у сатаны' } })
    fireEvent.click(screen.getByRole('button', { name: 'Добавить закладку' }))
    await waitFor(() => expect(puts).toHaveLength(1))
    const locator = String(puts[0]!.body.locator)
    expect(locator).toMatch(/^ms:\d+$/)
    expect(msOf(locator)).toBeGreaterThanOrEqual(600_000) // the second file: from the start of the book
    expect(puts[0]!.body).toMatchObject({ kind: 'bookmark', note: 'Бал у сатаны' })
    expect(await screen.findByText('Бал у сатаны')).toBeInTheDocument()
  })
})
