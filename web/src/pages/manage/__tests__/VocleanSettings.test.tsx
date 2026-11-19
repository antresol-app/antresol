// «Очищенные озвучки» in the server settings («Обработка»): «Обработать всю
// медиатеку» and «Перепроверить неподходящие» posted to
// /api/admin/voclean/backfill; the counts are in «Задачи», a row leads there.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render as rtlRender, screen, waitFor } from '@testing-library/react'
import type { ReactElement } from 'react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import userEvent from '@testing-library/user-event'
import type { VocleanStatus } from '../../../api/types'
import { VocleanSection } from '../VocleanSettings'

function Where() {
  return <span data-testid="where">{useLocation().pathname}</span>
}
const render = (ui: ReactElement) =>
  rtlRender(
    <MemoryRouter initialEntries={['/manage/settings/processing']}>
      <Routes>
        <Route path="/manage/settings/processing" element={ui} />
        <Route path="*" element={<Where />} />
      </Routes>
    </MemoryRouter>,
  )

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const status = (patch: Partial<VocleanStatus> = {}): VocleanStatus => ({ done: 120, queued: 34, failed: 2, notEligible: 5, backfill: false, ...patch })

function stub(first: VocleanStatus, after: (body: { on: boolean; recheck?: boolean }) => VocleanStatus) {
  const calls: { method: string; path: string; body: unknown }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), 'http://x').pathname
      const method = init?.method ?? 'GET'
      const body = init?.body ? JSON.parse(String(init.body)) : undefined
      calls.push({ method, path, body })
      return json(method === 'GET' ? first : after(body))
    }),
  )
  return calls
}

afterEach(() => vi.unstubAllGlobals())

describe('cleaned voice-overs settings', () => {
  it('leads to «Задачи» for the counts, which it no longer shows', async () => {
    stub(status(), () => status())
    render(<VocleanSection />)
    expect(await screen.findByRole('switch', { name: 'Обработать всю медиатеку' })).toHaveAttribute('aria-checked', 'false')
    expect(screen.queryByText(/Готово 120/)).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: /Ход обработки/ }))
    expect(screen.getByTestId('where')).toHaveTextContent('/manage/tasks')
  })

  it('turns the whole library on', async () => {
    const calls = stub(status(), (b) => status({ backfill: b.on }))
    render(<VocleanSection />)
    await userEvent.click(await screen.findByRole('switch', { name: 'Обработать всю медиатеку' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'POST')).toBe(true))
    expect(calls.find((c) => c.method === 'POST')).toEqual({ method: 'POST', path: '/api/admin/voclean/backfill', body: { on: true, recheck: false } })
    await waitFor(() => expect(screen.getByRole('switch', { name: 'Обработать всю медиатеку' })).toHaveAttribute('aria-checked', 'true'))
  })

  it('rechecks the not eligible tracks, keeping the toggle', async () => {
    const calls = stub(status({ backfill: true }), () => status({ backfill: true, queued: 41, failed: 0, notEligible: 0 }))
    render(<VocleanSection />)
    await userEvent.click(await screen.findByRole('button', { name: 'Перепроверить' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ on: true, recheck: true }))
    // nothing left to recheck
    expect(screen.getByRole('button', { name: 'Перепроверить' })).toBeDisabled()
  })
})
