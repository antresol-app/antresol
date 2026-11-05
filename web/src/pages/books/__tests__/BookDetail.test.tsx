import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { BookDetail as BookDetailData } from '../../../api/types'
import { setLanguagePref } from '../../../i18n'
import { BookDetail } from '../BookDetail'

function detail(over: Partial<BookDetailData> = {}): BookDetailData {
  return {
    card: {
      id: 1,
      kind: 'book',
      title: 'Пробная книга',
      subtitle: 'Иван Тестов',
      year: 2020,
      poster: '/api/images/local/1/poster',
      backdrop: '',
      logo: '',
      progress: 0,
      onServer: true,
    },
    kind: 'book',
    authors: ['Иван Тестов'],
    narrator: '',
    series: { name: 'Классика', index: 2 },
    description: 'Описание книги.',
    year: 2020,
    lang: 'ru',
    files: [
      { id: 11, format: 'EPUB', size: 2_516_582, durationMs: 0 },
      { id: 12, format: 'FB2', size: 1_153_434, durationMs: 0 },
    ],
    seriesId: 0,
    nextId: 0,
    readFileId: 11,
    progress: null,
    want: false,
    durationMs: 0,
    chapters: [],
    volumes: [],
    direction: '',
    ...over,
  }
}

let current: BookDetailData
let kindleAnswer: () => Response = () => new Response(null, { status: 204 })
const calls: string[] = []

