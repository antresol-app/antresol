import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation } from 'react-router'
import type { StorageItem } from '../../../api/types'
import { Files } from '../Files'
import { filterStorage, idleFor, sortStorage } from '../filesView'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const NOW = Date.parse('2026-11-01T12:00:00Z')
let next = 1
const item = (title: string, p: Partial<StorageItem>): StorageItem => ({
  id: next++, kind: 'movie', title, poster: '', size: 1000, files: 1, addedAt: '2024-01-01T00:00:00Z', lastWatched: null,
  trash: [{ root: 'movies', paths: [title] }], ...p,
})

// newest-watched Дюна; Брат watched 8 months ago; Вий never, added long ago;
// Новинка never, added last week; an album and an audiobook
const list: StorageItem[] = [
  item('Дюна', { size: 9000, lastWatched: '2026-10-29T20:00:00Z', addedAt: '2025-06-01T00:00:00Z' }),
  item('Брат', { size: 3000, lastWatched: '2026-03-01T20:00:00Z', addedAt: '2023-01-01T00:00:00Z' }),
  item('Вий', { size: 2000, addedAt: '2022-01-01T00:00:00Z' }),
  item('Новинка', { size: 5000, addedAt: '2026-10-25T00:00:00Z' }),
  item('Сёгун', { kind: 'series', size: 7000, files: 10, lastWatched: '2025-09-01T00:00:00Z', addedAt: '2025-01-01T00:00:00Z' }),
  item('Группа крови', { kind: 'album', artist: 'Кино', size: 400, addedAt: '2024-05-01T00:00:00Z' }),
  item('Мастер и Маргарита', { kind: 'audiobook', size: 800, lastWatched: '2026-10-01T00:00:00Z' }),
]
const titles = (l: StorageItem[]) => l.map((x) => x.title)

describe('filesView: sort and filters', () => {
  it('sorts largest first, least recently watched first (never first), by name, newest first', () => {
    expect(titles(sortStorage(list, 'size'))).toEqual(['Дюна', 'Сёгун', 'Новинка', 'Брат', 'Вий', 'Мастер и Маргарита', 'Группа крови'])
    // never watched first (the largest of them first), then the longest ago
    expect(titles(sortStorage(list, 'watched'))).toEqual(['Новинка', 'Вий', 'Группа крови', 'Сёгун', 'Брат', 'Мастер и Маргарита', 'Дюна'])
    expect(titles(sortStorage(list, 'name', 'ru'))).toEqual(['Брат', 'Вий', 'Группа крови', 'Дюна', 'Мастер и Маргарита', 'Новинка', 'Сёгун'])
    expect(titles(sortStorage(list, 'added')).slice(0, 2)).toEqual(['Новинка', 'Дюна'])
  })

  it('«not watched for 6 months» leaves out what was just added and what was watched since', () => {
    expect(idleFor(list[3]!, 6, NOW)).toBe(false) // Новинка: never, but a week old
    expect(idleFor(list[2]!, 6, NOW)).toBe(true) // Вий
    expect(idleFor(list[1]!, 6, NOW)).toBe(true) // Брат: 8 months ago
    expect(idleFor(list[1]!, 12, NOW)).toBe(false)
    const f = (watch: Parameters<typeof filterStorage>[1]['watch'], segment: Parameters<typeof filterStorage>[1]['segment'] = 'all') =>
      titles(filterStorage(list, { segment, watch }, NOW))
    expect(f('idle6')).toEqual(['Брат', 'Вий', 'Сёгун', 'Группа крови'])
    expect(f('idle12')).toEqual(['Вий', 'Сёгун', 'Группа крови'])
    expect(f('never')).toEqual(['Вий', 'Новинка', 'Группа крови'])
    expect(f('watched', 'books')).toEqual(['Мастер и Маргарита'])
    expect(f('all', 'music')).toEqual(['Группа крови'])
  })
})

function Where() {
  return <output data-testid="where">{useLocation().search}</output>
}

