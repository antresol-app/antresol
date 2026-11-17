// The core title page's action row: «Смотреть ▾» merges the online ways
// (none in the core build) and the addons' `watchWay` item actions (way id
// `<addonId>:<label>`), runs the one the profile chose last, and a kid gets
// «Попросить» only. With the episode guide known, a series' «Смотреть»
// plays the next episode through the player of its source kind (the
// build's, or an addon's episodeActions entry), and «Другие источники…»
// goes where the guide's does (useEpisodePlayers).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { EpisodeSource, ItemDetail as Detail, SourceKind, TitleEpisode, User } from '../../api/types'
import type { EpisodePlay } from '../../components/episodes/episodeTypes'
import { ep, online as onlineSource } from '../../components/episodes/__tests__/fixtures'
import { registerAddon, resetAddonsForTests } from '../../addons/registry'
import { session } from '../../state/session'
import { readWatchWay } from '../../state/watchWay'
import { fromTitle } from '../titleDetail'
import { TitleActionRow } from '../TitleActionRow'

// an online way, as the sources build's OnlineWay gives one (the core build's gives none)
const online: string[] = []
vi.mock('../ItemDetailFull', () => ({
  OnlineWay: ({ children }: { children: (ways: { id: string; label: string; run: () => void }[]) => unknown }) =>
    children(online.length ? [{ id: 'online', label: 'Онлайн', run: () => void online.push('ran') }] : []),
}))
// the build's own episode player (null: none, as in the core build)
const h = vi.hoisted(() => ({ play: null as unknown }))
vi.mock('../../components/episodes/EpisodePlay', () => ({ useEpisodePlay: () => h.play }))

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

const row = (o: { kid?: boolean } = {}) =>
  render(
    <MemoryRouter>
      <TitleActionRow item={o.kid ? { ...item, actions: { ...item.actions, watchNow: false, toServer: false } } : item} tmdbId={42} seasons={[]} kidRequest={o.kid ? { key: 'tmdb:42', allowed: true, request: null } : undefined} />
    </MemoryRouter>,
  )

