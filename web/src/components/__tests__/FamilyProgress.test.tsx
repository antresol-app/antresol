import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { FamilyMember } from '../../api/types'
import { FamilyProgress, jumpLabel, placeOf, ringOf } from '../FamilyProgress'

const member = (over: Partial<FamilyMember>): FamilyMember => ({
  userId: 2,
  name: 'Мама',
  avatar: 'grad-3',
  isKid: false,
  itemId: 10,
  positionMs: 0,
  durationMs: 0,
  fraction: 0,
  finished: false,
  updatedAt: new Date(Date.now() - 2 * 3600_000).toISOString(),
  ...over,
})

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

describe('FamilyProgress', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('is not there when nobody else has progress', () => {
    const { container } = render(<FamilyProgress members={[]} kind="movie" onOpen={() => {}} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('folds into one line and unfolds into everyone’s place', async () => {
    const members = [
      member({ itemId: 11, season: 1, episode: 2, positionMs: 17 * 60_000, durationMs: 45 * 60_000, fraction: 0.38, done: 1, total: 3 }),
      member({ userId: 3, name: 'Петя', itemId: 11, season: 1, episode: 2, next: true, done: 1, total: 3 }),
      member({ userId: 4, name: 'Бабушка', finished: true, done: 3, total: 3 }),
    ]
    render(<FamilyProgress members={members} kind="series" onOpen={() => {}} />)
    const fold = screen.getByRole('button', { name: /Семья/ })
    expect(fold).toHaveAttribute('aria-expanded', 'false')
    expect(fold).toHaveTextContent('Мама · С1 Э2 · 17 мин')
    expect(fold).toHaveTextContent('ещё 2')
    expect(screen.queryByText('Петя')).toBeNull()

    await userEvent.click(fold)
    expect(fold).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('Далее: С1 Э2')).toBeInTheDocument()
    expect(screen.getByText('Всё просмотрено')).toBeInTheDocument()
    expect(screen.getAllByText(/2 ч назад/, { selector: 'span' })).toHaveLength(3)
    expect(screen.getByRole('button', { name: /С1 Э2 с 17:00 — с места профиля «Мама»/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Смотреть С1 Э2 — с места профиля «Петя»/ })).toBeInTheDocument()
    // finished: nothing to continue
    expect(screen.queryByRole('button', { name: /Бабушка/ })).toBeNull()
  })

  it('says where a reader or listener is', () => {
    expect(placeOf('book', member({ locator: 'page:120', page: 120, pages: 300, fraction: 0.4 }))).toBe('стр. 120 из 300')
    expect(placeOf('book', member({ locator: 'epubcfi(/6/4)', fraction: 0.42 }))).toBe('42 %')
    expect(placeOf('comic', member({ volume: 3, page: 12, pages: 40 }))).toBe('Том 3 · стр. 12 из 40')
    const chapters = [0, 60_000, 120_000].map((startMs, idx) => ({ idx, title: '', fileId: 1, startMs, endMs: startMs + 60_000 }))
    expect(placeOf('audiobook', member({ positionMs: 90_000, durationMs: 180_000, fraction: 0.5 }), chapters)).toBe('Глава 2 · 50 %')
    expect(placeOf('movie', member({ positionMs: 34 * 60_000, durationMs: 110 * 60_000 }))).toBe('34 мин из 1 ч 50 мин')
    expect(placeOf('book', member({ finished: true }))).toBe('Прочитано')
    expect(jumpLabel('book', member({ locator: 'epubcfi(/6/4)', fraction: 0.42 }))).toBe('Читать с 42 %')
    expect(jumpLabel('comic', member({ volume: 4, next: true }))).toBe('Читать: Том 4')
    expect(jumpLabel('movie', member({ finished: true }))).toBe('')
    expect(ringOf(member({ fraction: 0.3, done: 2, total: 4 }))).toBe(0.5)
  })

  it('takes their position as mine first, then opens it', async () => {
    const calls: [string, string | undefined, unknown][] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push([String(input), init?.method, init?.body ? JSON.parse(String(init.body)) : undefined])
        return json({ positionMs: 2_000_000 })
      }),
    )
    const onOpen = vi.fn()
    const m = member({ userId: 7, itemId: 42, positionMs: 2_000_000, durationMs: 6_000_000, fraction: 0.33 })
    render(<FamilyProgress members={[m]} kind="movie" onOpen={onOpen} />)
    await userEvent.click(screen.getByRole('button', { name: /Семья/ }))
    await userEvent.click(screen.getByRole('button', { name: /Смотреть с 33:20/ }))
    await waitFor(() => expect(onOpen).toHaveBeenCalledWith(m))
    // only this profile's progress is written: the server copies theirs
    expect(calls).toEqual([['/api/progress/42/adopt', 'POST', { userId: 7 }]])
  })

  it('starts a next episode without taking anyone’s position', async () => {
    const fetch = vi.fn(async () => json({}))
    vi.stubGlobal('fetch', fetch)
    const onOpen = vi.fn()
    const m = member({ itemId: 12, season: 1, episode: 3, next: true })
    render(<FamilyProgress members={[m]} kind="series" onOpen={onOpen} />)
    await userEvent.click(screen.getByRole('button', { name: /Семья/ }))
    await userEvent.click(screen.getByRole('button', { name: /Смотреть С1 Э3/ }))
    await waitFor(() => expect(onOpen).toHaveBeenCalled())
    expect(fetch).not.toHaveBeenCalled()
  })

  it('does not open anything when the server refuses', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ error: 'Не найдено' }, 404)))
    const onOpen = vi.fn()
    render(<FamilyProgress members={[member({ positionMs: 60_000, durationMs: 600_000 })]} kind="episode" onOpen={onOpen} />)
    await userEvent.click(screen.getByRole('button', { name: /Семья/ }))
    await userEvent.click(screen.getByRole('button', { name: /Смотреть с 1:00/ }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Не получилось')
    expect(onOpen).not.toHaveBeenCalled()
  })
})
