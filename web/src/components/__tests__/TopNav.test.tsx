import { describe, expect, it } from 'vitest'
import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { TopNav } from '../TopNav'
import { setViewport } from '../../test/setup'
import { admin } from './fixtures'
import { getLanguagePref, setLanguagePref } from '../../i18n'

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

  it('switches the interface language from the profile menu and re-renders in English', async () => {
    setViewport(1440)
    renderAt('/movies')
    await userEvent.click(screen.getByRole('button', { name: `Профиль: ${admin.name}` }))
    const english = screen.getByRole('menuitemradio', { name: 'English' })
    expect(screen.getByRole('menuitemradio', { name: 'Как в системе' })).toHaveAttribute('aria-checked', 'true')
    await userEvent.click(english)
    expect(getLanguagePref()).toBe('en')
    expect(localStorage.getItem('vault.uiLanguage')).toBe('en')
    expect(document.documentElement.lang).toBe('en')
    const nav = screen.getByRole('navigation', { name: 'Sections' })
    expect(within(nav).getByRole('link', { name: 'Movies' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('menuitemradio', { name: 'English' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('menuitem', { name: 'Switch Profile' })).toBeInTheDocument()
    act(() => setLanguagePref('system'))
    expect(screen.getByRole('link', { name: 'Фильмы' })).toBeInTheDocument()
  })
})