beforeEach(() => {
  current = detail()
  calls.length = 0
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      calls.push(`${method} ${url}`)
      if (method === 'GET' && url === '/api/books/1') return new Response(JSON.stringify(current), { status: 200 })
      if (url === '/api/watchlist/1') return new Response(null, { status: 204 })
      if (url === '/api/books/1/kindle') return kindleAnswer()
      return new Response('{}', { status: 404 })
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

function renderDetail() {
  return render(
    <MemoryRouter initialEntries={['/books/1']}>
      <Routes>
        <Route path="/books/:id" element={<BookDetail />} />
        <Route path="/read/:id" element={<p>читалка</p>} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('BookDetail', () => {
  it('shows the book', async () => {
    renderDetail()
    expect(await screen.findByRole('heading', { level: 1, name: 'Пробная книга' })).toBeInTheDocument()
    expect(screen.getByText('Иван Тестов')).toBeInTheDocument()
    expect(screen.getByText('Классика · книга 2')).toBeInTheDocument()
    expect(screen.getByText('EPUB · 2,4 МБ · русский')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Читать' })).toHaveAttribute('href', '/read/1')
  })

  it('speaks English when English is picked', async () => {
    setLanguagePref('en')
    current = detail({ progress: { fraction: 0.342, locator: 'epubcfi(/6/4!/4/2)', positionMs: 0, durationMs: 0, finished: false } })
    renderDetail()
    expect(await screen.findByRole('link', { name: 'Resume · 34%' })).toHaveAttribute('href', '/read/1')
    expect(screen.getByText('Классика · Book 2')).toBeInTheDocument()
    expect(screen.getByText('EPUB · 2.4 MB · Russian')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Want to Read' })).toBeInTheDocument()
  })

  it('shows Продолжить · 34 % with progress', async () => {
    current = detail({ progress: { fraction: 0.342, locator: 'epubcfi(/6/4!/4/2)', positionMs: 0, durationMs: 0, finished: false } })
    renderDetail()
    expect(await screen.findByRole('link', { name: 'Продолжить · 34 %' })).toHaveAttribute('href', '/read/1')
    expect(screen.queryByRole('link', { name: 'Читать' })).not.toBeInTheDocument()
  })

  it('toggles want list', async () => {
    renderDetail()
    fireEvent.click(await screen.findByRole('button', { name: 'Хочу прочитать' }))
    expect(await screen.findByRole('button', { name: 'В списке' })).toHaveAttribute('aria-pressed', 'true')
    expect(calls).toContain('POST /api/watchlist/1')
    fireEvent.click(screen.getByRole('button', { name: 'В списке' }))
    expect(await screen.findByRole('button', { name: 'Хочу прочитать' })).toHaveAttribute('aria-pressed', 'false')
    expect(calls).toContain('DELETE /api/watchlist/1')
  })

  it('formats menu lists EPUB and FB2', async () => {
    renderDetail()
    fireEvent.click(await screen.findByRole('button', { name: 'На это устройство' }))
    const menu = screen.getByRole('menu', { name: 'На это устройство' })
    // «Сохранить офлайн» first (disabled here: no Cache Storage in jsdom), then the files
    const [save, ...items] = within(menu).getAllByRole('menuitem')
    expect(save).toHaveTextContent('Сохранить офлайн')
    expect(items.map((i) => i.textContent)).toEqual(['EPUB2,4 МБ', 'FB21,1 МБ'])
    expect(items[0]).toHaveAttribute('href', '/api/download/11')
    expect(items[1]).toHaveAttribute('href', '/api/download/12')
  })
})

describe('Отправить на Kindle', () => {
  it('has no button when the server says it would not work', async () => {
    renderDetail()
    await screen.findByRole('heading', { level: 1 })
    expect(screen.queryByRole('button', { name: 'Отправить на Kindle' })).toBeNull()
  })

  it('turns white, says Sent, then settles on On Kindle', async () => {
    current = detail({ canSendToKindle: true })
    kindleAnswer = () => new Response(null, { status: 204 })
    renderDetail()
    fireEvent.click(await screen.findByRole('button', { name: 'Отправить на Kindle' }))
    const sent = await screen.findByRole('button', { name: 'Отправлено' })
    expect(sent.className).toMatch(/kindleDone/)
    expect(screen.getByRole('status')).toHaveTextContent('Отправлено')
    expect(calls).toContain('POST /api/books/1/kindle')
    expect(await screen.findByRole('button', { name: 'На Kindle' }, { timeout: 4000 })).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('')
  })

  it('takes a book still on its way as sent', async () => {
    current = detail({ canSendToKindle: true })
    kindleAnswer = () => new Response('{"status":"sending"}', { status: 202 })
    renderDetail()
    fireEvent.click(await screen.findByRole('button', { name: 'Отправить на Kindle' }))
    expect(await screen.findByRole('button', { name: 'Отправлено' })).toBeInTheDocument()
  })

  it('a book sent before asks before going again', async () => {
    current = detail({ canSendToKindle: true, sentToKindle: true })
    renderDetail()
    fireEvent.click(await screen.findByRole('button', { name: 'На Kindle' }))
    expect(await screen.findByText('Эта книга уже отправлена. Отправить ещё раз?')).toBeInTheDocument()
    expect(calls).not.toContain('POST /api/books/1/kindle')
    fireEvent.click(screen.getByRole('menuitem', { name: 'Отмена' }))
    expect(calls).not.toContain('POST /api/books/1/kindle')
    fireEvent.click(screen.getByRole('button', { name: 'На Kindle' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Отправить ещё раз' }))
    expect(await screen.findByRole('button', { name: 'Отправлено' })).toBeInTheDocument()
    expect(calls.filter((c) => c === 'POST /api/books/1/kindle')).toHaveLength(1)
  })

  it('a failed resend stays On Kindle and says why', async () => {
    current = detail({ canSendToKindle: true, sentToKindle: true })
    kindleAnswer = () => new Response('{"error":"Нет связи с почтой"}', { status: 502 })
    renderDetail()
    fireEvent.click(await screen.findByRole('button', { name: 'На Kindle' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Отправить ещё раз' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Нет связи с почтой')
    expect(screen.getByRole('button', { name: 'На Kindle' })).toBeInTheDocument()
  })

  it('shows why it failed', async () => {
    current = detail({ canSendToKindle: true })
    kindleAnswer = () => new Response('{"error":"Почтовый сервер не принял логин"}', { status: 502 })
    renderDetail()
    fireEvent.click(await screen.findByRole('button', { name: 'Отправить на Kindle' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Почтовый сервер не принял логин')
    expect(screen.getByRole('button', { name: 'Отправить на Kindle' })).toBeInTheDocument()
  })
})
