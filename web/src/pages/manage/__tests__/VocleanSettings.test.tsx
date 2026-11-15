// «Очищенные озвучки» in the server settings: the counts from
// /api/admin/voclean, «Обработать всю медиатеку» and «Перепроверить
// неподходящие» posted to /api/admin/voclean/backfill.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { VocleanStatus } from '../../../api/types'
import { VocleanSection } from '../VocleanSettings'

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
  it('shows the counts', async () => {
    stub(status(), () => status())
    render(<VocleanSection />)
    expect(await screen.findByText('Готово 120 · в очереди 34 · ошибки 2 · не подходят 5')).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'Обработать всю медиатеку' })).toHaveAttribute('aria-checked', 'false')
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
    expect(await screen.findByText('Готово 120 · в очереди 41 · ошибки 0 · не подходят 0')).toBeInTheDocument()
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ on: true, recheck: true })
    // nothing left to recheck
    expect(screen.getByRole('button', { name: 'Перепроверить' })).toBeDisabled()
  })
})
