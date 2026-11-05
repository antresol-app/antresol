// On a phone a profile's row keeps one «⋯» with its actions: five round
// buttons squeezed «Детский · до 12+» into three lines.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { User } from '../../../api/types'
import { Profiles } from '../Profiles'
import { setViewport } from '../../../test/setup'

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const u = (id: number, name: string, isKid: boolean) => ({ id, name, avatar: 'grad-1', hasPassword: true, isAdmin: !isKid, isKid, maxRating: isKid ? '12+' : null }) as unknown as User

function show() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/users')) return json([u(1, 'Саша', false), u(3, 'Миша', true)])
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

afterEach(() => {
  vi.unstubAllGlobals()
  setViewport(1440)
})

describe('Profiles on a phone', () => {
  it('a row has one «⋯» with every action, «Удалить…» last', async () => {
    setViewport(390)
    show()
    const more = await screen.findByRole('button', { name: 'Действия: Миша' })
    expect(screen.queryByRole('button', { name: /Экранное время/ })).toBeNull()
    await userEvent.click(more)
    const items = within(screen.getByRole('menu')).getAllByRole('menuitem')
    expect(items.map((i) => i.textContent)).toEqual(['Изменить', 'Звук и субтитры', 'Уведомления на почту', 'Экранное время', 'Удалить…'])
  })

  it('a wide screen keeps the buttons in the row', async () => {
    show()
    await screen.findByRole('button', { name: 'Изменить' }, {}).catch(() => null)
    expect((await screen.findAllByRole('button', { name: /Изменить/ })).length).toBeGreaterThan(0)
    expect(screen.queryByRole('button', { name: 'Действия: Миша' })).toBeNull()
  })
})
