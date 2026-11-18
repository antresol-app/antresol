// The addon SDK: what an addon bundle gets as `@antresol/addon-sdk`
// (globalThis.__antresol.shared). This module's namespace IS the SDK surface.
import { useMemo } from 'react'
import { ApiError, generic, getDisk, getItem, getTitle, imageUrl, offline, readResponse, request } from '../api/client'
export { ApiError } from '../api/client'
import { getLanguage, useLang, type Lang } from '../i18n'
import { useSession } from '../state/session'
import { audio } from '../player/audio/engine'
import { useWatchWay as useProfileWatchWay } from '../state/watchWay'
import { addonString } from './registry'

export type {
  AddonContributions,
  AddonRoute,
  AddonRouteProps,
  EmptyStateAction,
  EpisodeAction,
  ImportSource,
  ImportSourcesProps,
  ItemAction,
  ItemPanel,
  ManageTile,
  MigrateSourceEntry,
  NavBadge,
  PlayerNote,
  PlayerTrackAction,
  ProfileMenuEntry,
  RequestAction,
  SearchBlock,
  StatsBlock,
  TrackActionItem,
  WayTitle,
} from './registry'
export { registerAddon } from './registry'
// every addon's «Смотреть ▾» ways for a title (another addon's rows draw them)
export { useWatchWays } from './watchWays'
// the design system, and the core's video player behind React.lazy (ui.Player)
export * as ui from './sdkUi'
export type { PlayerProps, StreamPlayback, UpNext } from './sdkUi'
export { Icon, type IconName } from '../components/Icon'
export { useEvents } from '../api/events'
export type { EpisodeSource, ItemDetail, KidRequest, PendingState, PlayerInput, ServerEvent, TitleEpisode, TitleInfo } from '../api/types'
// what ui.Player plays: a part, a play request and its answer, a stream's qualities
export type { MediaFile, PlayRequest, PlayResponse, StreamQualityInfo } from '../api/types'
export type { EpisodeCtx, EpisodePlay } from '../components/episodes/episodeTypes'
// «Переезд»: the core's page, reusable over an addon's own API and part
export { Migrate as MigratePage } from '../pages/manage/Migrate'
export type { MigrateApi, MigrateCtx, MigrateFullApi, MigrateProps, MigrateText, SourceMeta } from '../pages/manage/Migrate'
export type { ImportJob, ImportService, MigrateReport } from '../api/types'
// the core's own buttons and search helpers (neutral)
export { DownloadButton, type DownloadState } from '../components/DownloadButton'
export { WatchButton, type WatchWay } from '../components/WatchButton'
export { cardHref } from '../components/Card'
export { PendingOverlay } from '../components/PendingOverlay'
export { titlePath } from '../components/RequestButton'
export { KIND_ICON, kindLabel, useDebounced, type Found } from '../state/searchKinds'
// helpers for an addon's own list pages (neutral, the core's own functions)
export { formatSize, formatRate } from '../state/format'
export { formatPercent, locale } from '../i18n'
export { useIsCompact } from '../state/useMediaQuery'
// the core's pill choices (a radio group or toggles), the one chip style
export { Chip, Chips, type ChipProps } from '../components/Chips'
// the player's language and time helpers: a track's badge («RU») and name, a clock («1:02:03»)
export { clock, langBadge, langCode, languageName } from '../player/timeline'
export {
  ViewMenu,
  ViewToolbar,
  useUrlView,
  useMediaSegments,
  MEDIA_SEGMENTS,
  mediaSegmentIcon,
  mediaSegmentOf,
  mediaKindIcon,
  type MediaSegment,
  type ViewSpec,
} from '../components/ViewMenu'

/**
 * Who is signed in (null: nobody yet), with the interface language and
 * whether the session is away from the home network (`remote`: a source
 * quality's default); re-renders when that changes.
 */
export function useUser(): { id: number; name: string; isAdmin: boolean; isKid: boolean; lang: Lang; remote: boolean } | null {
  const { user } = useSession()
  const lang = useLang()
  return useMemo(
    () => (user ? { id: user.id, name: user.name, isAdmin: user.isAdmin, isKid: user.isKid, lang, remote: Boolean(user.remote) } : null),
    [user, lang],
  )
}

/**
 * Pauses the music and hands the system media controls back: what a page
 * playing its own video calls when it opens (ui.Player does not), so the
 * music does not play on under it.
 */
export function yieldSession(): void {
  audio.yieldSession()
}

/**
 * The «Смотреть ▾» way the signed-in profile chose last (an addon way's
 * `<addonId>:<label>`), read only and live: it re-renders when
 * the profile chooses another way, here or in another tab. null: none
 * chosen, nobody signed in, or a kid.
 */
export function useWatchWay(): string | null {
  const { user } = useSession()
  const way = useProfileWatchWay(user?.id ?? 0)
  return user && !user.isKid ? way : null
}

