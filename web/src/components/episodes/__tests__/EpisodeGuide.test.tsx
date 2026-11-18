import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation } from 'react-router'
import type { EpisodeSource, SourceChoice, SourceKind, TitleEpisodes } from '../../../api/types'
import { registerAddon, resetAddonsForTests } from '../../../addons/registry'
import { choiceOf } from '../../../state/pickSource'
import { session } from '../../../state/session'
import { EpisodeGuide } from '../EpisodeGuide'
import type { EpisodePlay } from '../episodeTypes'
import { rule } from '../../../ui/__tests__/cssRule'
import { ep, guide, lib, online } from './fixtures'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

function serve(data: TitleEpisodes | null, choice: SourceChoice | null = null) {
  const puts: unknown[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/episodes')) return data ? json(data) : new Response('{}', { status: 404 })
      if (url.endsWith('/choice')) {
        if (init?.method === 'PUT') {
          puts.push(JSON.parse(String(init.body)))
          return new Response(null, { status: 204 })
        }
        return json({ choice })
      }
      return json({ id: 2, name: 'Test', avatar: 'grad-1', hasPassword: false, isAdmin: false, isKid: false, maxRating: null })
    }),
  )
  return puts
}

const Where = () => {
  const l = useLocation()
  return <output data-testid="where">{l.pathname + l.search}</output>
}
const mount = (props: Partial<Parameters<typeof EpisodeGuide>[0]> = {}) =>
  render(
    <MemoryRouter>
      <EpisodeGuide kind="series" tmdbId={5} title="Show" {...props} />
      <Where />
    </MemoryRouter>,
  )

const fakePlay = (over: Partial<EpisodePlay> = {}) => ({ play: vi.fn(), other: vi.fn(), node: null, ...over }) as EpisodePlay & { play: ReturnType<typeof vi.fn>; other: ReturnType<typeof vi.fn> }
// the addon that plays the online kind (ranked 20 on the kinds' scale)
const onlineAddon = (mine: EpisodePlay) => registerAddon('online', { episodeActions: [{ kinds: ['online'], order: { online: 20 }, use: () => mine }] })

