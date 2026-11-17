// An empty library: the page says where files go and leads to the rescan;
// an addon's `emptyStateActions` of the page's scope add their buttons
// (every build the same, D15).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, screen } from '@testing-library/react'
import { registerAddon, resetAddonsForTests, setAddonUser } from '../../addons/registry'
import { en } from '../../i18n/en'
import { ru } from '../../i18n/ru'
import { session } from '../../state/session'
import { admin, adult, renderEmpty, type Page } from './emptyLibrary.harness'

const TEXT: Record<Page, string> = {
  home: 'Положите фильмы и сериалы в папки медиатеки на сервере — они появятся здесь.',
  movies: 'Положите файлы в папку медиатеки на сервере — они появятся здесь.',
  music: 'Положите альбомы в папку «Музыка» на сервере',
  albums: 'Положите альбомы в папку «Музыка» на сервере',
  books: 'Добавьте EPUB, FB2 или PDF со своего устройства или положите их в папку «Книги» на сервере',
  grid: 'Добавьте EPUB, FB2 или PDF со своего устройства или положите их в папку «Книги» на сервере',
}
const ALL = Object.keys(TEXT) as Page[]
// the scope each page asks the addons for
const SCOPE = { home: 'home', movies: 'video', music: 'music', albums: 'music', books: 'book', grid: 'book' } as const

beforeEach(() => session.reset())
afterEach(() => {
  vi.unstubAllGlobals()
  session.reset()
  resetAddonsForTests()
})

describe('the texts every build ships', () => {
  it('the core keys of an empty library promise nothing from Search', () => {
    const keys = ['browse.emptyAdmin', 'browse.home.emptyAdmin', 'music.emptyHint', 'books.empty.hint'] as const
    for (const k of keys) {
      expect(ru[k]).not.toMatch(/поиск|найд/i)
      expect(en[k]).not.toMatch(/search|find/i)
    }
  })
})

describe('an empty library without addons', () => {
  it.each(ALL)('%s: the admin gets the folder and the rescan, not Search', async (page) => {
    await renderEmpty(page, admin)
    expect(await screen.findByText(TEXT[page])).toBeInTheDocument()
    const link = screen.getByRole('link', { name: 'Пересканировать в настройках' })
    expect(link).toHaveAttribute('href', '/manage/settings/library#rescan')
    expect(document.querySelector('a[href^="/search"]')).toBeNull()
  })

  // only the pages that show an adult the hint: the rest have their own line
  it.each(['music', 'books', 'grid'] as Page[])('%s: another adult has no link to Settings, nor to Search', async (page) => {
    await renderEmpty(page, adult)
    expect(await screen.findByText(TEXT[page])).toBeInTheDocument()
    expect(screen.queryByRole('link')).toBeNull()
  })
})

describe('an empty library with an addon', () => {
  const register = () =>
    registerAddon('dl', {
      strings: { ru: { find: 'Найти и добавить' }, en: { find: 'Find and add' } },
      emptyStateActions: (['home', 'video', 'music', 'book'] as const).map((scope) => ({
        scope,
        label: 'find',
        to: scope === 'home' ? '/search' : `/search?dk=${scope}`,
      })),
    })

  it.each(ALL)('%s: the admin gets the rescan and the addon\'s button', async (page) => {
    register()
    act(() => setAddonUser({ isAdmin: true }))
    await renderEmpty(page, admin)
    expect(await screen.findByText(TEXT[page])).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Пересканировать в настройках' })).toBeInTheDocument()
    const href = SCOPE[page] === 'home' ? '/search' : `/search?dk=${SCOPE[page]}`
    expect(screen.getByRole('link', { name: 'Найти и добавить' })).toHaveAttribute('href', href)
  })

  it.each(['music', 'books', 'grid'] as Page[])('%s: another adult gets the addon\'s button only', async (page) => {
    register()
    await renderEmpty(page, adult)
    expect(await screen.findByText(TEXT[page])).toBeInTheDocument()
    expect(screen.getAllByRole('link').map((a) => a.textContent)).toEqual(['Найти и добавить'])
  })
})
