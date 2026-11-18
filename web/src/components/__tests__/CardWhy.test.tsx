// «Вышло новое»: a card says why the profile follows its title — the mark
// and the list that set it, or the watchlist.
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Card as CardData } from '../../api/types'
import { Card } from '../Card'
import { card } from './fixtures'

const show = (c: CardData) =>
  render(
    <MemoryRouter>
      <Card card={c} />
    </MemoryRouter>,
  )

describe('why line', () => {
  it('names the list that set the mark', () => {
    show(card(0, { title: 'Судзумэ', tmdbKind: 'movie', tmdbId: 1, why: { state: 'planned', origin: 'myshows' } }))
    expect(screen.getByTestId('card-why')).toHaveTextContent('MyShows: Хочу посмотреть')
  })
  it('a mark made here and the watchlist', () => {
    const { unmount } = show(card(1, { why: { state: 'watching', origin: 'antresol' } }))
    expect(screen.getByTestId('card-why')).toHaveTextContent('Отметка: Смотрю')
    unmount()
    show(card(1, { why: { state: 'watchlist' } }))
    expect(screen.getByTestId('card-why')).toHaveTextContent('В списке «Хочу посмотреть»')
  })
  it('no why, no line', () => {
    show(card(1))
    expect(screen.queryByTestId('card-why')).toBeNull()
  })
})
