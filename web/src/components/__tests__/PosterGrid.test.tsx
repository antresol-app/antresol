// A grid that gets its next page renders the new cards only (Card is memo).
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { PosterGrid } from '../PosterGrid'
import { card } from './fixtures'

vi.mock('../../api/client', async (original) => {
  const m = await original<typeof import('../../api/client')>()
  return { ...m, imageUrl: vi.fn(m.imageUrl) }
})
const { imageUrl } = await import('../../api/client')

const grid = (cards: ReturnType<typeof card>[]) => (
  <MemoryRouter>
    <PosterGrid cards={cards} label="Фильмы" />
  </MemoryRouter>
)

describe('PosterGrid', () => {
  it('renders only the cards that came with the next page', () => {
    const first = [0, 1, 2].map((i) => card(i))
    const { rerender } = render(grid(first))
    expect(screen.getAllByRole('listitem')).toHaveLength(3)
    // one render of a card asks for its picture once
    expect(imageUrl).toHaveBeenCalledTimes(3)
    rerender(grid([...first, card(3), card(4)]))
    expect(screen.getAllByRole('listitem')).toHaveLength(5)
    expect(imageUrl).toHaveBeenCalledTimes(5)
  })

  it('draws a card again when its own data changes', () => {
    const first = [card(0), card(1)]
    const { rerender } = render(grid(first))
    rerender(grid([first[0]!, { ...first[1]!, title: 'Другое имя' }]))
    expect(screen.getByRole('link', { name: /Другое имя/ })).toBeInTheDocument()
  })
})
