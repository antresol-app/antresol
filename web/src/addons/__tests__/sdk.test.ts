import { afterEach, describe, expect, it, vi } from 'vitest'
import { createElement, Profiler, useSyncExternalStore } from 'react'
import * as sdk from '../sdk'
import * as client from '../../api/client'
import * as format from '../../state/format'
import * as i18n from '../../i18n'
import * as media from '../../state/useMediaQuery'
import * as view from '../../components/ViewMenu'
import * as download from '../../components/DownloadButton'
import * as pendingOverlay from '../../components/PendingOverlay'
import * as watch from '../../components/WatchButton'
import * as card from '../../components/Card'
import * as request from '../../components/RequestButton'
import * as kinds from '../../state/searchKinds'
import { act, render, renderHook, screen, waitFor } from '@testing-library/react'
import { registerAddon, resetAddonsForTests } from '../registry'
import { AddonWaysOutlet } from '../watchWays'
import * as watchWay from '../../state/watchWay'
import { session } from '../../state/session'
import * as migrate from '../../pages/manage/Migrate'
import type * as registry from '../registry'
import * as timeline from '../../player/timeline'
import * as chips from '../../components/Chips'
import * as badge from '../../ui/Badge'
import { audio } from '../../player/audio/engine'
import type * as player from '../../player/VideoPlayer'
import type * as types from '../../api/types'

// the 2f types an addon's «Переезд» page and import sign-ins are typed with:
// each is the core's own (tsc fails here when one is gone or another)
type Same<A, B> = [A] extends [B] ? ([B] extends [A] ? true : false) : false
export const sdkTypes2f = {
  MigrateApi: true satisfies Same<sdk.MigrateApi, migrate.MigrateApi>,
  MigrateFullApi: true satisfies Same<sdk.MigrateFullApi, migrate.MigrateFullApi>,
  MigrateCtx: true satisfies Same<sdk.MigrateCtx, migrate.MigrateCtx>,
  MigrateReport: true satisfies Same<sdk.MigrateReport, types.MigrateReport>,
  SourceMeta: true satisfies Same<sdk.SourceMeta, migrate.SourceMeta>,
  MigrateText: true satisfies Same<sdk.MigrateText, migrate.MigrateText>,
  MigrateSourceEntry: true satisfies Same<sdk.MigrateSourceEntry, registry.MigrateSourceEntry>,
  ImportSource: true satisfies Same<sdk.ImportSource, registry.ImportSource>,
  ImportSourcesProps: true satisfies Same<sdk.ImportSourcesProps, registry.ImportSourcesProps>,
}

