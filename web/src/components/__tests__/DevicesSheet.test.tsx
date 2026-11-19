import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { DevicesSheet } from '../DevicesSheet'
import type { Device, ManagedAccount, User } from '../../api/types'
import { session } from '../../state/session'

let devices: Device[] = []
let managed: ManagedAccount[] = []
let calls: string[] = []
let bodies: Record<string, unknown> = {}

const phone: Device = {
  id: 7,
  name: 'iPhone Маши',
  model: 'iPhone17,1',
  version: '1.0',
  createdAt: '2026-10-20T10:00:00Z',
  lastSeen: '2026-10-25T10:00:00Z',
  current: true,
  userId: 1,
  userName: 'Маша',
  userAvatar: 'grad-2',
}
const mac: Device = { ...phone, id: 9, name: 'MacBook', model: 'Mac15,3', version: '1.1', current: false }

beforeEach(() => {
  devices = [phone, mac]
  managed = []
  calls = []
  bodies = {}
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      const url = String(input)
      calls.push(`${method} ${url}`)
      if (init?.body) bodies[`${method} ${url}`] = JSON.parse(String(init.body))
      if (url.endsWith('/history')) {
        const id = Number(url.split('/').at(-2))
        const on = (bodies[`${method} ${url}`] as { separate: boolean }).separate
        const history = on ? { id: 50, name: 'Гостиная' } : null
        devices = devices.map((d) => (d.id === id ? { ...d, history } : d))
        managed = on ? [{ id: 50, name: 'Гостиная', avatar: 'grad-1', devices: [{ id, name: 'MacBook', model: '' }] }] : []
        return new Response(JSON.stringify({ id, history }), { status: 200 })
      }
      if (url.startsWith('/api/me/managed') && url.endsWith('/app-password')) {
        if (method === 'POST') return new Response(JSON.stringify({ password: 'abcd-efgh-ijkl-mnop' }), { status: 200 })
        return new Response(JSON.stringify({ exists: false, createdAt: null }), { status: 200 })
      }
      if (url === '/api/managed-accounts') return new Response(JSON.stringify({ items: managed }), { status: 200 })
      if (url.startsWith('/api/me/managed')) {
        if (method === 'POST' && url === '/api/me/managed') {
          const a = { id: 60, name: (bodies[`POST ${url}`] as { name: string }).name, avatar: 'grad-1', devices: [] }
          managed = [...managed, a]
          return new Response(JSON.stringify(a), { status: 201 })
        }
        if (method === 'DELETE' || method === 'POST') {
          const id = Number(url.split('/')[4])
          managed = managed.filter((m) => m.id !== id)
          return new Response(null, { status: 204 })
        }
        return new Response(JSON.stringify({ items: managed }), { status: 200 })
      }
      if (method === 'DELETE') {
        const id = Number(String(input).split('/').pop())
        devices = devices.filter((d) => d.id !== id)
        return new Response(null, { status: 204 })
      }
      return new Response(JSON.stringify({ items: devices }), { status: 200 })
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

describe('DevicesSheet', () => {
  it('lists devices and marks the current one', async () => {
    render(<DevicesSheet onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Устройства' })
    expect(await within(sheet).findByText('iPhone Маши')).toBeInTheDocument()
    expect(within(sheet).getByText('MacBook')).toBeInTheDocument()
    expect(within(sheet).getByText('Antresol 1.0 · iPhone17,1 · Последний вход: 25 октября')).toBeInTheDocument()
    // the profile's own list: no heading of whose it is
    expect(within(sheet).queryByRole('heading', { name: /Маша/ })).not.toBeInTheDocument()
    expect(within(sheet).getAllByText('Это устройство')).toHaveLength(1)
    expect(calls).toContain('GET /api/me/devices')
    expect(calls.some((c) => c.includes('/api/admin/'))).toBe(false)
  })

  it('signs a device out after confirmation', async () => {
    render(<DevicesSheet onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Устройства' })
    await within(sheet).findByText('MacBook')
    const row = within(sheet).getByText('MacBook').closest('li') as HTMLElement
    fireEvent.click(within(row).getByRole('button', { name: 'Выйти на устройстве: MacBook' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Выйти на «MacBook»?' })
    fireEvent.click(within(confirm).getByRole('button', { name: 'Выйти' }))
    await waitFor(() => expect(within(sheet).queryByText('MacBook')).not.toBeInTheDocument())
    expect(calls).toContain('DELETE /api/me/devices/9')
    expect(within(sheet).getByText('iPhone Маши')).toBeInTheDocument()
  })

  it('lists browsers too and signs one out', async () => {
    devices = [phone, { id: 11, kind: 'web', name: '', model: '', version: '', createdAt: phone.createdAt, lastSeen: phone.lastSeen, current: false }]
    render(<DevicesSheet onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Устройства' })
    const row = (await within(sheet).findByText('Браузер')).closest('li') as HTMLElement
    expect(within(row).getByText('Веб-версия Antresol · Последний вход: 25 октября')).toBeInTheDocument()
    fireEvent.click(within(row).getByRole('button', { name: 'Выйти на устройстве: Браузер' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Выйти на «Браузер»?' })
    fireEvent.click(within(confirm).getByRole('button', { name: 'Выйти' }))
    await waitFor(() => expect(within(sheet).queryByText('Браузер')).not.toBeInTheDocument())
    expect(calls).toContain('DELETE /api/me/devices/11')
  })

  it('names a browser by itself and its system', async () => {
    devices = [{ ...phone, id: 12, kind: 'web', name: 'Safari', model: 'iPhone', version: '' }]
    render(<DevicesSheet onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Устройства' })
    expect(await within(sheet).findByText('Safari · iPhone')).toBeInTheDocument()
  })

  it('the server’s list (all): every profile’s, by profile, this device’s profile first, on the admin endpoints', async () => {
    const grandpa: Device = { ...mac, id: 20, name: 'iPad', userId: 2, userName: 'Дед', userAvatar: 'grad-5' }
    devices = [grandpa, phone, mac]
    render(<DevicesSheet all onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Устройства всех профилей' })
    await within(sheet).findByText('iPad')
    expect(within(sheet).getAllByRole('heading', { level: 3 }).map((h) => h.textContent)).toEqual(['ММаша', 'ДДед', 'Отдельные истории'])
    const section = within(sheet).getByRole('heading', { name: /Дед/ }).closest('section') as HTMLElement
    expect(within(section).getByText('iPad')).toBeInTheDocument()
    expect(within(section).queryByText('MacBook')).not.toBeInTheDocument()
    expect(calls).toContain('GET /api/admin/devices')
    expect(calls).toContain('GET /api/managed-accounts')
    // signing out and switching go to the admin endpoints too
    fireEvent.click(within(section).getByRole('button', { name: 'Выйти на устройстве: iPad' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Выйти на «iPad»?' })
    fireEvent.click(within(confirm).getByRole('button', { name: 'Выйти' }))
    await waitFor(() => expect(calls).toContain('DELETE /api/admin/devices/20'))
  })

  it('switches a device to a separate history and back, moving it into the profile', async () => {
    render(<DevicesSheet onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Устройства' })
    await within(sheet).findByText('MacBook')
    // the devices' row (the account's row below names the device too)
    const row = () => within(sheet).getAllByText('MacBook')[0]!.closest('li') as HTMLElement
    const toggle = () => within(row()).getByRole('switch', { name: 'Отдельная история на этом устройстве: MacBook' })
    expect(toggle()).toHaveAttribute('aria-checked', 'false')
    fireEvent.click(toggle())
    await waitFor(() => expect(toggle()).toHaveAttribute('aria-checked', 'true'))
    expect(bodies['PUT /api/me/devices/9/history']).toEqual({ separate: true })
    expect(within(row()).getByText(/История: «Гостиная»/)).toBeInTheDocument()
    // the account is listed under «Отдельные истории»
    const section = within(sheet).getByRole('heading', { name: 'Отдельные истории' }).closest('section') as HTMLElement
    expect(within(section).getByText('Гостиная')).toBeInTheDocument()

    fireEvent.click(toggle())
    const ask = await screen.findByRole('alertdialog', { name: 'Вернуть «MacBook» к вашей истории?' })
    fireEvent.click(within(ask).getByRole('button', { name: 'Перенести в мой профиль' }))
    await waitFor(() => expect(toggle()).toHaveAttribute('aria-checked', 'false'))
    expect(bodies['PUT /api/me/devices/9/history']).toEqual({ separate: false, merge: true })
  })

  it('keeps the history apart with «Оставить»', async () => {
    devices = [phone, { ...mac, history: { id: 50, name: 'Гостиная' } }]
    render(<DevicesSheet onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Устройства' })
    const toggle = await within(sheet).findByRole('switch', { name: 'Отдельная история на этом устройстве: MacBook' })
    fireEvent.click(toggle)
    const ask = await screen.findByRole('alertdialog', { name: 'Вернуть «MacBook» к вашей истории?' })
    fireEvent.click(within(ask).getByRole('button', { name: 'Оставить' }))
    await waitFor(() => expect(bodies['PUT /api/me/devices/9/history']).toEqual({ separate: false, merge: false }))
  })

  it('creates a separate history by hand and deletes it', async () => {
    render(<DevicesSheet onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Устройства' })
    await within(sheet).findByText('MacBook')
    fireEvent.change(within(sheet).getByRole('textbox', { name: 'Название' }), { target: { value: 'Кухня' } })
    fireEvent.click(within(sheet).getByRole('button', { name: 'Создать' }))
    expect(await within(sheet).findByText('Кухня')).toBeInTheDocument()
    expect(bodies['POST /api/me/managed']).toEqual({ name: 'Кухня' })
    fireEvent.click(within(sheet).getByRole('button', { name: 'Действия: Кухня' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Удалить историю: Кухня' }))
    const ask = await screen.findByRole('alertdialog', { name: 'Удалить «Кухня»?' })
    fireEvent.click(within(ask).getByRole('button', { name: 'Удалить' }))
    await waitFor(() => expect(within(sheet).queryByText('Кухня')).not.toBeInTheDocument())
    expect(calls).toContain('DELETE /api/me/managed/60')
  })

  it('makes an app password for a separate history', async () => {
    managed = [{ id: 50, name: 'Гостиная', avatar: 'grad-1', devices: [] }]
    render(<DevicesSheet onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Устройства' })
    fireEvent.click(await within(sheet).findByRole('button', { name: 'Действия: Гостиная' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Пароль для приложений' }))
    const pw = await screen.findByRole('dialog', { name: 'Пароль для приложений: Гостиная' })
    expect(within(pw).getByText(/запишется в «Гостиная»/)).toBeInTheDocument()
    fireEvent.click(await within(pw).findByRole('button', { name: 'Создать пароль' }))
    expect(await within(pw).findByDisplayValue('abcd-efgh-ijkl-mnop')).toBeInTheDocument()
    expect(calls).toContain('GET /api/me/managed/50/app-password')
    expect(calls).toContain('POST /api/me/managed/50/app-password')
    expect(calls).not.toContain('POST /api/me/app-password')
  })

  it('leaves the switch and the accounts to an administrator for a kid', async () => {
    devices = [phone, { ...mac, history: { id: 50, name: 'Гостиная' } }]
    const kid: User = { id: 1, name: 'Маша', avatar: 'grad-2', hasPassword: false, isAdmin: false, isKid: true, maxRating: '6+' }
    const base = globalThis.fetch
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) =>
      String(input).endsWith('/api/login') ? new Response(JSON.stringify(kid), { status: 200 }) : base(input, init),
    ))
    await session.login(kid.id)
    try {
      render(<DevicesSheet onClose={() => {}} />)
      const sheet = await screen.findByRole('dialog', { name: 'Устройства' })
      await within(sheet).findByText('MacBook')
      // what is on is still shown, nothing of it can be changed
      expect(within(sheet).getByText(/История: «Гостиная»/)).toBeInTheDocument()
      expect(within(sheet).queryByRole('switch')).not.toBeInTheDocument()
      expect(within(sheet).queryByRole('heading', { name: 'Отдельные истории' })).not.toBeInTheDocument()
      expect(calls.filter((c) => c.includes('/api/me/managed'))).toEqual([])
      // signing a device out stays the kid's own
      expect(within(sheet).getAllByRole('button', { name: /Выйти на устройстве/ })).toHaveLength(2)
    } finally {
      session.reset()
    }
  })

  it('shows the empty state', async () => {
    devices = []
    render(<DevicesSheet onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Устройства' })
    expect(await within(sheet).findByText('Приложение Antresol ещё не входило в этот профиль')).toBeInTheDocument()
  })
})
