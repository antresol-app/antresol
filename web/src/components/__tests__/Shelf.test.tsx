import { describe, expect, it } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { Shelf } from '../Shelf'
import { card } from './fixtures'

describe('Shelf', () => {
  it('renders N cards with a title', () => {
    const cards = [1, 2, 3, 4, 5].map((i) => card(i))
    render(
      <MemoryRouter>
        <Shelf title="Недавно добавленные фильмы" cards={cards} />
      </MemoryRouter>,
    )
    const region = screen.getByRole('region', { name: 'Недавно добавленные фильмы' })
    expect(within(region).getAllByRole('listitem')).toHaveLength(5)
    expect(within(region).getByRole('link', { name: /Фильм 3/ })).toHaveAttribute('href', '/item/3')
  })

  it('draws progress 0.6 as a 60% wide bar', () => {
    render(
      <MemoryRouter>
        <Shelf title="Продолжить" variant="wide" cards={[card(7, { progress: 0.6 })]} />
      </MemoryRouter>,
    )
    const bar = screen.getByRole('progressbar')
    expect(bar).toHaveAttribute('aria-valuenow', '60')
    expect(within(bar).getByTestId('progress-fill')).toHaveStyle({ width: '60%' })
  })

  it('hides the progress bar when there is no progress', () => {
    render(
      <MemoryRouter>
        <Shelf title="Фильмы" cards={[card(1)]} />
      </MemoryRouter>,
    )
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
  })
})
