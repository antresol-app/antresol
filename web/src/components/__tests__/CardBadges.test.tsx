// The corner of a card: «Ещё не на сервере» sits in it; «Просмотрено» is the
// eye in the middle of the picture, never in the corner; a download's corner
// ring takes the corner alone.
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
  it('keeps the cloud in the corner and puts the eye in the middle', () => {
    draw(card(1, { onServer: false, watched: true }))
    const corner = screen.getByTestId('card-corner')
    expect(within(corner).getByTitle('Ещё не на сервере')).toBeInTheDocument()
    // the eye is the picture's own, not one of the corner's badges
    expect(within(corner).queryByRole('img', { name: 'Просмотрено' })).not.toBeInTheDocument()
    expect(screen.getByRole('img', { name: 'Просмотрено' })).toBeInTheDocument()
  })

  it('a watched library card has the eye and no corner', () => {
    draw(card(2, { watched: true }))
    expect(screen.queryByTestId('card-corner')).not.toBeInTheDocument()
    expect(screen.getByRole('link')).toHaveAccessibleName(/Просмотрено/)
  })

  it('no badge, no corner', () => {
    draw(card(3))
    expect(screen.queryByTestId('card-corner')).not.toBeInTheDocument()
  })
})
