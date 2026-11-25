// «Сейчас в эфире» on Главная reads the brief channel list (the full one costs a query per channel).
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'

const getChannels = vi.fn()
vi.mock('../../../api/channels', async (orig) => ({ ...(await orig<typeof import('../../../api/channels')>()), getChannels: (...a: unknown[]) => getChannels(...a) }))

const { OnAirShelf } = await import('../OnAirShelf')

describe('OnAirShelf', () => {
  it('asks for the brief list and shows the channels that have a programme on', async () => {
    const now = { itemId: 5, title: 'Новости', start: '2026-11-24T10:00:00Z', end: '2026-11-24T11:00:00Z' }
    getChannels.mockResolvedValue({ channels: [{ id: 1, number: 1, name: 'Первый', now }, { id: 2, number: 2, name: 'Пустой' }] })
    render(
      <MemoryRouter>
        <OnAirShelf />
      </MemoryRouter>,
    )
    await waitFor(() => expect(screen.getByText(/Новости/)).toBeTruthy())
    expect(getChannels).toHaveBeenCalledWith(expect.any(AbortSignal), false, true)
    expect(screen.queryByText(/Пустой/)).toBeNull()
  })
})