describe('addon sdk', () => {
  afterEach(() => vi.restoreAllMocks())

  it('exposes the page helpers, the same functions as the core modules', () => {
    expect(sdk.core.getItem).toBe(client.getItem)
    expect(sdk.core.imageUrl).toBe(client.imageUrl)
    expect(sdk.core.getDisk).toBe(client.getDisk)
    expect(sdk.formatSize).toBe(format.formatSize)
    expect(sdk.formatRate).toBe(format.formatRate)
    expect(sdk.formatPercent).toBe(i18n.formatPercent)
    expect(sdk.locale).toBe(i18n.locale)
    expect(sdk.useIsCompact).toBe(media.useIsCompact)
    expect(sdk.ViewMenu).toBe(view.ViewMenu)
    expect(sdk.ViewToolbar).toBe(view.ViewToolbar)
    expect(sdk.useUrlView).toBe(view.useUrlView)
    expect(sdk.useMediaSegments).toBe(view.useMediaSegments)
    expect(sdk.MEDIA_SEGMENTS).toBe(view.MEDIA_SEGMENTS)
    expect(sdk.mediaSegmentIcon).toBe(view.mediaSegmentIcon)
    expect(sdk.mediaSegmentOf).toBe(view.mediaSegmentOf)
    expect(sdk.mediaKindIcon).toBe(view.mediaKindIcon)
  })

  it('exports the reusable «Переезд» page', () => {
    expect(sdk.MigratePage).toBe(migrate.Migrate)
  })

  it('core holds only the read helpers it names', () => {
    expect(Object.keys(sdk.core).sort()).toEqual(['getDisk', 'getItem', 'getTitle', 'imageUrl'])
  })
  it('addonApi sends every method through the proxy with a JSON body', async () => {
    const calls: { url: string; method: string; body: unknown }[] = []
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
      calls.push({ url: String(url), method: String(init?.method), body: init?.body })
      return new Response(null, { status: 204 })
    })
    const api = sdk.addonApi('hello')
    await api.get('admin/items')
    await api.post('admin/items/ab/pause')
    await api.put('admin/settings', { a: 1 })
    await api.patch('admin/things/3', { enabled: false })
    await api.del('admin/things/3')
    expect(calls.map((c) => `${c.method} ${c.url} ${c.body ?? ''}`)).toEqual([
      'GET /api/addons/hello/x/admin/items ',
      'POST /api/addons/hello/x/admin/items/ab/pause {}',
      'PUT /api/addons/hello/x/admin/settings {"a":1}',
      'PATCH /api/addons/hello/x/admin/things/3 {"enabled":false}',
      'DELETE /api/addons/hello/x/admin/things/3 {}',
    ])
  })

  it('a failed call is the core ApiError with the server text', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ error: 'Задано на сервере' }), { status: 409 }))
    const err = await sdk.addonApi('hello').put('admin/settings', { on: false }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(sdk.ApiError)
    expect(err).toBeInstanceOf(client.ApiError)
    expect((err as client.ApiError).message).toBe('Задано на сервере')
    expect((err as client.ApiError).status).toBe(409)
  })
  it('addonApi keeps every path under the addon prefix, refusing the rest before a request', async () => {
    const urls: string[] = []
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (url) => {
      urls.push(String(url))
      return new Response(null, { status: 204 })
    })
    const api = sdk.addonApi('hello')
    for (const p of [
      '../../settings',
      'a/../b',
      '..',
      './a',
      'a/./b',
      '%2e%2e/settings',
      'a/%2E%2e/b',
      '%2e/a',
      '/api/settings',
      '//elsewhere.example/x',
      'https://elsewhere.example/x',
      'a//b',
      'a\\b',
      'a#frag',
      'a?x=1#frag',
      'a%2fb',
      'a%5cb',
      '%zz',
      '',
      '.\t./x',
      'a/.\n./b',
      '..\r',
      'a b',
      'a\u007f',
      'a%09b',
      '.%0a./x',
      'a?q=\t',
    ]) {
      await expect(api.get(p), p).rejects.toThrow(/refused path/)
      await expect(api.del(p), p).rejects.toThrow(/refused path/)
    }
    expect(urls).toEqual([])
    for (const p of ['items', 'admin/items/ab/pause', 'admin/', 'items?q=a%20b&x=..', 'items/%41b']) await api.get(p)
    expect(urls).toEqual([
      '/api/addons/hello/x/items',
      '/api/addons/hello/x/admin/items/ab/pause',
      '/api/addons/hello/x/admin/',
      '/api/addons/hello/x/items?q=a%20b&x=..',
      '/api/addons/hello/x/items/%41b',
    ])
  })

  it('exposes the pending overlay, the core component itself', () => {
    expect(sdk.PendingOverlay).toBe(pendingOverlay.PendingOverlay)
  })

  it('exposes the 2d helpers', () => {
    expect([sdk.DownloadButton, sdk.WatchButton, sdk.cardHref, sdk.titlePath, sdk.KIND_ICON, sdk.kindLabel, sdk.useDebounced]).toEqual([
      download.DownloadButton, watch.WatchButton, card.cardHref, request.titlePath, kinds.KIND_ICON, kinds.kindLabel, kinds.useDebounced,
    ])
  })

  it('useUser is who is signed in, null before', async () => {
    session.reset()
    expect(renderHook(() => sdk.useUser()).result.current).toBeNull()
    const user = { id: 4, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
    vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify(user), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await session.login(4)
    expect(renderHook(() => sdk.useUser()).result.current).toEqual({ id: 4, name: 'Мама', isAdmin: false, isKid: false, lang: 'ru', remote: false })
    session.reset()
  })

  it('useWatchWay is the signed-in profile\'s remembered way, live; null before a sign-in and for a kid', async () => {
    session.reset()
    localStorage.clear()
    expect(renderHook(() => sdk.useWatchWay()).result.current).toBeNull()
    const user = { id: 4, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
    vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify(user), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await session.login(4)
    const { result } = renderHook(() => sdk.useWatchWay())
    expect(result.current).toBeNull()
    act(() => watchWay.writeWatchWay(4, 'fake:watch.now'))
    expect(result.current).toBe('fake:watch.now')
    // another profile's choice is not this one's
    act(() => watchWay.writeWatchWay(5, 'online'))
    expect(result.current).toBe('fake:watch.now')
    session.reset()
    vi.restoreAllMocks()
    const kid = { ...user, id: 5, isKid: true }
    vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify(kid), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await session.login(5)
    expect(renderHook(() => sdk.useWatchWay()).result.current).toBeNull()
    session.reset()
    localStorage.clear()
  })

  describe('addonApi.stream', () => {
    const body = (chunks: string[]) =>
      new ReadableStream<Uint8Array>({
        start(c) {
          for (const ch of chunks) c.enqueue(new TextEncoder().encode(ch))
          c.close()
        },
      })

    it('stream delivers each NDJSON line in order', async () => {
      const urls: string[] = []
      vi.spyOn(globalThis, 'fetch').mockImplementation(async (url) => {
        urls.push(String(url))
        return new Response(body(['{"n":1}\n{"n"', ':2}\n\n', '{"n":3}']), { status: 200, headers: { 'Content-Type': 'application/x-ndjson' } })
      })
      const got: unknown[] = []
      await sdk.addonApi('dl').stream<{ n: number }>('x/list?stream=1', (l) => got.push(l))
      expect(got).toEqual([{ n: 1 }, { n: 2 }, { n: 3 }])
      expect(urls).toEqual(['/api/addons/dl/x/x/list?stream=1'])
    })

    it('a non-2xx answer rejects with ApiError', async () => {
      vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ error: 'Нет связи' }), { status: 502 }))
      const err = await sdk.addonApi('dl').stream('list', () => {}).catch((e: unknown) => e)
      expect(err).toBeInstanceOf(sdk.ApiError)
      expect((err as client.ApiError).status).toBe(502)
      expect((err as client.ApiError).message).toBe('Нет связи')
    })

    it('a non-JSON line rejects', async () => {
      vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(body(['{"n":1}\nnot json\n']), { status: 200 }))
      const got: unknown[] = []
      const err = await sdk.addonApi('dl').stream('list', (l) => got.push(l)).catch((e: unknown) => e)
      expect(err).toBeInstanceOf(sdk.ApiError)
      expect(got).toEqual([{ n: 1 }])
    })

    it('a refused path rejects before fetch', async () => {
      const f = vi.spyOn(globalThis, 'fetch')
      await expect(sdk.addonApi('dl').stream('../settings', () => {})).rejects.toThrow(/refused path/)
      expect(f).not.toHaveBeenCalled()
    })

    it('abort stops the read', async () => {
      const ctl = new AbortController()
      let pull = 0
      vi.spyOn(globalThis, 'fetch').mockImplementation(async (_url, init) => {
        const stream = new ReadableStream<Uint8Array>({
          pull(c) {
            pull++
            if (pull === 1) c.enqueue(new TextEncoder().encode('{"n":1}\n'))
            // then nothing more arrives until the abort
            return new Promise<void>((resolve) => init?.signal?.addEventListener('abort', () => {
              c.error(new DOMException('aborted', 'AbortError'))
              resolve()
            }))
          },
        })
        return new Response(stream, { status: 200 })
      })
      const got: unknown[] = []
      const done = sdk.addonApi('dl').stream('list', (l) => {
        got.push(l)
        ctl.abort()
      }, ctl.signal)
      const err = await done.catch((e: unknown) => e)
      expect(err).toBeInstanceOf(DOMException)
      expect((err as DOMException).name).toBe('AbortError')
      expect(got).toEqual([{ n: 1 }])
    })
  })
})

