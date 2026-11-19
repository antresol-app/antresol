import { describe, expect, it } from 'vitest'
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { TabBar } from '../TabBar'

const ITEMS = [
  { to: '/', label: 'Главная', icon: 'homeFill' as const },
  { to: '/movies', label: 'Фильмы', icon: 'filmFrame' as const },
  { to: '/books', label: 'Медиа', icon: 'squareStackFill' as const, roots: ['/music', '/books'] },
]

function at(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <TabBar label="Разделы" items={ITEMS} accessory={{ to: '/search?scope=movies', label: 'Поиск', icon: 'search' }} expandLabel="Показать вкладки" />
    </MemoryRouter>,
  )
}

function scrollTo(y: number) {
  act(() => {
    Object.defineProperty(window, 'scrollY', { value: y, configurable: true })
    fireEvent.scroll(window)
  })
}

describe('TabBar', () => {
  it('is the layout’s bottom bar: a named navigation that menus keep clear of', () => {
    at('/')
    const nav = screen.getByRole('navigation', { name: 'Разделы' })
    expect(nav).toHaveAttribute('data-bottom-bar')
    expect(within(nav).getAllByRole('link')).toHaveLength(4)
  })

  it('marks the current tab under any of its roots; home only on its own path', () => {
    at('/music/album/3')
    expect(screen.getByRole('link', { name: 'Медиа' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'Медиа' })).toHaveAttribute('href', '/books')
    expect(screen.getByRole('link', { name: 'Главная' })).not.toHaveAttribute('aria-current')
  })

  it('shows every tab’s label under its glyph, the same solid glyph selected or not', () => {
    at('/movies')
    for (const i of ITEMS) {
      const link = screen.getByRole('link', { name: i.label })
      expect(link).toHaveTextContent(i.label)
      expect(link.querySelector('svg')!.getAttribute('data-icon')).toBe(i.icon)
    }
  })

  it('the lens sits under the current tab, and no lens on a page that is no tab’s', () => {
    const { container, unmount } = at('/movies')
    const lens = () => container.querySelector<HTMLElement>('[aria-hidden="true"][style*="--i"]')
    expect(lens()?.style.getPropertyValue('--i')).toBe('1')
    unmount()
    const other = at('/settings')
    expect(other.container.querySelector('[style*="--i"]')).toBeNull()
  })

  it('the square accessory is a link named by its label, current on its path whatever the query', () => {
    at('/search')
    const search = screen.getByRole('link', { name: 'Поиск' })
    expect(search).toHaveAttribute('href', '/search?scope=movies')
    expect(search).toHaveAttribute('aria-current', 'page')
    expect(search).toHaveTextContent('')
  })

  it('folds on a scroll down and opens on a scroll up; the folded square opens it too', () => {
    at('/movies')
    const nav = screen.getByRole('navigation', { name: 'Разделы' })
    // out of the accessibility tree while the bar is open
    const unfold = nav.querySelector('button')!
    expect(unfold).toHaveAttribute('aria-hidden', 'true')
    expect(nav).not.toHaveAttribute('data-minimized')
    expect(unfold).toHaveAttribute('tabindex', '-1')
    scrollTo(0)
    scrollTo(400)
    expect(nav).toHaveAttribute('data-minimized')
    // the tabs are out of reach while folded, the square is the way back
    expect(nav.querySelector('ul')).toHaveAttribute('inert')
    expect(screen.getByRole('button', { name: 'Показать вкладки' })).toBe(unfold)
    expect(unfold).toHaveAttribute('tabindex', '0')
    expect(unfold.querySelector('svg')!.getAttribute('data-icon')).toBe('filmFrame')
    scrollTo(300)
    expect(nav).not.toHaveAttribute('data-minimized')
    scrollTo(800)
    expect(nav).toHaveAttribute('data-minimized')
    fireEvent.click(unfold)
    expect(nav).not.toHaveAttribute('data-minimized')
    scrollTo(0)
  })
})
