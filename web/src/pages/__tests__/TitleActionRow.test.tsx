// The core title page's action row: «Смотреть ▾» offers the addons'
// `watchWay` item actions (way id `<addonId>:<label>`), runs the one the
// profile chose last, and a kid gets «Попросить» only. With the episode
// guide known, a series' «Смотреть» plays the next episode through the
// player of its source kind (an addon's episodeActions entry), and
// «Другие источники…» goes where the guide's does (useEpisodePlayers).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useSyncExternalStore } from 'react'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation } from 'react-router'
import type { EpisodeSource, ItemDetail as Detail, SourceKind, TitleEpisode, User } from '../../api/types'
import type { EpisodePlay } from '../../components/episodes/episodeTypes'
import { ep, online as onlineSource } from '../../components/episodes/__tests__/fixtures'
import { contributionsOf, registerAddon, resetAddonsForTests } from '../../addons/registry'
import { session } from '../../state/session'
import { readWatchWay } from '../../state/watchWay'
import { fromTitle } from '../titleDetail'
import { TitleActionRow } from '../TitleActionRow'

const adult: User = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const kid: User = { ...adult, id: 3, name: 'Вася', isKid: true, canRequest: true }
const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

const item: Detail = fromTitle(
  { tmdbId: 42, kind: 'movie', title: 'Дюна', year: 2021, poster: '', backdrop: '', overview: '', inLibrary: false, libraryItemId: null, genres: [], seasons: [] },
  false,
)

async function signIn(u: User) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => (String(input).endsWith('/api/login') ? json(u) : json({}))),
  )
  await session.login(u.id)
}

function Where() {
  const l = useLocation()
  return <output data-testid="where">{l.pathname + l.search}</output>
}

const row = (o: { kid?: boolean } = {}) =>
  render(
    <MemoryRouter>
      <TitleActionRow item={o.kid ? { ...item, actions: { ...item.actions, watchNow: false, toServer: false } } : item} tmdbId={42} kidRequest={o.kid ? { key: 'tmdb:42', allowed: true, request: null } : undefined} />
    </MemoryRouter>,
  )