// the 2e types are SDK names (tsc fails here when one is missing)
export type SdkTypes2e = [
  sdk.TitleInfo,
  sdk.EpisodeCtx,
  sdk.EpisodePlay,
  sdk.TitleEpisode,
  sdk.EpisodeSource,
  sdk.ItemPanel,
  sdk.EpisodeAction,
  sdk.PlayerTrackAction,
  sdk.TrackActionItem,
  sdk.ProfileMenuEntry,
]

// the stream player's types are the core's own (tsc fails here otherwise)
export const sdkTypesStream = {
  StreamPlayback: true satisfies Same<sdk.StreamPlayback, player.StreamPlayback>,
  UpNext: true satisfies Same<sdk.UpNext, player.UpNext>,
  MediaFile: true satisfies Same<sdk.MediaFile, types.MediaFile>,
  PlayRequest: true satisfies Same<sdk.PlayRequest, types.PlayRequest>,
  PlayResponse: true satisfies Same<sdk.PlayResponse, types.PlayResponse>,
  StreamQualityInfo: true satisfies Same<sdk.StreamQualityInfo, types.StreamQualityInfo>,
}

describe('addon sdk: what a stream player page needs', () => {
  afterEach(() => vi.restoreAllMocks())

  it('ui.Player is lazy (the main chunk does not import player/VideoPlayer)', () => {
    // a React.lazy component: the player's module loads on its first render only
    expect((sdk.ui.Player as unknown as { $$typeof: symbol }).$$typeof).toBe(Symbol.for('react.lazy'))
    // the rest of ui is the design system as it was
    expect(sdk.ui.Button).toBeDefined()
    expect(sdk.ui.Badges).toBe(badge.Badges)
  })

  it('the SDK exports yieldSession and the timeline helpers', () => {
    const pause = vi.spyOn(audio, 'yieldSession').mockImplementation(() => {})
    sdk.yieldSession()
    expect(pause).toHaveBeenCalledTimes(1)
    expect(sdk.langBadge).toBe(timeline.langBadge)
    expect(sdk.languageName).toBe(timeline.languageName)
    expect(sdk.langCode).toBe(timeline.langCode)
    expect(sdk.clock).toBe(timeline.clock)
    expect(sdk.Chips).toBe(chips.Chips)
    expect(sdk.Chip).toBe(chips.Chip)
  })

  it('the SDK exports Chips and ui.Badge', () => {
    // what the moved online sheet draws its choices and badges with
    expect(sdk.Chips).toBe(chips.Chips)
    expect(sdk.Chip).toBe(chips.Chip)
    expect(sdk.ui.Badge).toBe(badge.Badge)
    expect(sdk.ui.BadgeLabel).toBe(badge.BadgeLabel)
  })

  it('useUser says whether the profile is away from home (a source quality default)', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ id: 2, name: 'Мама', isAdmin: false, isKid: false, remote: true }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    await session.login(2)
    const { result } = renderHook(() => sdk.useUser())
    expect(result.current).toMatchObject({ id: 2, remote: true })
    vi.unstubAllGlobals()
    session.reset()
  })
})

