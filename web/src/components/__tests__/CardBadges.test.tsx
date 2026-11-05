// The corner of a card: «Ещё не на сервере» and «Просмотрено» sit side by
// side in one group, never on top of each other; a download's corner ring
// takes the corner alone.
import { describe, expect, it } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { Card } from '../Card'
import { card } from './fixtures'

const draw = (c: ReturnType<typeof card>) =>
  render(
    <MemoryRouter>
      <Card card={c} />
    </MemoryRouter>,
  )

describe('card corner badges', () => {
  it('lays the cloud and the check out together', () => {
    draw(card(1, { onServer: false, watched: true }))
    const corner = screen.getByTestId('card-corner')
    expect(within(corner).getByTitle('Ещё не на сервере')).toBeInTheDocument()
    expect(within(corner).getByRole('img', { name: 'Просмотрено' })).toBeInTheDocument()
  })

  it('a watched library card has the check only', () => {
    draw(card(2, { watched: true }))
    const corner = screen.getByTestId('card-corner')
    expect(within(corner).queryByTitle('Ещё не на сервере')).not.toBeInTheDocument()
    expect(screen.getByRole('link')).toHaveAccessibleName(/Просмотрено/)
  })

  it('no badge, no corner', () => {
    draw(card(3))
    expect(screen.queryByTestId('card-corner')).not.toBeInTheDocument()
  })
})
