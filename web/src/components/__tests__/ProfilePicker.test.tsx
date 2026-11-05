import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { ProfilePicker } from '../../pages/ProfilePicker'
import { session } from '../../state/session'

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('ProfilePicker', () => {
  beforeEach(() => session.reset())

  it('logs in a profile without a password with a single click', async () => {
    const masha = { id: 2, name: 'Маша', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: true, maxRating: '6+' }
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/api/profiles')) {
        return jsonResponse([
          { id: 1, name: 'Александр', avatar: 'grad-1', hasPassword: true },
          { id: 2, name: 'Маша', avatar: 'grad-3', hasPassword: false },
        ])
      }
      if (url.endsWith('/api/login') && init?.method === 'POST') return jsonResponse(masha)
      return jsonResponse({ error: 'нет' }, 404)
    })
    vi.stubGlobal('fetch', fetchMock)

    render(
      <MemoryRouter>
        <ProfilePicker />
      </MemoryRouter>,
    )
    expect(await screen.findByRole('heading', { name: 'Кто смотрит?' })).toBeInTheDocument()
    await userEvent.click(await screen.findByRole('button', { name: 'Маша' }))

    await waitFor(() => expect(session.get().user?.id).toBe(2))
    const loginCall = fetchMock.mock.calls.find(([u]) => String(u).endsWith('/api/login'))
    expect(loginCall).toBeDefined()
    expect(JSON.parse(String(loginCall![1]!.body))).toEqual({ userId: 2 })
    expect(screen.queryByLabelText('Пароль')).not.toBeInTheDocument()
    vi.unstubAllGlobals()
  })

  it('asks for a password for a protected profile', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) =>
        String(input).endsWith('/api/profiles')
          ? jsonResponse([{ id: 1, name: 'Александр', avatar: 'grad-1', hasPassword: true }])
          : jsonResponse({ error: 'Неверный пароль' }, 401),
      ),
    )
    render(
      <MemoryRouter>
        <ProfilePicker />
      </MemoryRouter>,
    )
    await userEvent.click(await screen.findByRole('button', { name: /Александр/ }))
    const field = await screen.findByLabelText('Пароль')
    await userEvent.type(field, 'wrong{Enter}')
    expect(await screen.findByRole('alert')).toHaveTextContent('Неверный пароль')
    expect(session.get().user).toBeNull()
    vi.unstubAllGlobals()
  })
})