function mount(search = '', items = list) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/stats/storage')) return json({ items })
      if (url.endsWith('/api/files/usage')) return json({ total: 0, used: 0, free: 0, categories: [] })
      return json({})
    }),
  )
  render(
    <MemoryRouter initialEntries={['/manage/files' + search]}>
      <Files />
      <Where />
    </MemoryRouter>,
  )
}

const names = async () => (await screen.findAllByTestId('storage-item')).map((li) => li.querySelectorAll('a')[1]!.firstChild!.textContent)
const openMenu = () => userEvent.click(screen.getByRole('button', { name: 'Сортировка и фильтры' }))

describe('Управление → Файлы: sections, sort and filters', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.useFakeTimers({ toFake: ['Date'], now: NOW })
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('lists every title largest first, with sections of what there is', async () => {
    mount('', list.filter((x) => x.kind !== 'audiobook'))
    expect(await names()).toEqual(['Дюна', 'Сёгун', 'Новинка', 'Брат', 'Вий', 'Группа крови'])
    expect(screen.getByRole('heading', { name: 'Больше всего места' })).toBeInTheDocument()
    expect(screen.getByTestId('storage-summary')).toHaveTextContent('6 названий')
    const seg = screen.getByRole('radiogroup', { name: 'Раздел медиатеки' })
    expect(within(seg).getAllByRole('radio').map((r) => r.textContent)).toEqual(['Все', 'Фильмы', 'Сериалы', 'Музыка'])
    expect(screen.getByText(/Альбом · Кино · добавлен .*, ещё не слушали/)).toBeInTheDocument()
    await userEvent.click(within(seg).getByRole('radio', { name: 'Музыка' }))
    expect(await names()).toEqual(['Группа крови'])
    expect(screen.getByTestId('where')).toHaveTextContent('?kind=music')
  })

  it('sorts by last watched from the menu and keeps it in the URL', async () => {
    mount()
    await names()
    await openMenu()
    const menu = screen.getByRole('menu', { name: 'Сортировка и фильтры' })
    expect(within(menu).getByRole('menuitemradio', { name: 'По размеру' })).toHaveAttribute('aria-checked', 'true')
    await userEvent.click(within(menu).getByRole('menuitemradio', { name: /По дате просмотра/ }))
    expect((await names())[0]).toBe('Новинка')
    expect(screen.getByRole('heading', { name: 'Давно не смотрели' })).toBeInTheDocument()
    expect(screen.getByTestId('where')).toHaveTextContent('?sort=watched')
  })

  it('filters «not watched for half a year» with the dot, an empty state and a reset', async () => {
    mount('?kind=movies')
    await names()
    await openMenu()
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Не смотрели полгода' }))
    expect(await names()).toEqual(['Брат', 'Вий'])
    expect(screen.getByTestId('filters-on')).toBeInTheDocument()
    expect(screen.getByTestId('where')).toHaveTextContent('?kind=movies&watch=idle6')
    // the words follow the section
    await userEvent.click(screen.getByRole('radio', { name: 'Книги' }))
    expect(screen.queryAllByTestId('storage-item')).toHaveLength(0)
    expect(screen.getByText('Ничего не подходит под фильтры.')).toBeInTheDocument()
    await openMenu()
    expect(screen.getByRole('menuitemradio', { name: 'Не открывали полгода' })).toHaveAttribute('aria-checked', 'true')
    await userEvent.keyboard('{Escape}')
    await userEvent.click(screen.getByRole('button', { name: 'Сбросить фильтры' }))
    expect(await names()).toEqual(['Мастер и Маргарита'])
    expect(screen.queryByTestId('filters-on')).not.toBeInTheDocument()
  })

  it('shows 60 rows, then «Показать ещё»', async () => {
    mount('', Array.from({ length: 70 }, (_, i) => item(`Фильм ${i}`, { size: 1000 - i })))
    expect(await names()).toHaveLength(60)
    await userEvent.click(screen.getByRole('button', { name: 'Показать ещё' }))
    expect(await names()).toHaveLength(70)
    expect(screen.queryByRole('button', { name: 'Показать ещё' })).not.toBeInTheDocument()
  })
})
