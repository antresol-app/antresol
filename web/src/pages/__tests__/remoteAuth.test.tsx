import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { ProfilePicker } from '../ProfilePicker'
import { Setup } from '../Setup'
import { Profiles } from '../manage/Profiles'
import { remotePasswordless, session } from '../../state/session'
import { setLanguagePref } from '../../i18n'

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const profiles = [
  { id: 1, name: 'Александр', avatar: 'grad-1', hasPassword: true },
  { id: 2, name: 'Маша', avatar: 'grad-3', hasPassword: false },
]

beforeEach(() => session.reset())
afterEach(() => vi.unstubAllGlobals())

describe('outside the home network', () => {
  it('bootstrap learns that the device is remote', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) =>
        String(input).endsWith('/api/setup') ? jsonResponse({ needsSetup: false, remote: true }) : jsonResponse({ error: 'Нужно войти' }, 401),
      ),
    )
    await session.bootstrap()
    expect(session.get()).toMatchObject({ status: 'signed-out', remote: true })
  })

  it('picker explains why a passwordless profile cannot sign in and does not try', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/setup')) return jsonResponse({ needsSetup: false, remote: true })
      if (url.endsWith('/api/profiles')) return jsonResponse(profiles)
      return jsonResponse({ error: 'Нужно войти' }, 401)
    })
    vi.stubGlobal('fetch', fetchMock)
    await session.bootstrap()
    render(
      <MemoryRouter>
        <ProfilePicker />
      </MemoryRouter>,
    )
    await userEvent.click(await screen.findByRole('button', { name: 'Маша' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(remotePasswordless())
    expect(fetchMock.mock.calls.some(([u]) => String(u).endsWith('/api/login'))).toBe(false)
    // the profile stays in the picker; a protected one still asks for its password
    await userEvent.click(screen.getByRole('button', { name: /Александр/ }))
    expect(await screen.findByLabelText('Пароль')).toBeInTheDocument()
  })

  it('picker shows the server refusal when it did not know it was remote', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/profiles')) return jsonResponse(profiles)
        if (url.endsWith('/api/login')) return jsonResponse({ error: remotePasswordless() }, 403)
        return jsonResponse({ error: 'нет' }, 404)
      }),
    )
    render(
      <MemoryRouter>
        <ProfilePicker />
      </MemoryRouter>,
    )
    await userEvent.click(await screen.findByRole('button', { name: 'Маша' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Для входа не из дома')
    expect(session.get().user).toBeNull()
  })

  it('first-run setup is not offered', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) =>
        String(input).endsWith('/api/setup') ? jsonResponse({ needsSetup: true, remote: true }) : jsonResponse({ error: 'Нужно войти' }, 401),
      ),
    )
    await session.bootstrap()
    render(
      <MemoryRouter>
        <Setup />
      </MemoryRouter>,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('только из домашней сети')
    expect(screen.queryByLabelText('Пароль')).not.toBeInTheDocument()
  })
})

describe('Setup', () => {
  it('asks for at least 6 characters', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL) => jsonResponse({}))
    vi.stubGlobal('fetch', fetchMock)
    render(
      <MemoryRouter>
        <Setup />
      </MemoryRouter>,
    )
    await userEvent.type(screen.getByLabelText('Ваше имя'), 'Папа')
    await userEvent.type(screen.getByLabelText('Пароль'), '12345')
    await userEvent.click(screen.getByRole('button', { name: 'Продолжить' }))
    expect(await screen.findByText('Не короче 6 символов')).toBeInTheDocument()
    // nothing is sent (the source offer, GET /api/about, is only read)
    expect(fetchMock.mock.calls.map(([u]) => String(u)).filter((u) => !u.endsWith('/api/about'))).toEqual([])
  })
})

describe('Profiles: «Убрать пароль»', () => {
  it('removes the password and says the profile becomes home-only', async () => {
    const ded = { id: 3, name: 'Дед', avatar: 'grad-2', hasPassword: true, isAdmin: false, isKid: false, maxRating: null }
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/api/users') && (!init?.method || init.method === 'GET')) return jsonResponse([ded])
      if (url.endsWith('/api/users/3') && init?.method === 'PATCH') return jsonResponse({ ...ded, hasPassword: false })
      return jsonResponse({ error: 'нет' }, 404)
    })
    vi.stubGlobal('fetch', fetchMock)
    render(
      <MemoryRouter>
        <Profiles />
      </MemoryRouter>,
    )
    await userEvent.click(await screen.findByRole('button', { name: /Изменить/ }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Убрать пароль' }))
    expect(within(dialog).getByText(/Пароль будет убран.*только из домашней сети/)).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(fetchMock.mock.calls.some(([u, i]) => String(u).endsWith('/api/users/3') && i?.method === 'PATCH')).toBe(true))
    const patch = fetchMock.mock.calls.find(([u, i]) => String(u).endsWith('/api/users/3') && i?.method === 'PATCH')!
    expect(JSON.parse(String(patch[1]!.body))).toMatchObject({ password: '' })
  })

  it('offers no removal for an administrator', async () => {
    const admin = { id: 1, name: 'Папа', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null }
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse([admin])),
    )
    render(
      <MemoryRouter>
        <Profiles />
      </MemoryRouter>,
    )
    await userEvent.click(await screen.findByRole('button', { name: /Изменить/ }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).queryByRole('button', { name: 'Убрать пароль' })).not.toBeInTheDocument()
  })
})

describe('in English', () => {
  it('the picker and its remote note follow the interface language', async () => {
    setLanguagePref('en')
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/setup')) return jsonResponse({ needsSetup: false, remote: true })
        if (url.endsWith('/api/profiles')) return jsonResponse(profiles)
        return jsonResponse({ error: 'Sign in' }, 401)
      }),
    )
    await session.bootstrap()
    render(
      <MemoryRouter>
        <ProfilePicker />
      </MemoryRouter>,
    )
    expect(await screen.findByRole('heading', { name: 'Who’s Watching?' })).toBeInTheDocument()
    await userEvent.click(await screen.findByRole('button', { name: 'Маша' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('To sign in away from home, the profile needs a password.')
  })
})