describe('TitleActionRow', () => {
  beforeEach(() => {
    session.reset()
    resetAddonsForTests()
    localStorage.clear()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('lists the addon watch ways, the first leads, and remembers the chosen way id', async () => {
    const ran: string[] = []
    registerAddon('fake', {
      strings: { ru: { 'way.a': 'Способ А', 'way.b': 'Способ Б' }, en: {} },
      itemActions: [
        { label: 'way.a', icon: 'play', watchWay: true, run: (it) => void ran.push(`a:${it.title}`) },
        { label: 'way.b', icon: 'play', watchWay: true, run: () => void ran.push('b') },
        // not a way: a round button of the row
        { label: 'Кнопка', icon: 'download', run: () => undefined },
        // hidden for this title
        { label: 'way.c', icon: 'play', watchWay: true, visible: () => false, run: () => undefined },
      ],
    })
    await signIn(adult)
    const first = row()
    // nothing remembered: the first way leads
    await userEvent.click(screen.getByRole('button', { name: 'Смотреть' }))
    expect(ran).toEqual(['a:Дюна'])
    await userEvent.click(screen.getByRole('button', { name: 'Другие способы просмотра' }))
    expect(screen.getAllByRole('menuitemradio').map((m) => m.textContent)).toEqual(['Способ А', 'Способ Б'])
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Способ Б' }))
    expect(ran).toEqual(['a:Дюна', 'b'])
    expect(readWatchWay(adult.id)).toBe('fake:way.b')
    expect(screen.getByRole('button', { name: 'Кнопка' })).toBeInTheDocument()
    first.unmount()

    // the next visit: the chosen way is the main part
    row()
    await userEvent.click(screen.getByRole('button', { name: 'Смотреть' }))
    expect(ran).toEqual(['a:Дюна', 'b', 'b'])
  })

  it('an element a way returns is drawn until it closes', async () => {
    registerAddon('fake', {
      itemActions: [
        {
          label: 'Способ',
          icon: 'play',
          watchWay: true,
          run: (_it, close) => (
            <div role="dialog" aria-label="Лист дополнения">
              <button onClick={close}>Закрыть</button>
            </div>
          ),
        },
      ],
    })
    await signIn(adult)
    row()
    await userEvent.click(screen.getByRole('button', { name: 'Смотреть' }))
    expect(screen.getByRole('dialog', { name: 'Лист дополнения' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Закрыть' }))
    expect(screen.queryByRole('dialog', { name: 'Лист дополнения' })).toBeNull()
  })

  it('no way: no «Смотреть»', async () => {
    await signIn(adult)
    row()
    expect(screen.queryByRole('button', { name: 'Смотреть' })).toBeNull()
  })

  it('a way whose useAvailable stays null is not offered and the row does not reflow', async () => {
    const ran: string[] = []
    const asked: unknown[] = []
    // the addon's answer (null: still asking), as a store its hook reads
    let answer: boolean | null = null
    const listeners = new Set<() => void>()
    const say = (a: boolean | null) => act(() => ((answer = a), listeners.forEach((l) => l())))
    const useAnswer = () =>
      useSyncExternalStore(
        (l) => (listeners.add(l), () => void listeners.delete(l)),
        () => answer,
      )
    registerAddon('fake', {
      itemActions: [
        { label: 'Способ А', icon: 'play', watchWay: true, run: () => void ran.push('a') },
        {
          label: 'Способ Б',
          icon: 'play',
          watchWay: true,
          useAvailable: (t) => {
            asked.push(t)
            return useAnswer()
          },
          run: () => void ran.push('b'),
        },
        { label: 'Кнопка', icon: 'download', run: () => undefined },
      ],
    })
    await signIn(adult)
    const { container } = row()
    const box = container.firstElementChild as HTMLElement
    const main = screen.getByRole('button', { name: 'Смотреть' })
    // the ▾ is drawn from the first frame: the way that comes lands in its menu
    const more = screen.getByRole('button', { name: 'Другие способы просмотра' })
    const button = screen.getByRole('button', { name: 'Кнопка' })
    const drawn = { height: box.getBoundingClientRect().height, nodes: box.querySelectorAll('*').length }
    expect(asked[0]).toEqual({ kind: 'movie', tmdbId: 42, itemId: 0 })
    await userEvent.click(more)
    expect(screen.getAllByRole('menuitemradio').map((m) => m.textContent)).toEqual(['Способ А'])
    await userEvent.keyboard('{Escape}')
    // still asking: not offered, nothing moved
    await new Promise((r) => setTimeout(r, 20))
    expect(screen.queryByRole('menuitemradio', { name: 'Способ Б' })).toBeNull()
    // it answers: the menu gains the entry; the row is the same nodes, the same box
    say(true)
    expect(screen.getByRole('button', { name: 'Смотреть' })).toBe(main)
    expect(screen.getByRole('button', { name: 'Другие способы просмотра' })).toBe(more)
    expect(screen.getByRole('button', { name: 'Кнопка' })).toBe(button)
    expect({ height: box.getBoundingClientRect().height, nodes: box.querySelectorAll('*').length }).toEqual(drawn)
    await userEvent.click(more)
    expect(screen.getAllByRole('menuitemradio').map((m) => m.textContent)).toEqual(['Способ А', 'Способ Б'])
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Способ Б' }))
    expect(ran).toEqual(['b'])
    // it changes its mind: the way goes, the ▾ stays where it was
    say(false)
    expect(screen.getByRole('button', { name: 'Другие способы просмотра' })).toBe(more)
    await userEvent.click(more)
    expect(screen.getAllByRole('menuitemradio').map((m) => m.textContent)).toEqual(['Способ А'])
  })

  it('a throwing useAvailable hides only its way', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    registerAddon('fake', {
      itemActions: [
        { label: 'Способ А', icon: 'play', watchWay: true, run: () => undefined },
        {
          label: 'Сломан',
          icon: 'play',
          watchWay: true,
          useAvailable: () => {
            throw new Error('boom')
          },
          run: () => undefined,
        },
        { label: 'Способ Б', icon: 'play', watchWay: true, useAvailable: () => true, run: () => undefined },
        { label: 'Кнопка', icon: 'download', run: () => undefined },
      ],
    })
    await signIn(adult)
    row()
    await userEvent.click(screen.getByRole('button', { name: 'Другие способы просмотра' }))
    expect(screen.getAllByRole('menuitemradio').map((m) => m.textContent)).toEqual(['Способ А', 'Способ Б'])
    expect(screen.getByRole('button', { name: 'Кнопка' })).toBeInTheDocument()
    vi.restoreAllMocks()
  })

  it('?way= runs that way once and drops the param', async () => {
    const ran: string[] = []
    let answer: boolean | null = null
    const listeners = new Set<() => void>()
    const useAnswer = () =>
      useSyncExternalStore(
        (l) => (listeners.add(l), () => void listeners.delete(l)),
        () => answer,
      )
    registerAddon('fake', {
      itemActions: [
        { label: 'way.a', icon: 'play', watchWay: true, run: () => void ran.push('a') },
        { label: 'way.b', icon: 'play', watchWay: true, useAvailable: () => useAnswer(), run: () => void ran.push('b') },
      ],
    })
    await signIn(adult)
    render(
      <MemoryRouter initialEntries={['/title/movie/42?x=1&way=fake%3Away.b']}>
        <TitleActionRow item={item} tmdbId={42} />
        <Where />
      </MemoryRouter>,
    )
    // the way still asks: it waits, the param stays
    await new Promise((r) => setTimeout(r, 20))
    expect(ran).toEqual([])
    expect(screen.getByTestId('where').textContent).toBe('/title/movie/42?x=1&way=fake%3Away.b')
    act(() => ((answer = true), listeners.forEach((l) => l())))
    await waitFor(() => expect(ran).toEqual(['b']))
    await waitFor(() => expect(screen.getByTestId('where').textContent).toBe('/title/movie/42?x=1'))
    // once: a registration (or any re-render) does not run it again
    act(() => registerAddon('fake', { ...contributionsOf('fake')! }))
    await new Promise((r) => setTimeout(r, 20))
    expect(ran).toEqual(['b'])
    // the remembered way is untouched by a link
    expect(readWatchWay(adult.id)).toBeNull()
  })

  it('?way=online:way runs the way of the addon named online', async () => {
    const ran: string[] = []
    registerAddon('online', { itemActions: [{ label: 'way', icon: 'play', watchWay: true, run: () => void ran.push('way') }] })
    await signIn(adult)
    render(
      <MemoryRouter initialEntries={['/title/movie/42?way=online:way']}>
        <TitleActionRow item={item} tmdbId={42} />
        <Where />
      </MemoryRouter>,
    )
    await waitFor(() => expect(ran).toEqual(['way']))
    await waitFor(() => expect(screen.getByTestId('where').textContent).toBe('/title/movie/42'))
  })

  it('a kid gets «Попросить» and none of the addon ways or buttons', async () => {
    registerAddon('fake', {
      itemActions: [
        { label: 'Способ', icon: 'play', watchWay: true, run: () => undefined },
        { label: 'Кнопка', icon: 'download', run: () => undefined },
      ],
    })
    await signIn(kid)
    row({ kid: true })
    expect(await screen.findByRole('button', { name: /Попросить/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Смотреть' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Кнопка' })).toBeNull()
  })
})

describe('TitleActionRow: the guide’s next episode', () => {
  const show: Detail = fromTitle(
    { tmdbId: 77, kind: 'series', title: 'Сёгун', year: 2024, poster: '', backdrop: '', overview: '', inLibrary: false, libraryItemId: null, genres: [], seasons: [{ number: 1, episodes: 3 }] },
    false,
  )
  // an addon's own source kind, played by its episodeActions entry
  const alpha = (): EpisodeSource => ({ kind: 'alpha' as SourceKind, ref: 'a:2', provider: 'Дополнение', labels: [], firstSeen: '2026-01-01', meta: {} })
  const fakePlay = () => ({ play: vi.fn(), other: vi.fn(), node: null }) as EpisodePlay & { play: ReturnType<typeof vi.fn>; other: ReturnType<typeof vi.fn> }
  const page = (episodes: TitleEpisode[], o: { kid?: boolean } = {}) =>
    render(
      <MemoryRouter>
        <TitleActionRow item={show} tmdbId={77} guide={{ episodes, choice: null }} kidRequest={o.kid ? { key: 'tmdb:77', allowed: true, request: null } : undefined} />
      </MemoryRouter>,
    )

  // the addon that plays the online kind (the one the guide ranks at 20)
  const onlineAddon = (mine: EpisodePlay) => registerAddon('online', { episodeActions: [{ kinds: ['online'], order: { online: 20 }, use: () => mine }] })

  beforeEach(() => {
    session.reset()
    resetAddonsForTests()
    localStorage.clear()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('a source of kind online plays through the addon that declares it', async () => {
    const online = fakePlay()
    onlineAddon(online)
    const mine = fakePlay()
    registerAddon('fake', { episodeActions: [{ kinds: ['alpha'], use: () => mine }] })
    await signIn(adult)
    page([ep(2, { sources: [onlineSource('p', 'Voice')] })])
    await userEvent.click(await screen.findByRole('button', { name: 'Смотреть С1 Э2' }))
    await waitFor(() => expect(online.play).toHaveBeenCalledTimes(1))
    expect(online.play.mock.calls[0]![1]).toMatchObject({ kind: 'online' })
    expect(mine.play).not.toHaveBeenCalled()
  })

  it('a source of kind online has no player when no addon declares it', async () => {
    await signIn(adult)
    page([ep(2, { sources: [onlineSource('p', 'Voice')] })])
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Смотреть С1 Э2' })).toBeNull())
  })

  it('a source of an addon’s kind plays through its episodeActions entry', async () => {
    const mine = fakePlay()
    registerAddon('fake', { episodeActions: [{ kinds: ['alpha'], use: () => mine }] })
    await signIn(adult)
    page([ep(2, { sources: [alpha()] })])
    await userEvent.click(await screen.findByRole('button', { name: 'Смотреть С1 Э2' }))
    await waitFor(() => expect(mine.play).toHaveBeenCalledTimes(1))
    expect(mine.play.mock.calls[0]![1]).toMatchObject({ kind: 'alpha' })
  })

  it('a kind with no live player is skipped: another playable source plays, else no next-episode «Смотреть»', async () => {
    const online = fakePlay()
    onlineAddon(online)
    const gone = (): EpisodeSource => ({ ...alpha(), kind: 'pinned' as SourceKind, ref: 'p:2' })
    await signIn(adult)
    const first = page([ep(2, { sources: [gone(), onlineSource('p', 'Voice')] })])
    await userEvent.click(await screen.findByRole('button', { name: 'Смотреть С1 Э2' }))
    await waitFor(() => expect(online.play).toHaveBeenCalledTimes(1))
    expect(online.play.mock.calls[0]![1]).toMatchObject({ kind: 'online' })
    first.unmount()
    // only the dead kind: the next-episode button is not offered
    page([ep(2, { sources: [gone()] })])
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Смотреть С1 Э2' })).toBeNull())
    expect(online.play).toHaveBeenCalledTimes(1)
  })

  it('«Другие источники…» goes to the addon whose kind the guide lists, the best ranked first', async () => {
    const mine = fakePlay()
    // registered first, and with no place on the scale
    registerAddon('fake', { episodeActions: [{ kinds: ['alpha'], use: () => mine }] })
    const online = fakePlay()
    onlineAddon(online)
    const other = async () => {
      await userEvent.click(await screen.findByRole('button', { name: 'Другие способы просмотра' }))
      await userEvent.click(screen.getByRole('menuitem', { name: 'Другие источники…' }))
    }
    await signIn(adult)
    const first = page([ep(2, { sources: [alpha(), onlineSource('p', 'Voice')] })])
    await other()
    expect(online.other).toHaveBeenCalledTimes(1)
    expect(mine.other).not.toHaveBeenCalled()
    first.unmount()

    // the guide lists only the other addon's kind: that addon, whatever the scale says
    page([ep(2, { sources: [alpha()] })])
    await other()
    expect(mine.other).toHaveBeenCalledTimes(1)
    expect(online.other).toHaveBeenCalledTimes(1)
  })

  it('an entry whose use() throws leaves the row and «Смотреть»', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const online = fakePlay()
    onlineAddon(online)
    registerAddon('fake', {
      episodeActions: [
        {
          kinds: ['alpha'],
          use: () => {
            throw new Error('boom')
          },
        },
      ],
    })
    await signIn(adult)
    page([ep(2, { sources: [onlineSource('p', 'Voice')] })])
    await userEvent.click(await screen.findByRole('button', { name: 'Смотреть С1 Э2' }))
    await waitFor(() => expect(online.play).toHaveBeenCalledTimes(1))
  })

  it('a kid gets none of it: no Host runs, only «Попросить»', async () => {
    const use = vi.fn(() => fakePlay())
    registerAddon('fake', { episodeActions: [{ kinds: ['alpha'], use }] })
    await signIn(kid)
    page([ep(2, { sources: [alpha()] })], { kid: true })
    expect(await screen.findByRole('button', { name: /Попросить/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Смотреть С1 Э2' })).toBeNull()
    expect(use).not.toHaveBeenCalled()
  })
})
