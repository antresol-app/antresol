import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MarkButton } from '../MarkButton'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const mark = (state: string) => ({ state, score: null, origin: 'antresol', updatedAt: '2026-11-13T10:00:00Z' })

let calls: { method: string; url: string; body?: unknown }[]
function stub(initial: unknown) {
  calls = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      const body = init?.body ? JSON.parse(String(init.body)) : undefined
      calls.push({ method, url: String(input), body })
      if (method === 'PUT') return json({ mark: mark(body.state) })
      if (method === 'DELETE') return new Response(null, { status: 204 })
      return json({ mark: initial })
    }),
  )
}

describe('MarkButton', () => {
  beforeEach(() => stub(null))
  afterEach(() => vi.unstubAllGlobals())

  it('choosing «Смотрю» sends the state and shows it on the button', async () => {
    render(<MarkButton kind="series" tmdbId={1399} />)
    await waitFor(() => expect(calls.some((c) => c.method === 'GET')).toBe(true))
    await userEvent.click(screen.getByRole('button', { name: /Отметка/ }))
    // five states and «Убрать отметку» (not for a title without a mark)
    expect(screen.getAllByRole('menuitemradio').map((m) => m.textContent)).toEqual(['Хочу посмотреть', 'Смотрю', 'Просмотрено', 'Отложено', 'Брошено'])
    expect(screen.queryByRole('menuitem', { name: 'Убрать отметку' })).toBeNull()
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Смотрю' }))
    expect(calls.find((c) => c.method === 'PUT')).toEqual({ method: 'PUT', url: '/api/marks/series/1399', body: { state: 'watching' } })
    await waitFor(() => expect(screen.getByRole('button', { name: /Отметка/ }).textContent).toContain('Смотрю'))
  })

  it('a movie has two states', async () => {
    render(<MarkButton kind="movie" tmdbId={603} />)
    await userEvent.click(screen.getByRole('button', { name: /Отметка/ }))
    expect(screen.getAllByRole('menuitemradio').map((m) => m.textContent)).toEqual(['Хочу посмотреть', 'Просмотрено'])
  })

  it('«Убрать отметку» sends DELETE and the label goes back', async () => {
    stub(mark('watching'))
    render(<MarkButton kind="series" tmdbId={1399} size="small" />)
    await waitFor(() => expect(screen.getByRole('button', { name: /Отметка/ }).textContent).toContain('Смотрю'))
    await userEvent.click(screen.getByRole('button', { name: /Отметка/ }))
    expect(screen.getByRole('menuitemradio', { name: 'Смотрю' }).getAttribute('aria-checked')).toBe('true')
    await userEvent.click(screen.getByRole('menuitem', { name: 'Убрать отметку' }))
    expect(calls.find((c) => c.method === 'DELETE')).toEqual({ method: 'DELETE', url: '/api/marks/series/1399', body: undefined })
    await waitFor(() => expect(screen.getByRole('button', { name: /Отметка/ }).textContent).toContain('Отметить'))
  })
})