/** Read helpers of the core's own API an addon page may use (a library item, a title's page, an image URL, the disk). */
export const core = { getItem, getTitle, imageUrl, getDisk }

/**
 * A path under the addon's own API: `admin/sources/3`, optionally with one
 * trailing `?query`. Refused (the call rejects, nothing is sent): a leading
 * `/`, a scheme (`https:`), an empty segment (`a//b`, `//host`), a `#`, a
 * backslash, a space or control character (raw or %-encoded: fetch strips
 * tabs and line breaks before it parses the URL), and a `.` or `..` segment
 * in any spelling (`%2e`, `%2E%2e`…), so a path never leaves /api/addons/<id>/x/.
 */
// a space, a control character or DEL
const CONTROL = /[\u0000-\u0020\u007f]/

function addonPath(id: string, p: string): string {
  const q = p.indexOf('?')
  const path = q < 0 ? p : p.slice(0, q)
  const segs = path.split('/')
  const bad =
    path === '' ||
    path.startsWith('/') ||
    /^[a-z][a-z0-9+.-]*:/i.test(path) ||
    /[#\\]/.test(p) ||
    CONTROL.test(p) ||
    segs.some((seg, i) => {
      if (seg === '') return i < segs.length - 1 // one trailing slash is fine
      let d: string
      try {
        d = decodeURIComponent(seg)
      } catch {
        return true
      }
      return d === '.' || d === '..' || /[/\\]/.test(d) || CONTROL.test(d)
    })
  if (bad) throw new Error(`addonApi: refused path ${JSON.stringify(p)}`)
  return `/api/addons/${encodeURIComponent(id)}/x/${p}`
}

/**
 * The addon's own server API, through the core's proxy (/api/addons/<id>/x/<p>).
 * A failed call throws ApiError (its message is the server's `error`); a path
 * that would leave the addon's prefix rejects before anything is sent.
 */
export function addonApi(id: string) {
  const call = async <T>(method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE', p: string, body?: unknown, signal?: AbortSignal) =>
    request<T>(method, addonPath(id, p), body, signal)
  return {
    get: <T>(p: string, signal?: AbortSignal) => call<T>('GET', p, undefined, signal),
    // the proxy takes JSON only (the CSRF rule): no body is an empty object
    post: <T>(p: string, body?: unknown) => call<T>('POST', p, body ?? {}),
    put: <T>(p: string, body?: unknown) => call<T>('PUT', p, body ?? {}),
    patch: <T>(p: string, body?: unknown) => call<T>('PATCH', p, body ?? {}),
    del: <T>(p: string) => call<T>('DELETE', p, {}),
    /**
     * A GET whose answer is NDJSON: each non-empty line, JSON-parsed, goes
     * to `onLine` as it arrives, in order. Resolves at the end of the body;
     * rejects with ApiError on a non-2xx answer (its `error`) or a line that
     * is not JSON, and before anything is sent on a refused path. An abort
     * stops the read (the AbortError).
     */
    stream: async <T>(p: string, onLine: (line: T) => void, signal?: AbortSignal): Promise<void> => {
      const path = addonPath(id, p)
      const cut = (e: unknown): never => {
        if (e instanceof DOMException && e.name === 'AbortError') throw e
        throw new ApiError(0, offline())
      }
      let res: Response
      try {
        res = await fetch(path, {
          method: 'GET',
          credentials: 'same-origin',
          headers: { Accept: 'application/x-ndjson, application/json', 'Accept-Language': getLanguage() },
          signal,
        })
      } catch (e) {
        return cut(e)
      }
      if (!res.ok) {
        await readResponse(res, path)
        throw new ApiError(res.status, generic())
      }
      if (!res.body) return
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      const deliver = (line: string) => {
        if (!line.trim()) return
        let v: T
        try {
          v = JSON.parse(line) as T
        } catch {
          throw new ApiError(res.status, generic())
        }
        onLine(v)
      }
      try {
        for (;;) {
          if (signal?.aborted) throw new DOMException('aborted', 'AbortError')
          let chunk: ReadableStreamReadResult<Uint8Array>
          try {
            chunk = await reader.read()
          } catch (e) {
            return cut(e)
          }
          buf += decoder.decode(chunk.value, { stream: !chunk.done })
          const lines = buf.split('\n')
          buf = chunk.done ? '' : lines.pop()!
          for (const line of lines) deliver(line)
          if (chunk.done) return
        }
      } finally {
        reader.cancel().catch(() => undefined)
      }
    },
  }
}

/** The addon's strings (passed to registerAddon) in the interface language; a missing one shows its key. */
export function useAddonT(id: string): (key: string, vars?: Record<string, string | number>) => string {
  const lang = useLang()
  return useMemo(() => (key, vars) => addonString(id, lang, key, vars), [id, lang])
}