describe('EpisodeGuide', () => {
  beforeEach(async () => {
    session.reset()
    resetAddonsForTests()
    vi.stubGlobal('fetch', vi.fn(async () => json({ id: 2, name: 'Test', avatar: 'grad-1', hasPassword: false, isAdmin: false, isKid: false, maxRating: null })))
    await session.login(2)
  })
  afterEach(() => vi.unstubAllGlobals())

  it('lists episodes with source tags', async () => {
    serve(guide([ep(1, { watched: true, sources: [lib()] }), ep(2, { new: true, sources: [lib(), online('alpha', 'Voice A'), online('alpha', 'Voice B')] })]))
    mount()
    const rows = await screen.findAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(within(rows[0]!).getByText('Диск 1080p')).toBeInTheDocument()
    expect(within(rows[0]!).getByText('просмотрено')).toBeInTheDocument()
    expect(within(rows[1]!).getByText('новое')).toBeInTheDocument()
    // two online sources of one provider and quality are one tag
    expect(within(rows[1]!).getAllByText('alpha 720p')).toHaveLength(1)
  })

  it('keeps to the page gutter like the sections around it', async () => {
    serve(guide([ep(1, { sources: [lib()] })]))
    mount()
    await screen.findAllByRole('listitem')
    const section = screen.getByRole('region', { name: 'Серии' })
    expect(section).toHaveClass('guide')
    expect(rule('components/episodes/episodes.module.css', '.guide')).toContain('padding: 0 var(--gutter)')
  })

  it('shows at most four tags then +N', async () => {
    serve(guide([ep(1, { sources: [lib(), online('a', 'x'), online('b', 'x'), online('c', 'x'), online('d', 'x'), online('e', 'x')] })]))
    mount()
    expect(await screen.findByText('+2')).toBeInTheDocument()
  })

  it('upcoming episode shows its date and is not playable', async () => {
    serve(guide([ep(1, { sources: [lib()] }), ep(2, { airDate: '2099-11-15' })]))
    mount()
    expect(await screen.findByText('выйдет 15 ноября')).toBeInTheDocument()
    const rows = screen.getAllByRole('listitem')
    expect(within(rows[1]!).queryByRole('button')).toBeNull()
  })

  it('an episode with nothing to play or fetch opens no empty sheet', async () => {
    // no addon player: a kid, who plays the library alone
    serve(guide([ep(1, { sources: [lib()] }), ep(2)]))
    mount()
    expect(await screen.findByText('нет источников')).toBeInTheDocument()
    const rows = screen.getAllByRole('listitem')
    expect(within(rows[1]!).queryByRole('button')).toBeNull()
    fireEvent.contextMenu(rows[1]!)
    expect(screen.queryByRole('dialog')).toBeNull()
    // with other sources to look for, the row still opens the sheet
    cleanup()
    onlineAddon(fakePlay())
    serve(guide([ep(1, { sources: [lib()] }), ep(2)]))
    mount()
    await screen.findByText('нет источников')
    expect(within(screen.getAllByRole('listitem')[1]!).getByRole('button', { name: 'Выбрать источник' })).toBeInTheDocument()
  })

  it('tap plays the remembered choice', async () => {
    const play = fakePlay()
    onlineAddon(play)
    const remembered = online('alpha', 'Voice A')
    serve(guide([ep(1, { sources: [lib(), remembered, online('alpha', 'Voice B')] })]), choiceOf(remembered))
    mount()
    await userEvent.click(await screen.findByRole('button', { name: /Name 1/ }))
    await waitFor(() => expect(play.play).toHaveBeenCalledTimes(1))
    expect(play.play.mock.calls[0]![1]).toMatchObject({ kind: 'online', meta: { providerId: 'alpha', voice: 'Voice A' } })
  })

  it('tap plays the library file in the app', async () => {
    serve(guide([ep(1, { sources: [lib()] })]))
    mount()
    await userEvent.click(await screen.findByRole('button', { name: /Name 1/ }))
    expect(screen.getByTestId('where')).toHaveTextContent('/watch/7?file=70')
  })

  it('library without a file id opens the item without ?file=', async () => {
    serve(guide([ep(1, { sources: [lib({ meta: { itemId: 7 } })] })]))
    mount()
    await userEvent.click(await screen.findByRole('button', { name: /Name 1/ }))
    expect(screen.getByTestId('where')).toHaveTextContent(/^\/watch\/7$/)
  })

  it('library play goes through onPlayLibrary when given', async () => {
    const onPlayLibrary = vi.fn()
    serve(guide([ep(1, { sources: [lib()] })]))
    mount({ onPlayLibrary })
    await userEvent.click(await screen.findByRole('button', { name: /Name 1/ }))
    expect(onPlayLibrary).toHaveBeenCalledWith(7, 70)
  })

  it('tap falls back to library when no player plays the source', async () => {
    const remembered = online('alpha', 'Voice A')
    serve(guide([ep(1, { sources: [lib(), remembered] })]), choiceOf(remembered))
    mount()
    await userEvent.click(await screen.findByRole('button', { name: /Name 1/ }))
    expect(screen.getByTestId('where')).toHaveTextContent('/watch/7?file=70')
  })

  it('tap opens the sheet when nothing can play here', async () => {
    serve(guide([ep(1, { sources: [online('alpha', 'Voice A')] })]))
    mount()
    await userEvent.click(await screen.findByRole('button', { name: /Name 1/ }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })

  it('long press opens the sheet grouped by language and dub', async () => {
    onlineAddon(fakePlay())
    serve(guide([ep(1, { sources: [lib(), online('alpha', 'Voice A'), online('beta', 'Voice B', { meta: { providerId: 'beta', voice: 'Voice B', lang: 'en' } })] })]))
    mount()
    const row = await screen.findByRole('button', { name: /Name 1/ })
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    fireEvent.pointerDown(row, { pointerType: 'touch', clientX: 1, clientY: 1 })
    act(() => void vi.advanceTimersByTime(600))
    vi.useRealTimers()
    const sheet = await screen.findByRole('dialog')
    expect(within(sheet).getByText('Русский')).toBeInTheDocument()
    expect(within(sheet).getByText('Английский')).toBeInTheDocument()
    expect(within(sheet).getByText('Другое')).toBeInTheDocument()
    expect(within(sheet).getByText('Voice A')).toBeInTheDocument()
    expect(within(sheet).getByText('Voice B')).toBeInTheDocument()
  })

  it('▾ opens the sheet', async () => {
    serve(guide([ep(1, { sources: [lib()] })]))
    mount()
    await userEvent.click(await screen.findByRole('button', { name: 'Выбрать источник' }))
    expect(await screen.findByRole('dialog', { name: 'Серия 1 · Name 1' })).toBeInTheDocument()
  })

  it('picking in the sheet saves the choice and plays', async () => {
    const play = fakePlay()
    onlineAddon(play)
    const a = online('alpha', 'Voice A')
    const puts = serve(guide([ep(1, { sources: [lib(), a] })]))
    mount()
    await userEvent.click(await screen.findByRole('button', { name: 'Выбрать источник' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: /alpha/ }))
    await waitFor(() => expect(puts).toEqual([choiceOf(a)]))
    expect(play.play.mock.calls[0]![1]).toMatchObject({ ref: a.ref })
  })

  it('library source offers «На устройство» with its item and file', async () => {
    const onDownloadLibrary = vi.fn()
    serve(guide([ep(1, { sources: [lib()] })]))
    mount({ onDownloadLibrary })
    await userEvent.click(await screen.findByRole('button', { name: 'Выбрать источник' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'На устройство' }))
    expect(onDownloadLibrary).toHaveBeenCalledWith(7, 70)
    // without the callback (a limited kid): no row
    cleanup()
    serve(guide([ep(1, { sources: [lib()] })]))
    mount()
    await userEvent.click(await screen.findByRole('button', { name: 'Выбрать источник' }))
    expect(within(await screen.findByRole('dialog')).queryByRole('button', { name: 'На устройство' })).not.toBeInTheDocument()
  })

  it('always offers other sources', async () => {
    const play = fakePlay()
    onlineAddon(play)
    serve(guide([ep(1, { sources: [lib()] })]))
    mount()
    await userEvent.click(await screen.findByRole('button', { name: 'Выбрать источник' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Другие источники…' }))
    expect(play.other).toHaveBeenCalledTimes(1)
  })

  it('opens on the season of the first unwatched aired episode', async () => {
    serve(guide([ep(1, { watched: true, sources: [lib()] }), ep(1, { season: 2, name: 'Second', sources: [lib()] })]))
    mount()
    expect(await screen.findByRole('button', { name: /Second/ })).toBeInTheDocument()
  })

  it('renders nothing when the endpoint 404s', async () => {
    serve(null)
    const { container } = mount()
    await waitFor(() => expect(vi.mocked(fetch).mock.calls.some(([u]) => String(u).endsWith('/episodes'))).toBe(true))
    await act(async () => {})
    expect(container.querySelector('section')).toBeNull()
    expect(screen.queryByRole('listitem')).toBeNull()
  })

  // an addon's own source kind (its scanners'), played by its episodeActions entry
  const extra = (over: Partial<EpisodeSource> = {}): EpisodeSource => ({
    kind: 'extra' as SourceKind, ref: 'x:1', provider: 'Дополнение', labels: [], firstSeen: '2026-01-01', meta: {}, ...over,
  })

  for (const bad of [-1, 0]) {
    it(`the library ranks first even when an addon's order says ${bad} (tap and sheet)`, async () => {
      const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
      const mine = fakePlay()
      registerAddon('fake', { episodeActions: [{ kinds: ['extra'], order: { extra: bad }, use: () => mine }] })
      const onPlayLibrary = vi.fn()
      const disk = lib({ quality: '480p' })
      serve(guide([ep(1, { sources: [extra({ quality: '2160p' }), disk] })]))
      mount({ onPlayLibrary })
      await userEvent.click(await screen.findByRole('button', { name: /Name 1/ }))
      expect(onPlayLibrary).toHaveBeenCalledWith(7, 70)
      expect(mine.play).not.toHaveBeenCalled()
      await userEvent.click(screen.getByRole('button', { name: 'Выбрать источник' }))
      const rows = within(screen.getByRole('dialog')).getAllByRole('button', { pressed: true }).concat(within(screen.getByRole('dialog')).queryAllByRole('button', { pressed: false }))
      expect(rows.map((r) => r.textContent)).toEqual([expect.stringContaining('Диск'), expect.stringContaining('Дополнение')])
      expect(warn).toHaveBeenCalledWith(expect.stringMatching(/addon fake: episodeActions order of "extra" dropped/))
      warn.mockRestore()
    })
  }

  it('an addon-kind row uses the addon\'s episode action', async () => {
    const viaOnline = fakePlay()
    onlineAddon(viaOnline)
    const mine = fakePlay({ node: <p>Узел дополнения</p> })
    registerAddon('fake', { episodeActions: [{ kinds: ['extra'], use: () => mine }] })
    serve(guide([ep(1, { sources: [extra()] }), ep(2, { sources: [online('alpha', 'Voice A')] })]))
    mount()
    await userEvent.click(await screen.findByRole('button', { name: /Name 1/ }))
    expect(mine.play).toHaveBeenCalledTimes(1)
    expect(mine.play.mock.calls[0]![1]).toMatchObject({ kind: 'extra' })
    expect(viaOnline.play).not.toHaveBeenCalled()
    // the online kind goes through its own addon's player
    await userEvent.click(screen.getByRole('button', { name: /Name 2/ }))
    expect(viaOnline.play).toHaveBeenCalledTimes(1)
    // the Host draws the addon's node; the guide does not draw it again
    expect(screen.getAllByText('Узел дополнения')).toHaveLength(1)
  })

  it('a source of a kind with no live player is not tapped dead: another playable source plays, else the sheet opens', async () => {
    const viaOnline = fakePlay()
    onlineAddon(viaOnline)
    // `pinned` (an addon's kind whose addon is gone) ranks above online
    const gone = extra({ kind: 'pinned' as SourceKind, ref: 'p:1' })
    serve(guide([ep(1, { sources: [gone, online('alpha', 'Voice A')] }), ep(2, { sources: [extra({ kind: 'pinned' as SourceKind, ref: 'p:2' })] })]))
    mount()
    await userEvent.click(await screen.findByRole('button', { name: /Name 1/ }))
    expect(viaOnline.play).toHaveBeenCalledTimes(1)
    expect(viaOnline.play.mock.calls[0]![1]).toMatchObject({ kind: 'online' })
    // nothing playable: the sheet, and the online player is not called with it
    await userEvent.click(screen.getByRole('button', { name: /Name 2/ }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    expect(viaOnline.play).toHaveBeenCalledTimes(1)
  })

  it('an addon registered late still gets its rows', async () => {
    serve(guide([ep(1, { sources: [extra()] })]))
    mount()
    const row = await screen.findByRole('button', { name: /Name 1/ })
    const mine = fakePlay()
    act(() => registerAddon('fake', { episodeActions: [{ kinds: ['extra'], use: () => mine }] }))
    await userEvent.click(row)
    expect(mine.play).toHaveBeenCalledTimes(1)
  })

  it('a use() answering a new object each render neither loops nor loses its rows', async () => {
    let calls = 0
    const played: string[] = []
    registerAddon('fake', {
      episodeActions: [
        {
          kinds: ['extra'],
          use: (ctx) => {
            calls++
            return { play: (e) => void played.push(`${ctx.title}:${e.number}`), other: () => {}, node: null }
          },
        },
      ],
    })
    serve(guide([ep(1, { sources: [extra()] })]))
    mount()
    await userEvent.click(await screen.findByRole('button', { name: /Name 1/ }))
    expect(played).toEqual(['Show:1'])
    expect(calls).toBeLessThan(10)
  })

  it('an addon whose use() throws drops its kinds; the guide stays', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    registerAddon('fake', {
      episodeActions: [
        {
          kinds: ['extra'],
          use: () => {
            throw new Error('boom')
          },
        },
      ],
    })
    serve(guide([ep(1, { sources: [extra(), lib()] })]))
    mount()
    await userEvent.click(await screen.findByRole('button', { name: /Name 1/ }))
    // the library source plays instead
    expect(screen.getByTestId('where')).toHaveTextContent('/watch/7')
    vi.restoreAllMocks()
  })

  it('«Другие источники…» goes to the addon whose kind the guide lists, the best ranked first', async () => {
    const mine = fakePlay()
    // registered first, and with no place on the scale
    registerAddon('fake', { episodeActions: [{ kinds: ['extra'], use: () => mine }] })
    const viaOnline = fakePlay()
    onlineAddon(viaOnline)
    const other = async () => {
      await userEvent.click(await screen.findByRole('button', { name: 'Выбрать источник' }))
      await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Другие источники…' }))
    }
    serve(guide([ep(1, { sources: [online('alpha', 'Voice A'), extra()] })]))
    const first = mount()
    await other()
    expect(viaOnline.other).toHaveBeenCalledTimes(1)
    expect(mine.other).not.toHaveBeenCalled()
    first.unmount()

    // the guide lists only the other addon's kind: that addon, whatever the scale says
    serve(guide([ep(1, { sources: [extra()] })]))
    const second = mount()
    await other()
    expect(mine.other).toHaveBeenCalledTimes(1)
    expect(viaOnline.other).toHaveBeenCalledTimes(1)
    second.unmount()

    // the guide lists neither: the best ranked player (online: 20 beats the unranked)
    serve(guide([ep(1, { sources: [lib()] })]))
    mount()
    await other()
    expect(viaOnline.other).toHaveBeenCalledTimes(2)
    expect(mine.other).toHaveBeenCalledTimes(1)
  })
})
