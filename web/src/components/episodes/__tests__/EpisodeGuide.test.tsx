import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation } from 'react-router'
import type { SourceChoice, TitleEpisodes } from '../../../api/types'
import { choiceOf } from '../../../state/pickSource'
import { session } from '../../../state/session'
import { EpisodeGuide } from '../EpisodeGuide'
import type { EpisodePlay } from '../EpisodePlay'
import { rule } from '../../../ui/__tests__/cssRule'
import { ep, guide, lib, online } from './fixtures'

const h = vi.hoisted(() => ({ play: null as unknown }))
vi.mock('../EpisodePlay', () => ({ useEpisodePlay: () => h.play }))

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

describe('EpisodeGuide', () => {
  beforeEach(async () => {
    session.reset()
    h.play = null
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
    // h.play null: a kid, who plays the library alone
    serve(guide([ep(1, { sources: [lib()] }), ep(2)]))
    mount()
    expect(await screen.findByText('нет источников')).toBeInTheDocument()
    const rows = screen.getAllByRole('listitem')
    expect(within(rows[1]!).queryByRole('button')).toBeNull()
    fireEvent.contextMenu(rows[1]!)
    expect(screen.queryByRole('dialog')).toBeNull()
    // with other sources to look for, the row still opens the sheet
    cleanup()
    h.play = fakePlay()
    serve(guide([ep(1, { sources: [lib()] }), ep(2)]))
    mount()
    await screen.findByText('нет источников')
    expect(within(screen.getAllByRole('listitem')[1]!).getByRole('button', { name: 'Выбрать источник' })).toBeInTheDocument()
  })

  it('tap plays the remembered choice', async () => {
    const play = fakePlay()
    h.play = play
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

  it('tap falls back to library when this build cannot play', async () => {
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
    h.play = fakePlay()
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
    h.play = play
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
    h.play = play
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
})
