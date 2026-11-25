// «Профили → Изменить»: an administrator's password is 10 characters or more,
// and a profile that becomes one sets a new password (its old one may be as
// short as a non-admin's).
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { User } from '../../../api/types'
import { Profiles } from '../Profiles'

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const user = (over: Partial<User> = {}) => ({ id: 3, name: 'Мама', avatar: 'grad-2', hasPassword: true, isAdmin: false, isKid: false, maxRating: null, ...over }) as User

function show(profile: User) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/api/users') && (init?.method ?? 'GET') === 'GET') return json([profile])
      if (url.endsWith('/api/screen-time/limited')) return json({ userIds: [], passwordless: [] })
      return json({ error: 'нет' }, 404)
    }),
  )
  render(
    <MemoryRouter>
      <Profiles />
    </MemoryRouter>,
  )
}

afterEach(() => vi.unstubAllGlobals())

async function openForm() {
  await userEvent.click((await screen.findAllByRole('button', { name: /Изменить/ }))[0]!)
  return screen.findByRole('dialog', { name: 'Изменить профиль' })
}

describe('Profiles: an administrator’s password', () => {
  it('promoting a profile asks for a new password of 10 characters', async () => {
    show(user())
    await openForm()
    const save = screen.getByRole('button', { name: 'Сохранить' })
    expect(save).toBeEnabled()
    await userEvent.click(screen.getByRole('switch', { name: 'Администратор' }))
    expect(save).toBeDisabled() // its old password is not enough
    await userEvent.type(screen.getByLabelText('Новый пароль'), '123456789')
    expect(await screen.findByText('Не короче 10 символов')).toBeInTheDocument()
    expect(save).toBeDisabled()
    await userEvent.type(screen.getByLabelText('Новый пароль'), '0')
    expect(save).toBeEnabled()
  })

  it('an administrator keeps the password it has when it is saved', async () => {
    show(user({ isAdmin: true }))
    await openForm()
    expect(screen.getByRole('button', { name: 'Сохранить' })).toBeEnabled()
  })
})
