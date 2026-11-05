import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import { MemoryRouter } from 'react-router'
import { MiniPlayer } from '../MiniPlayer'
import { NowPlaying } from '../NowPlaying'
import { audio, resetAudioForTests, type QueueItem } from '../audio/engine'

const item = (n: number, over: Partial<QueueItem> = {}): QueueItem => ({
  key: `t${n}`,
  trackId: n,
  fileId: 200 + n,
  title: `Трек ${n}`,
  artist: 'Тестовый артист',
  artistId: 5,
  album: 'Первый альбом',
  albumId: 1,
  cover: '/api/images/local/1/poster',
  durationSec: 20,
  src: `/api/tracks/${n}/stream?accept=flac`,
  lossless: true,
  ...over,
})

function Harness() {
  const [open, setOpen] = useState(false)
  return (
    <MemoryRouter>
      <MiniPlayer onExpand={() => setOpen(true)} />
      <NowPlaying open={open} onClose={() => setOpen(false)} />
    </MemoryRouter>
  )
}

beforeEach(() => {
  resetAudioForTests()
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(JSON.stringify({ error: 'Текста нет' }), { status: 404 })),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
  resetAudioForTests()
})

describe('NowPlaying', () => {
  it('opens from mini player and closes on Escape', async () => {
    render(<Harness />)
    act(() => audio.play([item(1), item(2)], 0))
    expect(screen.queryByRole('dialog', { name: 'Сейчас играет' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Трек 1 — Тестовый артист. Открыть плеер/ }))
    const dialog = await screen.findByRole('dialog', { name: 'Сейчас играет' })
    expect(dialog).toHaveTextContent('Трек 1')
    expect(dialog).toHaveTextContent('Lossless')
    expect(screen.getByRole('link', { name: 'Тестовый артист' })).toHaveAttribute('href', '/music/artist/5')
    expect(screen.getByRole('button', { name: 'Свернуть' })).toBeInTheDocument()
    expect(screen.getByRole('slider', { name: 'Громкость' })).toBeInTheDocument()
    fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Сейчас играет' })).not.toBeInTheDocument())
  })

  it('repeat button cycles aria-label выкл → все → один трек', async () => {
    render(<Harness />)
    act(() => audio.play([item(1)], 0))
    fireEvent.click(screen.getByRole('button', { name: /Открыть плеер/ }))
    const btn = await screen.findByRole('button', { name: 'Повтор: выкл' })
    fireEvent.click(btn)
    expect(screen.getByRole('button', { name: 'Повтор: все' })).toBe(btn)
    fireEvent.click(btn)
    expect(screen.getByRole('button', { name: 'Повтор: один трек' })).toBe(btn)
    expect(audio.get().repeat).toBe('one')
    fireEvent.click(btn)
    expect(screen.getByRole('button', { name: 'Повтор: выкл' })).toBe(btn)
  })

  it('shows Текста нет when lyrics 404', async () => {
    render(<Harness />)
    act(() => audio.play([item(1, { hasLyrics: true })], 0))
    fireEvent.click(screen.getByRole('button', { name: /Открыть плеер/ }))
    fireEvent.click(await screen.findByRole('button', { name: 'Текст' }))
    expect(await screen.findByText('Текста нет')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/music/tracks/1/lyrics', expect.anything())
  })

  it('queue lists the next tracks and moves them', async () => {
    render(<Harness />)
    act(() => audio.play([item(1), item(2), item(3)], 0))
    fireEvent.click(screen.getByRole('button', { name: /Открыть плеер/ }))
    fireEvent.click(await screen.findByRole('button', { name: 'Очередь' }))
    const next = screen.getByRole('list', { name: 'Далее' })
    expect(next).toHaveTextContent(/Трек 2.*Трек 3/)
    fireEvent.click(screen.getByRole('button', { name: 'Ниже: Трек 2' }))
    expect(audio.get().queue.map((q) => q.title)).toEqual(['Трек 1', 'Трек 3', 'Трек 2'])
    // focus follows the moved track
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Выше: Трек 2' }))
    fireEvent.click(screen.getByRole('button', { name: 'Убрать: Трек 3' }))
    expect(audio.get().queue.map((q) => q.title)).toEqual(['Трек 1', 'Трек 2'])
    // a tap plays it
    fireEvent.click(screen.getByRole('button', { name: /Трек 2 — Тестовый артист. Включить/ }))
    expect(audio.get().index).toBe(1)
  })
})