describe('addon sdk: ways across addons', () => {
  const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
  const adult = { id: 4, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
  const signIn = async (u: typeof adult) => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async () => json(u))
    await session.login(u.id)
  }
  afterEach(() => {
    vi.restoreAllMocks()
    session.reset()
    resetAddonsForTests()
  })

  it("useWatchWays returns other addons' ways for a title, none for kids", async () => {
    const ran: string[] = []
    // the addon's answer (null: still asking), as a store its hook reads
    let answer: boolean | null = null
    const listeners = new Set<() => void>()
    const useAnswer = () =>
      useSyncExternalStore(
        (l) => (listeners.add(l), () => void listeners.delete(l)),
        () => answer,
      )
    const asked: unknown[] = []
    registerAddon('beta', {
      strings: { ru: { way: 'Способ Б' }, en: {} },
      itemActions: [
        {
          label: 'way',
          icon: 'play',
          watchWay: true,
          useAvailable: (t) => (asked.push(t), useAnswer()),
          run: (item) => void ran.push(`beta:${item.title}:${item.tmdbId}`),
        },
        // not a way: none of useWatchWays
        { label: 'Кнопка', icon: 'download', run: () => undefined },
      ],
    })
    registerAddon('alpha', {
      itemActions: [
        { label: 'Способ А', icon: 'play', watchWay: true, run: (item) => void ran.push(`alpha:${item.kind}`) },
        { label: 'Не для сериалов', icon: 'play', watchWay: true, visible: (item) => item.kind === 'movie', run: () => undefined },
      ],
    })
    await signIn(adult)
    const target = { kind: 'series' as const, tmdbId: 77, title: 'Сёгун', poster: '/p.jpg' }
    const outlet = render(createElement(AddonWaysOutlet))
    const { result } = renderHook(() => sdk.useWatchWays(target))
    // beta still asks: only alpha's way, in addon order
    expect(result.current.map((w) => [w.id, w.label])).toEqual([['alpha:Способ А', 'Способ А']])
    expect(asked).toContainEqual({ kind: 'series', tmdbId: 77, itemId: 0 })
    act(() => ((answer = true), listeners.forEach((l) => l())))
    await waitFor(() => expect(result.current.map((w) => w.id)).toEqual(['alpha:Способ А', 'beta:way']))
    act(() => result.current[1]!.run())
    act(() => result.current[0]!.run())
    expect(ran).toEqual(['beta:Сёгун:77', 'alpha:series'])
    outlet.unmount()

    // a kid: no way of any addon
    session.reset()
    vi.restoreAllMocks()
    await signIn({ ...adult, id: 5, isKid: true })
    expect(renderHook(() => sdk.useWatchWays(target)).result.current).toEqual([])
  })

  it('useWatchWays redraws the outlet only when what it draws changed', async () => {
    registerAddon('beta', {
      itemActions: [{ label: 'way', icon: 'play', watchWay: true, useAvailable: () => true, run: () => undefined }],
    })
    await signIn(adult)
    // each commit of the outlet (it renders only when a caller drew something new)
    let commits = 0
    render(createElement(Profiler, { id: 'outlet', onRender: () => void commits++ }, createElement(AddonWaysOutlet)))
    const target = { kind: 'movie' as const, tmdbId: 42, title: 'Дюна' }
    const { result, rerender } = renderHook(({ n }: { n: number }) => (void n, sdk.useWatchWays(target)), { initialProps: { n: 0 } })
    await waitFor(() => expect(result.current.map((w) => w.id)).toEqual(['beta:way']))
    const settled = commits
    // the caller renders again and again, with nothing new to draw
    for (let n = 1; n <= 3; n++) rerender({ n })
    expect(commits).toBe(settled)
  })

  it('useWatchWays: an element a way returns is drawn by the app until it closes', async () => {
    registerAddon('beta', {
      itemActions: [
        {
          label: 'way',
          icon: 'play',
          watchWay: true,
          run: (_item, close) => createElement('div', { role: 'dialog', 'aria-label': 'Лист' }, createElement('button', { onClick: close }, 'Закрыть')),
        },
      ],
    })
    await signIn(adult)
    render(createElement(AddonWaysOutlet))
    const { result } = renderHook(() => sdk.useWatchWays({ kind: 'movie', tmdbId: 42, title: 'Дюна' }))
    act(() => result.current[0]!.run())
    expect(await screen.findByRole('dialog', { name: 'Лист' })).toBeInTheDocument()
    act(() => screen.getByRole('button', { name: 'Закрыть' }).click())
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Лист' })).toBeNull())
  })
})
