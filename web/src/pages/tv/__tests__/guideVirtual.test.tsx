// «Телегид» with a big list: only the rows near the screen are in the DOM
// (the grid says how many there are), and ↓ still walks down the rows.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { Guide } from '../Guide'
import { ALL } from '../model'

const N = 2000
const window6h = () => {
  const now = Date.now()
  const at = (m: number) => new Date(now + m * 60_000).toISOString()
  return {
    from: at(-60),
    to: at(300),
    channels: Array.from({ length: N }, (_, i) => ({
      id: i + 1,
      number: i + 1,
      name: `Канал ${i + 1}`,
      category: '',
      programs: [
        { id: `${i + 1}-1`, itemId: 0, kind: 'external', title: `Сейчас ${i + 1}`, start: at(-30), end: at(30) },
        { id: `${i + 1}-2`, itemId: 0, kind: 'external', title: `Потом ${i + 1}`, start: at(30), end: at(90) },
      ],
    })),
  }
}

beforeEach(() => {
  // jsdom has no scrolling
  Element.prototype.scrollIntoView = vi.fn()
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(JSON.stringify(window6h()), { status: 200, headers: { 'Content-Type': 'application/json' } })),
  )
})
afterEach(() => vi.unstubAllGlobals())

describe('the guide grid with thousands of channels', () => {
  it('draws only the rows in sight and walks down with the keys', async () => {
    render(
      <MemoryRouter>
        <Guide filter={ALL} favorites={{}} onFavorite={() => {}} playerTo={(id) => `/tv/${id}`} />
      </MemoryRouter>,
    )
    const grid = await screen.findByRole('grid')
    await waitFor(() => expect(grid.querySelector('[data-cell]')).toBeTruthy())
    expect(grid).toHaveAttribute('aria-rowcount', String(N + 1))
    const rows = grid.querySelectorAll('[role=row]')
    // a 768 px window of 64 px rows plus the overscan, and the ruler
    expect(rows.length).toBeGreaterThan(5)
    expect(rows.length).toBeLessThan(40)
    expect(screen.getByText('Канал 1')).toBeTruthy()
    expect(screen.queryByText(`Канал ${N}`)).toBeNull()

    const first = grid.querySelector<HTMLButtonElement>('[data-cell][tabindex="0"]')!
    expect(first.dataset.cell).toBe('0:0')
    act(() => first.focus())
    fireEvent.keyDown(first, { key: 'ArrowDown' })
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' })
    expect((document.activeElement as HTMLElement).dataset.cell).toBe('2:0')
  })
})
