import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { AppPasswordSheet } from '../AppPasswordSheet'

let stored: string | null = null
let calls: string[] = []
let failGet = false

beforeEach(() => {
  stored = null
  calls = []
  failGet = false
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push(`${method} ${String(input)}`)
      if (method === 'POST') {
        stored = 'abcd-efgh-ijkl-mnop'
        return new Response(JSON.stringify({ password: stored }), { status: 200 })
      }
      if (method === 'DELETE') {
        stored = null
        return new Response(null, { status: 204 })
      }
      if (failGet) return new Response(JSON.stringify({ error: 'Сервер недоступен' }), { status: 503 })
      // GET never carries the password
      return new Response(JSON.stringify({ exists: stored !== null, createdAt: stored ? '2026-10-20T10:00:00Z' : null }), { status: 200 })
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

describe('AppPasswordSheet', () => {
  it('creates and shows password with copy button', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    render(<AppPasswordSheet userName="Александр" onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Пароль для приложений' })
    expect(sheet).toHaveTextContent(`сервер — ${location.origin}`)
    expect(sheet).toHaveTextContent('имя — Александр')
    expect(sheet).toHaveTextContent(`Каталог книг: ${location.origin}/opds`)
    fireEvent.click(await within(sheet).findByRole('button', { name: 'Создать пароль' }))
    const field = await within(sheet).findByDisplayValue('abcd-efgh-ijkl-mnop')
    expect(field).toHaveAttribute('readonly')
    fireEvent.click(within(sheet).getByRole('button', { name: 'Скопировать пароль' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('abcd-efgh-ijkl-mnop'))
    expect(await within(sheet).findByText('Скопировано')).toBeInTheDocument()
    expect(calls).toContain('POST /api/me/app-password')
  })

  it('does not reveal an existing password', async () => {
    stored = 'old-secret'
    render(<AppPasswordSheet userName="Александр" onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Пароль для приложений' })
    expect(await within(sheet).findByRole('button', { name: 'Создать новый' })).toBeInTheDocument()
    expect(within(sheet).queryByDisplayValue('old-secret')).not.toBeInTheDocument()
    expect(sheet).toHaveTextContent('Создан 20 октября 2026')
  })

  it('a failed load offers Повторить', async () => {
    failGet = true
    render(<AppPasswordSheet userName="Александр" onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Пароль для приложений' })
    expect(await within(sheet).findByText('Сервер недоступен')).toBeInTheDocument()
    failGet = false
    fireEvent.click(within(sheet).getByRole('button', { name: 'Повторить' }))
    expect(await within(sheet).findByRole('button', { name: 'Создать пароль' })).toBeInTheDocument()
  })

  it('revoke asks for confirmation', async () => {
    stored = 'old-secret'
    render(<AppPasswordSheet userName="Александр" onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Пароль для приложений' })
    fireEvent.click(await within(sheet).findByRole('button', { name: 'Отозвать' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Отозвать пароль?' })
    expect(confirm).toHaveTextContent('Приложения с этим паролем перестанут подключаться.')
    expect(calls).not.toContain('DELETE /api/me/app-password')
    fireEvent.click(within(confirm).getByRole('button', { name: 'Отозвать' }))
    await waitFor(() => expect(calls).toContain('DELETE /api/me/app-password'))
    expect(await within(sheet).findByRole('button', { name: 'Создать пароль' })).toBeInTheDocument()
  })
})
