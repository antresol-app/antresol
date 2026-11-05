// An empty library in the core build: it has no way to bring titles in from
// Search, so its empty pages say where files go and lead to the rescan. (The
// other build keeps its own text: emptyLibrary.full.test.tsx.)
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
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

beforeEach(() => session.reset())
afterEach(() => {
  vi.unstubAllGlobals()
  session.reset()
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

describe('core build: an empty library', () => {
  it.runIf(__LIBRARY_EDITION__).each(ALL)('%s: the admin gets the folder and the rescan, not Search', async (page) => {
    await renderEmpty(page, admin)
    expect(await screen.findByText(TEXT[page])).toBeInTheDocument()
    const link = screen.getByRole('link', { name: 'Пересканировать в настройках' })
    expect(link).toHaveAttribute('href', '/manage/settings#rescan')
    expect(document.querySelector('a[href^="/search"]')).toBeNull()
  })

  // only the pages that show an adult the hint: the rest have their own line
  it.runIf(__LIBRARY_EDITION__).each(['music', 'books', 'grid'] as Page[])('%s: another adult has no link to Settings, nor to Search', async (page) => {
    await renderEmpty(page, adult)
    expect(await screen.findByText(TEXT[page])).toBeInTheDocument()
    expect(screen.queryByRole('link')).toBeNull()
  })
})
