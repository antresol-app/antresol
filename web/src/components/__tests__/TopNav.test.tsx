import { describe, expect, it } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { TopNav } from '../TopNav'
import { setViewport } from '../../test/setup'
import { admin } from './fixtures'

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <TopNav user={admin} />
    </MemoryRouter>,
  )
}

describe('TopNav', () => {
  it('highlights the active section', () => {
    setViewport(1440)
    renderAt('/movies')
    const nav = screen.getByRole('navigation', { name: 'Разделы' })
    expect(within(nav).getByRole('link', { name: 'Фильмы' })).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'Главная' })).not.toHaveAttribute('aria-current')
    expect(within(nav).getByRole('link', { name: 'Поиск' })).toBeInTheDocument()
    expect(screen.queryByTestId('tabbar')).not.toBeInTheDocument()
  })

  it('treats nested routes as part of their section', () => {
    setViewport(1440)
    renderAt('/series/42')
    expect(screen.getByRole('link', { name: 'Сериалы' })).toHaveAttribute('aria-current', 'page')
  })

  it('renders the bottom TabBar on a 600px wide screen', () => {
    setViewport(600)
    renderAt('/')
    const bar = screen.getByTestId('tabbar')
    expect(within(bar).getByRole('link', { name: 'Главная' })).toHaveAttribute('aria-current', 'page')
    expect(screen.queryByTestId('topnav-pill')).not.toBeInTheDocument()
    setViewport(1440)
  })

  it('a phone has five tabs: «Медиа» holds Музыка and Книги and leads to the one open last', () => {
    setViewport(390)
    const { unmount } = renderAt('/books/12')
    const bar = screen.getByTestId('tabbar')
    expect(within(bar).getAllByRole('listitem').map((li) => li.textContent)).toEqual(['Главная', 'Фильмы', 'Сериалы', 'ТВ', 'Медиа'])
    expect(within(bar).getByRole('link', { name: 'Медиа' })).toHaveAttribute('aria-current', 'page')
    unmount()
    renderAt('/')
    expect(within(screen.getByTestId('tabbar')).getByRole('link', { name: 'Медиа' })).toHaveAttribute('href', '/books')
    localStorage.clear()
    setViewport(1440)
  })
})
