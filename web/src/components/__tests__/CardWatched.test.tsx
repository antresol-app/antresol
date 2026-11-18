// A finished title reads as finished at a glance (web/DESIGN.md, «A watched
// title»): its picture dimmed, the eye in the corner, no progress bar — not
// even a full one. A title in progress keeps the bar and nothing else.
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Card as CardData } from '../../api/types'
import { Card } from '../Card'
import { card } from './fixtures'

const draw = (c: CardData, variant: 'poster' | 'wide' | 'square' = 'poster') =>
  render(
    <MemoryRouter>
      <Card card={c} variant={variant} />
    </MemoryRouter>,
  )

const art = (container: HTMLElement) => container.querySelector('img')!

describe('a watched card', () => {
  it('dims the picture, shows the eye and no progress bar', () => {
    const { container } = draw(card(1, { watched: true, progress: 1 }))
    expect(art(container)).toHaveClass('dim')
    expect(screen.getByRole('img', { name: 'Просмотрено' })).toBeInTheDocument()
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
  })

  it('dims the wide card (an episode of «Продолжить», a watched episode in a grid) the same way', () => {
    const { container } = draw(card(2, { kind: 'episode', watched: true, progress: 1 }), 'wide')
    expect(art(container)).toHaveClass('dim')
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
  })

  it('dims the title on a card without a picture', () => {
    const { container } = draw(card(3, { watched: true, poster: '', backdrop: '' }))
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('.fallbackTitle')).toHaveClass('dim')
  })

  it('says «Прочитано» of a book and «Прослушано» of an audiobook', () => {
    const book = draw(card(4, { kind: 'book', watched: true, progress: 1 }), 'poster')
    expect(screen.getByRole('img', { name: 'Прочитано' })).toBeInTheDocument()
    book.unmount()
    draw(card(5, { kind: 'audiobook', watched: true, progress: 1 }), 'square')
    expect(screen.getByRole('img', { name: 'Прослушано' })).toBeInTheDocument()
  })

  it('marks a comic series read once every volume is (the server sends watched)', () => {
    const { container } = draw(card(9, { kind: 'comic', watched: true }))
    expect(screen.getByRole('img', { name: 'Прочитано' })).toBeInTheDocument()
    expect(art(container)).toHaveClass('dim')
  })

  it('a card in progress has the bar and no mark, its picture as it is', () => {
    const { container } = draw(card(6, { progress: 0.4 }))
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '40')
    expect(screen.queryByRole('img', { name: 'Просмотрено' })).not.toBeInTheDocument()
    expect(art(container)).not.toHaveClass('dim')
  })

  it('a card nobody has started is plain', () => {
    const { container } = draw(card(7))
    expect(art(container)).not.toHaveClass('dim')
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
    expect(screen.queryByTestId('card-corner')).not.toBeInTheDocument()
  })

  it('a full bar is never drawn, watched or not', () => {
    draw(card(8, { progress: 1 }))
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
  })
})
