import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { ProfilePicker } from '../ProfilePicker'
import { session } from '../../state/session'
import { request } from '../../api/client'

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const profiles = [
  { id: 1, name: 'Александр', avatar: 'grad-1', hasPassword: true },
  { id: 2, name: 'Маша', avatar: 'grad-3', hasPassword: true },
]
const alex = { id: 1, name: 'Александр', avatar: 'grad-1', isAdmin: true, isKid: false }

let calls: string[] = []
let meStatus = 200
beforeEach(() => {
  session.reset()
  calls = []
  meStatus = 200
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url.endsWith('/api/me')) return meStatus === 200 ? json(alex) : json({ error: 'Нужно войти' }, 401)
      if (url.endsWith('/api/profiles')) return json(profiles)
      if (url.endsWith('/api/addon-thing')) return json({ error: 'нет токена' }, 401)
      return json({}, 404)
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

function openPicker() {
  render(
    <MemoryRouter initialEntries={['/library', { pathname: '/profiles', state: { from: '/library', switching: true } }]} initialIndex={1}>
      <Routes>
        <Route path="/profiles" element={<ProfilePicker />} />
        <Route path="/library" element={<p>Медиатека</p>} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('«Сменить профиль»', () => {
  it('cancel returns to the page still signed in, without signing out', async () => {
    await session.bootstrap()
    openPicker()
    await userEvent.click(await screen.findByRole('button', { name: 'Отмена' }))
    expect(await screen.findByText('Медиатека')).toBeInTheDocument()
    expect(session.get()).toMatchObject({ status: 'signed-in', user: { id: 1 } })
    expect(calls.some((c) => c.includes('/api/logout'))).toBe(false)
  })

  it('Escape returns too, and the current profile’s tile is a no-op switch', async () => {
    await session.bootstrap()
    openPicker()
    await screen.findByRole('button', { name: /Маша/ })
    await userEvent.keyboard('{Escape}')
    expect(await screen.findByText('Медиатека')).toBeInTheDocument()
    expect(session.get().status).toBe('signed-in')
  })

  it('the password step is a form a password manager understands', async () => {
    await session.bootstrap()
    openPicker()
    await userEvent.click(await screen.findByRole('button', { name: /Маша/ }))
    const pw = await screen.findByLabelText('Пароль')
    const form = pw.closest('form')!
    expect(pw).toHaveAttribute('autocomplete', 'current-password')
    const user = form.querySelector('input[autocomplete="username"]') as HTMLInputElement
    expect(user.value).toBe('Маша')
    expect(form.querySelector('button[type="submit"]')).not.toBeNull()
    // Back from the password step keeps the session
    await userEvent.click(screen.getByRole('button', { name: 'Назад' }))
    expect(session.get().status).toBe('signed-in')
  })
})

describe('a 401 of one request', () => {
  it('does not sign the device out while /api/me still answers', async () => {
    await session.bootstrap()
    await request('GET', '/api/addon-thing').catch(() => {})
    await waitFor(() => expect(calls.filter((c) => c.endsWith('/api/me')).length).toBe(2))
    await new Promise((r) => setTimeout(r, 0))
    expect(session.get().status).toBe('signed-in')
  })

  it('signs out when /api/me agrees the session is gone', async () => {
    await session.bootstrap()
    meStatus = 401
    await request('GET', '/api/addon-thing').catch(() => {})
    await waitFor(() => expect(session.get().status).toBe('signed-out'))
  })
})
