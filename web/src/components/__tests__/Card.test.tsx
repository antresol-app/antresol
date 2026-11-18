// An addon's card of «Продолжить» (home.continue): it opens the page its
// addon names, keeps its progress, and has a key of its own in a row.
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Card as CardData } from '../../api/types'
import { Card, cardKey } from '../Card'
import { card } from './fixtures'

const addonCard = (over: Partial<CardData> = {}): CardData =>
  card(0, {
    title: 'Сёгун',
    subtitle: 'S1 E2',
    onServer: false,
    progress: 0.25,
    tmdbKind: 'series',
    tmdbId: 126308,
    season: 1,
    episode: 2,
    resumeSec: 600,
    addonId: 'seed',
    key: 'addon:seed:s-1',
    href: '/addons/seed/watch/series/126308?s=1&e=2&t=600',
    ...over,
  })

const show = (c: CardData) =>
  render(
    <MemoryRouter>
      <Card card={c} variant="wide" />
    </MemoryRouter>,
  )

describe('addon cards', () => {
  it('an addon card links to its href and resumes', () => {
    show(addonCard())
    const link = screen.getByRole('link')
    expect(link).toHaveAttribute('href', '/addons/seed/watch/series/126308?s=1&e=2&t=600')
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '25')
    // not a library title: no «Ещё не на сервере» corner
    expect(screen.queryByTestId('card-corner')).not.toBeInTheDocument()
  })

  it('without an href it opens the title page', () => {
    show(addonCard({ href: undefined }))
    expect(screen.getByRole('link')).toHaveAttribute('href', '/title/series/126308?season=1&episode=2')
  })

  it('has the key the core gave it', () => {
    expect(cardKey(addonCard())).toBe('addon:seed:s-1')
    expect(cardKey(card(3))).toBe('movie-3')
  })
})