describe('TitleActionRow', () => {
  beforeEach(() => {
    session.reset()
    resetAddonsForTests()
    localStorage.clear()
    online.length = 0
    h.play = null
  })
  afterEach(() => vi.unstubAllGlobals())

  it('merges online and addon watch ways and remembers the chosen way id', async () => {
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
    online.push('offered')
    await signIn(adult)
    const first = row()
    // nothing remembered: online leads
    await userEvent.click(screen.getByRole('button', { name: 'Смотреть' }))
    expect(online).toEqual(['offered', 'ran'])
    expect(ran).toEqual([])
    await userEvent.click(screen.getByRole('button', { name: 'Другие способы просмотра' }))
    expect(screen.getAllByRole('menuitemradio').map((m) => m.textContent)).toEqual(['Онлайн', 'Способ А', 'Способ Б'])
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Способ А' }))
    expect(ran).toEqual(['a:Дюна'])
    await userEvent.click(screen.getByRole('button', { name: 'Другие способы просмотра' }))
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

  it('without online the first addon way leads', async () => {
    const ran: string[] = []
    registerAddon('fake', { itemActions: [{ label: 'Способ', icon: 'play', watchWay: true, run: () => void ran.push('way') }] })
    await signIn(adult)
    row()
    await userEvent.click(screen.getByRole('button', { name: 'Смотреть' }))
    expect(ran).toEqual(['way'])
  })

  it('no way: no «Смотреть»', async () => {
    await signIn(adult)
    row()
    expect(screen.queryByRole('button', { name: 'Смотреть' })).toBeNull()
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
  const watcher: User = { ...adult, canWatchOnline: true }
  const page = (episodes: TitleEpisode[], o: { kid?: boolean } = {}) =>
    render(
      <MemoryRouter>
        <TitleActionRow item={show} tmdbId={77} seasons={[]} guide={{ episodes, choice: null }} kidRequest={o.kid ? { key: 'tmdb:77', allowed: true, request: null } : undefined} />
      </MemoryRouter>,
    )

  beforeEach(() => {
    session.reset()
    resetAddonsForTests()
    localStorage.clear()
    online.length = 0
    h.play = null
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('an online source plays through the build’s player', async () => {
    const core = fakePlay()
    h.play = core
    const mine = fakePlay()
    registerAddon('fake', { episodeActions: [{ kinds: ['alpha'], use: () => mine }] })
    await signIn(watcher)
    page([ep(2, { sources: [onlineSource('p', 'Voice')] })])
    await userEvent.click(await screen.findByRole('button', { name: 'Смотреть С1 Э2' }))
    expect(core.play).toHaveBeenCalledTimes(1)
    expect(core.play.mock.calls[0]![1]).toMatchObject({ kind: 'online' })
    expect(mine.play).not.toHaveBeenCalled()
  })

  it('a source of an addon’s kind plays through its episodeActions entry', async () => {
    const core = fakePlay()
    h.play = core
    const mine = fakePlay()
    registerAddon('fake', { episodeActions: [{ kinds: ['alpha'], use: () => mine }] })
    await signIn(adult)
    page([ep(2, { sources: [alpha()] })])
    await userEvent.click(await screen.findByRole('button', { name: 'Смотреть С1 Э2' }))
    await waitFor(() => expect(mine.play).toHaveBeenCalledTimes(1))
    expect(mine.play.mock.calls[0]![1]).toMatchObject({ kind: 'alpha' })
    expect(core.play).not.toHaveBeenCalled()
  })

  it('a kind with no live player is skipped: another playable source plays, else no next-episode «Смотреть»', async () => {
    const core = fakePlay()
    h.play = core
    const gone = (): EpisodeSource => ({ ...alpha(), kind: 'pinned' as SourceKind, ref: 'p:2' })
    await signIn(watcher)
    const first = page([ep(2, { sources: [gone(), onlineSource('p', 'Voice')] })])
    await userEvent.click(await screen.findByRole('button', { name: 'Смотреть С1 Э2' }))
    expect(core.play).toHaveBeenCalledTimes(1)
    expect(core.play.mock.calls[0]![1]).toMatchObject({ kind: 'online' })
    first.unmount()
    // only the dead kind: the next-episode button is not offered
    page([ep(2, { sources: [gone()] })])
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Смотреть С1 Э2' })).toBeNull())
    expect(core.play).toHaveBeenCalledTimes(1)
  })

  it('an addon’s kind plays without the build’s player too (the core build)', async () => {
    const mine = fakePlay()
    registerAddon('fake', { episodeActions: [{ kinds: ['alpha'], use: () => mine }] })
    await signIn(adult)
    page([ep(2, { sources: [alpha()] })])
    await userEvent.click(await screen.findByRole('button', { name: 'Смотреть С1 Э2' }))
    expect(mine.play).toHaveBeenCalledTimes(1)
  })

  it('«Другие источники…»: the online picker for a profile that watches online and a title with online sources, else the addon’s', async () => {
    const core = fakePlay()
    h.play = core
    const mine = fakePlay()
    registerAddon('fake', { episodeActions: [{ kinds: ['alpha'], use: () => mine }] })
    const other = async () => {
      await userEvent.click(await screen.findByRole('button', { name: 'Другие способы просмотра' }))
      await userEvent.click(screen.getByRole('menuitem', { name: 'Другие источники…' }))
    }
    await signIn(watcher)
    const first = page([ep(2, { sources: [onlineSource('p', 'Voice'), alpha()] })])
    await other()
    expect(core.other).toHaveBeenCalledTimes(1)
    expect(mine.other).not.toHaveBeenCalled()
    first.unmount()

    // the same profile, a title with no online source: the addon's
    const second = page([ep(2, { sources: [alpha()] })])
    await other()
    expect(mine.other).toHaveBeenCalledTimes(1)
    second.unmount()

    // a profile that does not watch online: the addon's
    session.reset()
    await signIn(adult)
    page([ep(2, { sources: [onlineSource('p', 'Voice'), alpha()] })])
    await other()
    expect(mine.other).toHaveBeenCalledTimes(2)
    expect(core.other).toHaveBeenCalledTimes(1)
  })

  it('an entry whose use() throws leaves the row and «Смотреть»', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const core = fakePlay()
    h.play = core
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
    await signIn(watcher)
    page([ep(2, { sources: [onlineSource('p', 'Voice')] })])
    await userEvent.click(await screen.findByRole('button', { name: 'Смотреть С1 Э2' }))
    expect(core.play).toHaveBeenCalledTimes(1)
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
