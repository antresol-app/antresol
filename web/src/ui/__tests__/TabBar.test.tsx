import { describe, expect, it } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { TabBar } from '../TabBar'

const ITEMS = [
  { to: '/', label: 'Главная', icon: 'home' as const, activeIcon: 'homeFill' as const, end: true },
  { to: '/movies', label: 'Фильмы', icon: 'film' as const, activeIcon: 'filmFill' as const },
  { to: '/music', label: 'Музыка', icon: 'music' as const },
]

function at(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <TabBar label="Разделы" items={ITEMS} accessory={{ to: '/search', label: 'Поиск', icon: 'search' }} />
    </MemoryRouter>,
  )
}

describe('TabBar', () => {
  it('is the layout’s bottom bar: a named navigation that menus keep clear of', () => {
    at('/')
    const nav = screen.getByRole('navigation', { name: 'Разделы' })
    expect(nav).toHaveAttribute('data-bottom-bar')
    expect(within(nav).getAllByRole('link')).toHaveLength(4)
  })

  it('marks the current tab, a nested route included; home only on its own path', () => {
    at('/music/album/3')
    expect(screen.getByRole('link', { name: 'Музыка' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'Главная' })).not.toHaveAttribute('aria-current')
  })

  it('shows every tab’s label under its glyph', () => {
    at('/')
    for (const i of ITEMS) expect(screen.getByRole('link', { name: i.label })).toHaveTextContent(i.label)
  })

  it('the current tab draws its solid glyph, the others the outline (one without a solid glyph keeps its outline)', () => {
    at('/movies')
    const glyph = (name: string) => screen.getByRole('link', { name }).querySelector('svg')!.getAttribute('data-icon')
    expect(glyph('Фильмы')).toBe('filmFill')
    expect(glyph('Главная')).toBe('home')
    expect(glyph('Музыка')).toBe('music')
  })

  it('the round accessory is a link named by its label and marked when current', () => {
    at('/search')
    const search = screen.getByRole('link', { name: 'Поиск' })
    expect(search).toHaveAttribute('href', '/search')
    expect(search).toHaveAttribute('aria-current', 'page')
    expect(search).toHaveTextContent('')
  })
})
