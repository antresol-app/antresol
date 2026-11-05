import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { BookDetail } from '../../../api/types'
import { audio, resetAudioForTests } from '../../../player/audio/engine'
import { SpeedMenu } from '../../../player/SpeedMenu'
import { AudiobookDetail } from '../AudiobookDetail'

const H = 3_600_000
const M = 60_000

function detail(over: Partial<BookDetail> = {}): BookDetail {
  return {
    card: {
      id: 5,
      kind: 'audiobook',
      title: 'Тестовая аудиокнига',
      subtitle: 'Тестовый автор',
      poster: '',
      backdrop: '',
      logo: '',
      progress: 0,
      onServer: true,
    },
    kind: 'audiobook',
    authors: ['Тестовый автор'],
    narrator: 'Иван Чтецов',
    series: null,
    description: '',
    year: 2021,
    lang: 'ru',
    files: [
      { id: 31, format: 'MP3', size: 300_000_000, durationMs: 5 * H },
      { id: 32, format: 'MP3', size: 300_000_000, durationMs: 7 * H + 30 * M },
    ],
    seriesId: 0,
    nextId: 0,
    readFileId: 0,
    // 7 h 18 min heard of 12 h 30 min: 5 h 12 min left
    progress: { fraction: 0.584, locator: '', positionMs: 7 * H + 18 * M, durationMs: 12 * H + 30 * M, finished: false },
    want: false,
    durationMs: 12 * H + 30 * M,
    chapters: [
      { idx: 0, title: 'Пролог', fileId: 31, startMs: 0, endMs: 5 * H },
      { idx: 1, title: 'Часть первая', fileId: 32, startMs: 5 * H, endMs: 9 * H },
      { idx: 2, title: 'Эпилог', fileId: 32, startMs: 9 * H, endMs: 12 * H + 30 * M },
    ],
    volumes: [],
    direction: '',
    ...over,
  }
}

beforeEach(() => {
  resetAudioForTests()
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === '/api/books/5') return new Response(JSON.stringify(detail()), { status: 200 })
      return new Response(null, { status: 204 })
    }),
  )
})

afterEach(() => {
  resetAudioForTests()
  vi.unstubAllGlobals()
})

describe('AudiobookDetail', () => {
  it('shows remaining time and chapter list', async () => {
    render(
      <MemoryRouter initialEntries={['/books/audiobook/5']}>
        <Routes>
          <Route path="/books/audiobook/:id" element={<AudiobookDetail />} />
        </Routes>
      </MemoryRouter>,
    )
    expect(await screen.findByRole('heading', { level: 1, name: 'Тестовая аудиокнига' })).toBeInTheDocument()
    expect(screen.getByText('Тестовый автор')).toBeInTheDocument()
    expect(screen.getByText('Читает: Иван Чтецов')).toBeInTheDocument()
    expect(screen.getByText(/12 ч 30 мин/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Продолжить · осталось 5 ч 12 мин' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Хочу прочитать' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'На это устройство' })).toBeInTheDocument()

    const list = screen.getByRole('list', { name: 'Главы' })
    const rows = within(list).getAllByRole('button')
    expect(rows).toHaveLength(3)
    expect(rows[0]).toHaveTextContent('Пролог')
    expect(rows[0]).toHaveTextContent('5:00:00')
    expect(rows[2]).toHaveTextContent('Эпилог')
    expect(rows[2]).toHaveTextContent('3:30:00')
    // the saved position (7 h 18 min) is in «Часть первая»
    expect(rows[1]).toHaveAttribute('aria-current', 'true')
    expect(rows[0]).not.toHaveAttribute('aria-current')
  })

  function renderWith(d: BookDetail) {
    vi.mocked(fetch).mockImplementation(async (input: RequestInfo | URL) =>
      String(input) === '/api/books/5' ? new Response(JSON.stringify(d), { status: 200 }) : new Response(null, { status: 204 }),
    )
    render(
      <MemoryRouter initialEntries={['/books/audiobook/5']}>
        <Routes>
          <Route path="/books/audiobook/:id" element={<AudiobookDetail />} />
        </Routes>
      </MemoryRouter>,
    )
  }

  it('a book heard to the end offers «Начать сначала» and plays from the start', async () => {
    const total = 12 * H + 30 * M
    renderWith(detail({ progress: { fraction: 1, locator: '', positionMs: total - 10_000, durationMs: total, finished: true } }))
    const btn = await screen.findByRole('button', { name: 'Начать сначала' })
    const src = vi.spyOn(HTMLMediaElement.prototype, 'src', 'set')
    fireEvent.click(btn)
    expect(src).toHaveBeenCalledWith(expect.stringMatching(/^\/api\/audio\/31\?/))
    expect(audio.get().positionSec).toBe(0)
  })

  it('a book marked finished well before its end (the old 92% rule) continues where it was', async () => {
    const total = 20 * H
    renderWith(detail({ durationMs: total, progress: { fraction: 1, locator: '', positionMs: 18 * H + 30 * M, durationMs: total, finished: true } }))
    expect(await screen.findByRole('button', { name: /^Продолжить · осталось 1 ч 30 мин/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Начать сначала' })).toBeNull()
  })

  it('speed menu lists 10 steps from 0,75× to 3×', () => {
    render(<SpeedMenu />)
    fireEvent.click(screen.getByRole('button', { name: /^Скорость/ }))
    const items = within(screen.getByRole('menu', { name: 'Скорость' })).getAllByRole('menuitemradio')
    expect(items.map((i) => i.textContent)).toEqual(['0,75×', '1×', '1,25×', '1,5×', '1,75×', '2×', '2,25×', '2,5×', '2,75×', '3×'])
    expect(items[1]).toHaveAttribute('aria-checked', 'true')
  })
})
