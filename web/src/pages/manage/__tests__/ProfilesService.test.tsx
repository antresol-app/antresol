// «Профили → Служебные»: the machines' profiles stand in a small group of
// their own, with the explanation and no action, apart from the people.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { User } from '../../../api/types'
import { Profiles } from '../Profiles'

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const user = (over: Partial<User>) => ({ id: 3, name: 'Миша', avatar: 'grad-2', hasPassword: false, isAdmin: false, isKid: false, maxRating: null, ...over }) as User

function show(service: User[] | 'fails') {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/users')) return json([user({})])
      if (url.endsWith('/api/screen-time/limited')) return json({ userIds: [], passwordless: [] })
      if (url.endsWith('/api/service-profiles')) return service === 'fails' ? json({ error: 'нет' }, 500) : json(service)
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

describe('Profiles: Служебные', () => {
  it('a service profile is in its own group, with the explanation and no action', async () => {
    show([user({ id: 9, name: 'playback-check', service: true })])
    const group = await screen.findByRole('region', { name: 'Служебные' })
    expect(within(group).getByText('playback-check')).toBeInTheDocument()
    expect(within(group).getByText(/Не видны семье/)).toBeInTheDocument()
    expect(within(group).queryByRole('button')).toBeNull()
    // the people's list does not have it
    const people = screen.getAllByRole('list')[0]!
    expect(within(people).getByText('Миша')).toBeInTheDocument()
    expect(within(people).queryByText('playback-check')).toBeNull()
  })

  it('no service profile: no group; a failed list: no group either', async () => {
    show([])
    await screen.findByText('Миша')
    expect(screen.queryByRole('region', { name: 'Служебные' })).toBeNull()
    vi.unstubAllGlobals()
    document.body.innerHTML = ''
    show('fails')
    await waitFor(() => expect(screen.getAllByText('Миша').length).toBeGreaterThan(0))
    expect(screen.queryByRole('region', { name: 'Служебные' })).toBeNull()
  })
})
