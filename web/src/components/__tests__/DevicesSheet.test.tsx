import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { DevicesSheet } from '../DevicesSheet'
import type { Device } from '../../api/types'

let devices: Device[] = []
let calls: string[] = []

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
  calls = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push(`${method} ${String(input)}`)
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
    expect(within(sheet).getByRole('heading', { name: /Маша/ })).toBeInTheDocument()
    expect(within(sheet).getAllByText('Это устройство')).toHaveLength(1)
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

  it('groups an admin’s list by profile, this device’s profile first', async () => {
    const grandpa: Device = { ...mac, id: 20, name: 'iPad', userId: 2, userName: 'Дед', userAvatar: 'grad-5' }
    devices = [grandpa, phone, mac]
    render(<DevicesSheet onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Устройства' })
    await within(sheet).findByText('iPad')
    expect(within(sheet).getAllByRole('heading', { level: 3 }).map((h) => h.textContent)).toEqual(['ММаша', 'ДДед'])
    const section = within(sheet).getByRole('heading', { name: /Дед/ }).closest('section') as HTMLElement
    expect(within(section).getByText('iPad')).toBeInTheDocument()
    expect(within(section).queryByText('MacBook')).not.toBeInTheDocument()
  })

  it('shows the empty state', async () => {
    devices = []
    render(<DevicesSheet onClose={() => {}} />)
    const sheet = await screen.findByRole('dialog', { name: 'Устройства' })
    expect(await within(sheet).findByText('Приложение Antresol ещё не входило в этот профиль')).toBeInTheDocument()
  })
})
