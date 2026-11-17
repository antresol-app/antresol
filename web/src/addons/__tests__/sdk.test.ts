import { afterEach, describe, expect, it, vi } from 'vitest'
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
import { act, renderHook } from '@testing-library/react'
import * as watchWay from '../../state/watchWay'
import { session } from '../../state/session'
import * as migrate from '../../pages/manage/Migrate'
import type * as registry from '../registry'
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
    expect(renderHook(() => sdk.useUser()).result.current).toEqual({ id: 4, name: 'Мама', isAdmin: false, isKid: false, lang: 'ru' })
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
