// Typed client for the Vault HTTP API. Every endpoint has one function here;
// pages never call fetch directly.
import type {
  About,
  Annotation,
  LibraryScope,
  TrickplayIndex,
  DeletePlan,
  DeleteResult,
  ImportGroup,
  ImportJob,
  ImportJobPatch,
  ImportResolve,
  ImportService,
  Card,
  PendingEntry,
  ReviewTrack,
  MigrateReport,
  MigrateRunRequest,
  MigrateState,
  MigrateStatus,
  AlbumDetail,
  AlbumSort,
  ArtistDetail,
  BookDetail,
  BookKind,
  BookSort,
  BooksHome,
  BooksPage,
  ComicPages,
  Lyrics,
  Playlist,
  PlaylistDetail,
  PlaylistPatch,
  SongsPage,
  Song,
  Genre,
  ShuffleSource,
  MusicHome,
  Candidate,
  KidRequest,
  TorrentEngineStatus,
  Settings,
  PlaybackPrefs,
  NotifyPrefs,
  TrackChoice,
  TrackPrefs,
  Stats,
  StorageReport,
  DiskBreakdown,
  TitleInfo,
  Device,
  DiskUsage,
  HlsStatus,
  Home,
  ItemDetail,
  FamilyMember,
  Ratings,
  RelatedRows,
  CollectionRow,
  ItemSort,
  ItemsPage,
  LibraryStatus,
  OnlineStatus,
  PlayRequest,
  PlayResponse,
  Profile,
  ScreenLimited,
  ScreenLimits,
  ScreenReport,
  ScreenStatus,
  SearchResult,
  SetupState,
  SubtitleResult,
  User,
} from './types'

import { getLanguage, t } from '../i18n'

export { useEvents } from './events'

/** An error returned by the API. `message` is already human-readable, in the interface language (Accept-Language). */
export class ApiError extends Error {
  readonly status: number
  /** The server's error code, e.g. 'no_format' (no release in «Формат загрузки»). */
  readonly code?: string
  constructor(status: number, message: string, code?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

/** What to tell the person when a request failed: the server's own words, else `fallback`. */
export const errorText = (e: unknown, fallback: string): string => (e instanceof ApiError ? e.message : fallback)

// (exported for ./clientFull, the sources build's endpoints)
export const offline = () => t('common.offlineRetry')
export const generic = () => t('common.genericError')

type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

// A 401 anywhere except the sign-in calls means the session has expired or was
// revoked; the session store subscribes and sends the app back to the picker.
const SIGN_IN_PATHS = ['/api/login', '/api/setup', '/api/me']
let unauthorized: (() => void) | null = null
export function onUnauthorized(fn: (() => void) | null) {
  unauthorized = fn
}

export async function request<T>(
  method: Method,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
  keepalive = false,
): Promise<T> {
  let res: Response
  try {
    const init: RequestInit = { method, credentials: 'same-origin', headers: { Accept: 'application/json', 'Accept-Language': getLanguage() } }
    // keepalive lets the request outlive the page (progress on close, HLS stop).
    if (keepalive) init.keepalive = true
    if (body instanceof FormData) {
      // multipart: the browser sets the boundary; the header is the server's
      // proof that this is not a cross-site form (it forces a preflight)
      init.body = body
      init.headers = { ...init.headers, 'X-Vault-Upload': '1' }
    } else if (body !== undefined) {
      init.body = JSON.stringify(body)
      init.headers = { ...init.headers, 'Content-Type': 'application/json' }
    }
    if (signal) init.signal = signal
    res = await fetch(path, init)
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    throw new ApiError(0, offline())
  }
  return readResponse<T>(res, path)
}

/** The JSON of a response, or its ApiError. */
export async function readResponse<T>(res: Response, path: string): Promise<T> {
  if (res.status === 204) return undefined as T
  const text = await res.text()
  let data: unknown = undefined
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = undefined
    }
  }
  if (!res.ok) {
    if (res.status === 401 && !SIGN_IN_PATHS.includes(path)) unauthorized?.()
    const msg =
      data && typeof data === 'object' && 'error' in data && typeof (data as { error: unknown }).error === 'string'
        ? (data as { error: string }).error
        : generic()
    const code = data && typeof data === 'object' && 'code' in data && typeof (data as { code: unknown }).code === 'string' ? (data as { code: string }).code : undefined
    throw new ApiError(res.status, msg, code)
  }
  return data as T
}

export function qs(params: Record<string, string | number | string[] | undefined>): string {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (Array.isArray(v)) for (const x of v) p.append(k, x)
    else if (v !== undefined && v !== '') p.set(k, String(v))
  }
  const s = p.toString()
  return s ? `?${s}` : ''
}

/** Resolves an image reference from the API to a URL the browser can load. */
export function imageUrl(ref: string | null | undefined, width?: number): string | undefined {
  if (!ref) return undefined
  const url = /^(\/|https?:|data:|blob:)/.test(ref) ? ref : `/api/images/${encodeURIComponent(ref)}`
  return width ? sized(url, width) : url
}

/** The widths the server shrinks an image to (?w=, internal/api/images.go imageWidths). */
export const IMAGE_WIDTHS = [160, 240, 320, 480, 640, 960, 1280, 1920] as const

/** The server's image at the smallest step at least `width` px wide; another URL (TMDB, a blob) as it is. */
function sized(url: string, width: number): string {
  if (!url.startsWith('/api/images/') || url.includes('?')) return url
  const w = IMAGE_WIDTHS.find((s) => s >= width)
  return w ? `${url}?w=${w}` : url
}

/**
 * A srcset of the server's image at the steps from `min` to `max` px, for
 * an <img> with `sizes`: the browser takes the step its layout and screen
 * need, so a phone gets a card's poster at ~500 px, not TMDB's 2000.
 * undefined for another URL (it has one size).
 */
export function imageSrcSet(ref: string | null | undefined, min = 240, max = 960): string | undefined {
  const url = imageUrl(ref)
  if (!url?.startsWith('/api/images/') || url.includes('?')) return undefined
  return IMAGE_WIDTHS.filter((w) => w >= min && w <= max)
    .map((w) => `${url}?w=${w} ${w}w`)
    .join(', ')
}

// --- System & auth ---
export const getAbout = () => request<About>('GET', '/api/about')
export const getSetup = () => request<SetupState>('GET', '/api/setup')
export const setup = (name: string, password: string) => request<User>('POST', '/api/setup', { name, password })
export const getProfiles = () => request<Profile[]>('GET', '/api/profiles')
export const login = (userId: number, password?: string) =>
  request<User>('POST', '/api/login', password ? { userId, password } : { userId })
export const logout = () => request<void>('POST', '/api/logout')
/** Whether the first getMe has run: only it may take the boot script's answer. */
let earlyLooked = false
/** Tests: the next getMe is the first again. */
export function resetEarlyMeForTests() {
  earlyLooked = false
}

/**
 * Who is signed in. The very first call takes the boot script's request
 * (boot/early.ts), already under way since the page was parsed, and leaves
 * null behind: a boot script that runs later sees it and does not ask, and
 * no later call ever gets the page-load answer.
 */
export const getMe = (): Promise<User> => {
  if (earlyLooked || typeof window === 'undefined') return request<User>('GET', '/api/me')
  earlyLooked = true
  const early = window.__vaultEarlyMe
  window.__vaultEarlyMe = null
  if (!early) return request<User>('GET', '/api/me')
  return early.then(
    (res) => readResponse<User>(res, '/api/me'),
    () => {
      throw new ApiError(0, offline())
    },
  )
}
/** The settings a profile changes for itself. */
export const updateMe = (patch: { hideProgress?: boolean }) => request<User>('PATCH', '/api/me', patch)

// --- Library ---
/** The `scope` query parameter: only «Моя медиатека» is sent (no parameter is the shared library). */
const scopeParam = (scope?: LibraryScope) => (scope === 'mine' ? 'mine' : undefined)
/** «Общая медиатека / Моя медиатека» of the profile, kept on the server; answers /api/me. */
export const setLibraryScope = (scope: LibraryScope) => request<User>('PUT', '/api/me/library-scope', { scope })
/** With the online entries of «Продолжить просмотр» (the server leaves them out of a plain /api/home). */
// ?online=1: the sources build's online titles in «Продолжить»
// ?family=1: «Семья смотрит» (the app's /api/home has no such shelf)
export const getHome = (signal?: AbortSignal, scope?: LibraryScope) =>
  request<Home>('GET', `/api/home${qs({ online: __LIBRARY_EDITION__ ? undefined : 1, family: 1, scope: scopeParam(scope) })}`, undefined, signal)
export interface ItemsOptions {
  sort?: ItemSort
  limit?: number
  offset?: number
  scope?: LibraryScope
}
export const getItems = (kind: 'movie' | 'series', opts: ItemsOptions = {}, signal?: AbortSignal) =>
  request<ItemsPage>('GET', `/api/items${qs({ kind, ...opts, scope: scopeParam(opts.scope) })}`, undefined, signal)
export const getItem = (id: number, signal?: AbortSignal) => request<ItemDetail>('GET', `/api/items/${id}`, undefined, signal)
/** «Семья»: the other profiles' places in an item (none for a kid, none hidden), most recent first. */
export const getFamily = (id: number, signal?: AbortSignal) =>
  request<{ members?: FamilyMember[] }>('GET', `/api/items/${id}/family`, undefined, signal).then((r) => r?.members ?? [])
/** «Продолжить с места …»: another profile's position in an item becomes this profile's own (theirs is never written). */
export const adoptProgress = (itemId: number, userId: number) => request<unknown>('POST', `/api/progress/${itemId}/adopt`, { userId })
/** IMDb and Rotten Tomatoes ratings of a movie or series ({} when nothing is known). */
export const getRatings = (imdbId: string, signal?: AbortSignal) =>
  request<Ratings>('GET', `/api/ratings/${encodeURIComponent(imdbId)}`, undefined, signal)
/** «Похожие» and «Коллекция» of a movie or series (after the page; never holding it up). */
export const getRelated = (id: number, signal?: AbortSignal) =>
  request<RelatedRows>('GET', `/api/items/${id}/related`, undefined, signal)
/** A movie's collection page (404: none this profile may see). */
export const getCollection = (id: number, signal?: AbortSignal) =>
  request<CollectionRow>('GET', `/api/items/${id}/collection`, undefined, signal)
export const search = (q: string, signal?: AbortSignal) =>
  request<SearchResult>('GET', `/api/search${qs({ q })}`, undefined, signal)
export const scanLibrary = () => request<void>('POST', '/api/library/scan')
export const getLibraryStatus = () => request<LibraryStatus>('GET', '/api/library/status')

// --- Playback & progress ---
export const play = (fileId: number, body: PlayRequest) => request<PlayResponse>('POST', `/api/play/${fileId}`, body)
export const touchHls = (session: string) => request<HlsStatus>('PUT', `/api/hls/${encodeURIComponent(session)}`)
export const stopHls = (session: string, keepalive = false) =>
  request<void>('DELETE', `/api/hls/${encodeURIComponent(session)}`, undefined, undefined, keepalive)
/** A 12-hour key for media URLs (`mk=`): an AirPlay receiver fetches the stream without the cookie. */
export const getMediaKey = () => request<{ key: string; expiresAt: string }>('POST', '/api/auth/media-key')
/** `updatedAt` (RFC 3339): when the position was reached (a save queued offline); the server keeps the newer one. */
export const saveProgress = (itemId: number, positionMs: number, durationMs: number, keepalive = false, updatedAt?: string) =>
  request<void>('PUT', `/api/progress/${itemId}`, updatedAt ? { positionMs, durationMs, updatedAt } : { positionMs, durationMs }, undefined, keepalive)
export const setWatched = (itemId: number, watched: boolean) =>
  request<void>('POST', `/api/items/${itemId}/watched`, { watched })

/** Admin: what deleting a library item would take away (a series lists its seasons). */
export const getDeletePlan = (itemId: number, signal?: AbortSignal) =>
  request<DeletePlan>('GET', `/api/items/${itemId}/delete`, undefined, signal)
/** Admin: deletes a library item (a season: its id); the files go to the trash of «Файлы». */
export const deleteItem = (itemId: number) => request<DeleteResult>('POST', `/api/items/${itemId}/delete`)
export const addToWatchlist = (itemId: number) => request<void>('POST', `/api/watchlist/${itemId}`)
export const removeFromWatchlist = (itemId: number) => request<void>('DELETE', `/api/watchlist/${itemId}`)
export const downloadUrl = (fileId: number) => `/api/download/${fileId}`
// --- «Смотреть онлайн» (not for kid profiles; 404 while it is off; the rest: ./clientFull) ---
/** Admin: is the sidecar there, how many sources it lists (404: not configured, or switched off). */
export const onlineStatus = (signal?: AbortSignal) => request<OnlineStatus>('GET', '/api/online/status', undefined, signal)
// --- Acquisition (not available to kid profiles; the rest: ./clientFull) ---
export const getTitle = (kind: string, tmdbId: number, signal?: AbortSignal) =>
  request<TitleInfo>('GET', `/api/discover/${encodeURIComponent(kind)}/${tmdbId}`, undefined, signal)

// --- «Попросить»: a kid asks, an admin approves (downloads) or declines ---
/** What a kid asks for: a TMDB title by id, an album or book as the catalog showed it. */
export interface KidRequestAsk {
  kind: string
  tmdbId?: number
  extId?: string
  title: string
  subtitle?: string
  year?: number
  poster?: string
  aliases?: string[]
}
/** Kids: the kid's request of the title (made once; asking again returns it). 403 code 'rating': above the age limit. */
export const askRequest = (ask: KidRequestAsk) => request<KidRequest>('POST', '/api/requests', ask)
/** The profile's own requests, newest first. */
export const getMyRequests = (signal?: AbortSignal) => request<KidRequest[]>('GET', '/api/requests/mine', undefined, signal)
/** Admin: pending requests, then those decided in the last 30 days, with who asked. */
export const getRequests = (signal?: AbortSignal) => request<KidRequest[]>('GET', '/api/requests', undefined, signal)
/** Admin: how many requests wait for an answer. */
export const getRequestCount = (signal?: AbortSignal) => request<{ pending: number }>('GET', '/api/requests/count', undefined, signal)
/** Admin: «Одобрить» without a download (a download of the title approves its request as well). */
export const approveRequest = (id: number) => request<KidRequest>('POST', `/api/requests/${id}/approve`)
/** Admin: «Отказать» with an optional reason the kid sees. */
export const declineRequest = (id: number, reason: string) => request<KidRequest>('POST', `/api/requests/${id}/decline`, { reason })
// --- Admin ---
/** Titles on their way into the library, newest first (sources build; none for a kid). */
export const getLibraryPending = (signal?: AbortSignal, scope?: LibraryScope) =>
  request<Card[]>('GET', `/api/library/pending${qs({ scope: scopeParam(scope) })}`, undefined, signal)
export const getDisk = () => request<DiskUsage>('GET', '/api/disk')
/** null: the server cannot tell (204, or an older server without the endpoint). */
export const getTorrentEngine = async (signal?: AbortSignal): Promise<TorrentEngineStatus | null> =>
  (await request<TorrentEngineStatus | undefined>('GET', '/api/torrent-engine', undefined, signal)) ?? null

export const getUsers = (signal?: AbortSignal) => request<User[]>('GET', '/api/users', undefined, signal)
export interface UserInput {
  name: string
  avatar: string
  password?: string
  isAdmin?: boolean
  isKid?: boolean
  maxRating?: string
  /** PATCH only: «Скрывать прогресс от семьи». */
  hideProgress?: boolean
  kindleEmail?: string
}
export const createUser = (u: UserInput) => request<User>('POST', '/api/users', u)
export const updateUser = (id: number, patch: Partial<UserInput>) => request<User>('PATCH', `/api/users/${id}`, patch)
export const deleteUser = (id: number) => request<void>('DELETE', `/api/users/${id}`)
/** Ends the profile's app password (Subsonic/OPDS apps must get a new one). */
export const resetAppPassword = (id: number) => request<void>('DELETE', `/api/users/${id}/app-password`)

// «Звук и субтитры»: a profile's defaults; a kid's are changed by an administrator (userId).
const prefsPath = (userId?: number) => (userId === undefined ? '/api/me/playback-prefs' : `/api/users/${userId}/playback-prefs`)
export type PlaybackPrefsInput = Omit<PlaybackPrefs, 'custom' | 'preferCleaned'>
/** The profile's interface language: its own choice ('' none) and what the apps show (`effective`, '' unknown). */
export interface UILanguage {
  language: '' | 'ru' | 'en'
  effective: '' | 'ru' | 'en'
}
export const getUILanguage = (signal?: AbortSignal) => request<UILanguage>('GET', '/api/me/ui-language', undefined, signal)
/** Sets the profile's interface language for every app ('' : back to the profile's own). */
export const putUILanguage = (language: '' | 'ru' | 'en') => request<void>('PUT', '/api/me/ui-language', { language })

export const getPlaybackPrefs = (userId?: number, signal?: AbortSignal) => request<PlaybackPrefs>('GET', prefsPath(userId), undefined, signal)
export const savePlaybackPrefs = (p: PlaybackPrefsInput, userId?: number) => request<PlaybackPrefs>('PUT', prefsPath(userId), p)
/** «Выбирать очищенную озвучку»: kept apart from the defaults (the household's stay followed). */
export const setPreferCleaned = (on: boolean, userId?: number) => request<PlaybackPrefs>('PUT', `${prefsPath(userId)}/prefer-cleaned`, { on })
/** Back to the household's «Языки». */
export const resetPlaybackPrefs = (userId?: number) => request<PlaybackPrefs>('DELETE', prefsPath(userId))
// «Уведомления на почту»: a profile's address and topics; a kid's are an administrator's (userId).
const notifyPath = (userId?: number) => (userId === undefined ? '/api/me/notify' : `/api/users/${userId}/notify`)
export type NotifyPrefsInput = Pick<NotifyPrefs, 'email' | 'episodes' | 'available' | 'requests' | 'downloads' | 'quietHours' | 'lang'>
export const getNotifyPrefs = (userId?: number, signal?: AbortSignal) => request<NotifyPrefs>('GET', notifyPath(userId), undefined, signal)
export const saveNotifyPrefs = (p: NotifyPrefsInput, userId?: number) => request<NotifyPrefs>('PUT', notifyPath(userId), p)
/** «Отправить проверочное письмо»: a code (and a link) to the saved address. */
export const sendNotifyVerification = (userId?: number) => request<void>('POST', `${notifyPath(userId)}/verify`)
export const confirmNotifyCode = (code: string, userId?: number) => request<NotifyPrefs>('POST', `${notifyPath(userId)}/confirm`, { code })
export const getTrackPrefs = (itemId: number, signal?: AbortSignal) => request<TrackPrefs>('GET', `/api/items/${itemId}/track-prefs`, undefined, signal)
/** A track chosen in an episode: kept for its series (a movie keeps nothing). */
export const rememberTracks = (itemId: number, choice: { audio: TrackChoice } | { subtitle: TrackChoice } | { subtitleOff: true }) =>
  request<void>('PUT', `/api/items/${itemId}/track-prefs`, choice)
/** «Субтитры раньше / позже» of a file for this profile; the answer is the value kept. */
export const setSubtitleOffset = (fileId: number, offsetMs: number) =>
  request<{ offsetMs: number }>('PUT', `/api/files/${fileId}/subtitle-offset`, { offsetMs }).then((r) => r.offsetMs)

/** The scrub previews of a file; null until the server has made them (404). */
export const getTrickplay = (fileId: number, signal?: AbortSignal) =>
  request<TrickplayIndex>('GET', `/api/files/${fileId}/trickplay`, undefined, signal).catch((e: unknown) => {
    if (e instanceof ApiError && e.status === 404) return null
    throw e
  })
/** Sheet n of a file's scrub previews (the index's key keeps a cached sheet current). */
export const trickplaySheetUrl = (fileId: number, ix: TrickplayIndex, n: number) =>
  `/api/files/${fileId}/trickplay/${n}.jpg?v=${encodeURIComponent(ix.key)}`

export const getSettings = (signal?: AbortSignal) => request<Settings>('GET', '/api/settings', undefined, signal)
export const saveSettings = (s: Settings) => request<Settings>('PUT', '/api/settings', s)

// Subtitles (admin): OpenSubtitles search for one video file.
export const searchSubtitles = (fileId: number, lang: 'ru' | 'en', signal?: AbortSignal) =>
  request<{ results: SubtitleResult[] }>('GET', `/api/subtitles/${fileId}/search${qs({ lang })}`, undefined, signal).then((r) => r.results)
export const downloadSubtitle = (fileId: number, subtitleId: number, lang: 'ru' | 'en') =>
  request<void>('POST', `/api/subtitles/${fileId}`, { subtitleId, lang })

export const getStats = (signal?: AbortSignal) => request<Stats>('GET', '/api/stats', undefined, signal)
export const getStorage = (signal?: AbortSignal) => request<StorageReport>('GET', '/api/stats/storage', undefined, signal)
/** What one disk category is made of (sources build, admin); `pending` while «other» is walked. */
export const getDiskBreakdown = (category: string, signal?: AbortSignal) =>
  request<DiskBreakdown>('GET', `/api/stats/disk/${encodeURIComponent(category)}`, undefined, signal)

// --- Music ---
export const getMusicHome = (signal?: AbortSignal, scope?: LibraryScope) =>
  request<MusicHome>('GET', `/api/music/home${qs({ scope: scopeParam(scope) })}`, undefined, signal)
export interface AlbumsOptions {
  sort?: AlbumSort
  artist?: number
  limit?: number
  offset?: number
  scope?: LibraryScope
}
export const getAlbums = (opts: AlbumsOptions = {}, signal?: AbortSignal) =>
  request<ItemsPage>('GET', `/api/music/albums${qs({ ...opts, scope: scopeParam(opts.scope) })}`, undefined, signal)
export const getAlbum = (id: number, signal?: AbortSignal) => request<AlbumDetail>('GET', `/api/music/albums/${id}`, undefined, signal)
/** The whole album as one ZIP (original files, cover, CUE). */
export const albumZipUrl = (id: number) => `/api/music/albums/${id}/zip`
/**
 * A track for <audio>. `accept` lists the codecs this browser decodes; the
 * server decides the rest (original at home, «Музыка вне дома» outside).
 * `t` starts a transcode that many seconds into the track.
 */
export function trackStreamUrl(id: number, o: { accept: string[]; format?: string; t?: number }): string {
  return `/api/tracks/${id}/stream${qs({ accept: o.accept.join(','), format: o.format, t: o.t })}`
}
/** Counts a listen (once per track, after 30 s or half of it). */
export const recordPlay = (trackId: number) => request<void>('POST', '/api/music/plays', { trackId })

export interface PageOptions {
  limit?: number
  offset?: number
}
export const getArtists = (opts: PageOptions & { scope?: LibraryScope } = {}, signal?: AbortSignal) =>
  request<ItemsPage>('GET', `/api/music/artists${qs({ ...opts, scope: scopeParam(opts.scope) })}`, undefined, signal)
export const getArtist = (id: number, signal?: AbortSignal) => request<ArtistDetail>('GET', `/api/music/artists/${id}`, undefined, signal)
export const getSongs = (opts: PageOptions = {}, signal?: AbortSignal) =>
  request<SongsPage>('GET', `/api/music/songs${qs({ ...opts })}`, undefined, signal)
/** The query string of GET /api/music/shuffle for a source. */
export function shuffleQuery(source: ShuffleSource): string {
  if (source.kind === 'genre') return qs({ source: 'genre', genre: source.genre })
  if (source.kind === 'artist') return qs({ source: 'artist', artist: source.artistId })
  return qs({ source: 'all' })
}
/** Up to 500 tracks of the source in random order; 404 «Не найдено» when there are none. */
export const getShuffle = (source: ShuffleSource, signal?: AbortSignal) =>
  request<{ tracks: Song[] }>('GET', `/api/music/shuffle${shuffleQuery(source)}`, undefined, signal).then((r) => r.tracks)
/** Genres of the visible albums, most songs first. */
export const getGenres = (signal?: AbortSignal) =>
  request<{ genres: Genre[] }>('GET', '/api/music/genres', undefined, signal).then((r) => r.genres)
/** 404 «Текста нет» when the track has none. */
export const getLyrics = (trackId: number, signal?: AbortSignal) =>
  request<Lyrics>('GET', `/api/music/tracks/${trackId}/lyrics`, undefined, signal)
/** Sets the track's lyrics offset for everyone (not kid profiles); answers the stored value (clamped to ±10 s). */
export const setLyricsOffset = (trackId: number, offsetMs: number) =>
  request<{ offsetMs: number }>('PUT', `/api/music/tracks/${trackId}/lyrics/offset`, { offsetMs }).then((r) => r.offsetMs)
export const getPlaylists = (signal?: AbortSignal) =>
  request<{ items: Playlist[] }>('GET', '/api/music/playlists', undefined, signal).then((r) => r.items)
export const getPlaylist = (id: number, signal?: AbortSignal) =>
  request<PlaylistDetail>('GET', `/api/music/playlists/${id}`, undefined, signal)
export const createPlaylist = (name: string, trackIds: number[] = []) =>
  request<Playlist>('POST', '/api/music/playlists', { name, trackIds })
export const updatePlaylist = (id: number, patch: PlaylistPatch) => request<Playlist>('PATCH', `/api/music/playlists/${id}`, patch)
export const deletePlaylist = (id: number) => request<void>('DELETE', `/api/music/playlists/${id}`)
/** «Нравится» for a track, album or artist. */
export const star = (itemId: number) => request<void>('POST', `/api/music/star/${itemId}`)
export const unstar = (itemId: number) => request<void>('DELETE', `/api/music/star/${itemId}`)

// --- App password (Subsonic apps, OPDS readers) ---
/** Whether the profile has an app password and since when; never the password (shown once, by create). */
export const getAppPassword = () => request<{ exists: boolean; createdAt: string | null }>('GET', '/api/me/app-password')
export const createAppPassword = () => request<{ password: string }>('POST', '/api/me/app-password')
export const revokeAppPassword = () => request<void>('DELETE', '/api/me/app-password')

// --- Devices (the Vault app signed in to this profile) ---
export const getDevices = () => request<{ items: Device[] }>('GET', '/api/me/devices')
export const revokeDevice = (id: number) => request<void>('DELETE', `/api/me/devices/${id}`)

// --- Books ---
export const getBooksHome = (signal?: AbortSignal, scope?: LibraryScope) =>
  request<BooksHome>('GET', `/api/books/home${qs({ scope: scopeParam(scope) })}`, undefined, signal)
export interface BooksOptions {
  sort?: BookSort
  limit?: number
  offset?: number
  scope?: LibraryScope
}
export const getBooks = (kind: BookKind, opts: BooksOptions = {}, signal?: AbortSignal) =>
  request<BooksPage>('GET', `/api/books${qs({ kind, ...opts, scope: scopeParam(opts.scope) })}`, undefined, signal)
/** «Добавить книги»: one file from this device; the server indexes it and makes it this profile's. */
export function uploadBook(file: File): Promise<{ name: string; duplicate: boolean; indexed: boolean }> {
  const form = new FormData()
  form.append('file', file, file.name)
  return request('POST', '/api/books/upload', form)
}
/** «Отправить на Kindle». Resolves when the book is sent or still on its way. */
export const sendToKindle = (id: number) => request<void>('POST', `/api/books/${id}/kindle`)
export const getBook = (id: number, signal?: AbortSignal) => request<BookDetail>('GET', `/api/books/${id}`, undefined, signal)
/** Saves a reading position: an EPUB CFI or `page:N`, and the read fraction 0..1. */
export const saveReading = (itemId: number, locator: string, fraction: number, keepalive = false, updatedAt?: string) =>
  request<void>('PUT', `/api/progress/${itemId}`, updatedAt ? { locator, fraction, updatedAt } : { locator, fraction }, undefined, keepalive)
/** The profile's bookmarks, highlights and notes in an item, tombstones included. */
export const getAnnotations = (itemId: number, signal?: AbortSignal) =>
  request<{ annotations: Annotation[] }>('GET', `/api/annotations/${itemId}`, undefined, signal).then((r) => r.annotations)
/** Creates, changes or (deleted: true) deletes one; the answer is the row as stored (the newer one wins). */
export const putAnnotation = (itemId: number, a: Annotation) =>
  request<Annotation>('PUT', `/api/annotations/${itemId}/${encodeURIComponent(a.id)}`, a)
/** The book's highlights, notes and bookmarks as a Markdown file. */
export const annotationsExportUrl = (itemId: number) => `/api/annotations/${itemId}/export.md`
/** The raw file (EPUB, FB2, PDF…) with its MIME type, for the reader. */
export const fileUrl = (fileId: number) => `/api/stream/${fileId}`
/** Page count and reading direction of a comic volume (its file). */
export const getComicPages = (fileId: number, signal?: AbortSignal) =>
  request<ComicPages>('GET', `/api/comics/${fileId}/pages`, undefined, signal)
/** Page n (0-based) of a comic volume as an image. */
export const comicPageUrl = (fileId: number, n: number) => `/api/comics/${fileId}/page/${n}`
/**
 * An audiobook file for <audio>, like `trackStreamUrl`: `accept` lists the
 * codecs this browser decodes, `t` starts a transcode that far into the file.
 */
export function audioFileUrl(fileId: number, o: { accept: string[]; format?: string; t?: number }): string {
  return `/api/audio/${fileId}${qs({ accept: o.accept.join(','), format: o.format, t: o.t })}`
}
/**
 * Asks for an audio stream again only to learn why the <audio> element
 * failed on it (the element does not tell): the status and Retry-After;
 * status 0 when the server cannot be reached. The body is never read.
 */
export async function probeStream(url: string): Promise<{ status: number; retryAfterSec?: number }> {
  const ctl = new AbortController()
  try {
    const res = await fetch(url, { credentials: 'same-origin', headers: { Range: 'bytes=0-0' }, signal: ctl.signal })
    const ra = Number(res.headers.get('Retry-After'))
    return { status: res.status, retryAfterSec: Number.isFinite(ra) && ra > 0 ? ra : undefined }
  } catch {
    return { status: 0 }
  } finally {
    ctl.abort()
  }
}
/** Downloads a book file for the reader (throws ApiError like `request`). */
export async function getFile(fileId: number, signal?: AbortSignal): Promise<Blob> {
  let res: Response
  try {
    res = await fetch(fileUrl(fileId), { credentials: 'same-origin', headers: { 'Accept-Language': getLanguage() }, ...(signal ? { signal } : {}) })
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    throw new ApiError(0, offline())
  }
  if (!res.ok) {
    if (res.status === 401) unauthorized?.()
    throw new ApiError(res.status, res.status === 404 ? t('app.api.bookFileMissing') : generic())
  }
  return res.blob()
}

// «Переезд на Vault» (admin)
export const migrateStatus = (signal?: AbortSignal) => request<MigrateStatus>('GET', '/api/migrate', undefined, signal)
export const migrateState = (signal?: AbortSignal) => request<MigrateState>('GET', '/api/migrate/state', undefined, signal)
export const migrateRun = (req: MigrateRunRequest) => request<{ reportId: string }>('POST', '/api/migrate/run', req)
export const migrateReports = (signal?: AbortSignal) => request<MigrateReport[]>('GET', '/api/migrate/reports', undefined, signal)
export const migrateReport = (id: string, signal?: AbortSignal) =>
  request<MigrateReport>('GET', `/api/migrate/reports/${encodeURIComponent(id)}`, undefined, signal)

// «Импорт медиатеки» (admin; the playlist's pending songs: any profile)
export const getImportServices = (signal?: AbortSignal) =>
  request<{ services: ImportService[] }>('GET', '/api/import/services', undefined, signal).then((r) => r.services)
export const getImportJobs = (signal?: AbortSignal) =>
  request<{ jobs: ImportJob[] }>('GET', '/api/import/jobs', undefined, signal).then((r) => r.jobs)
export const getImportJob = (id: number, signal?: AbortSignal) => request<ImportJob>('GET', `/api/import/jobs/${id}`, undefined, signal)
/** Files of the «Файл» tile (CSV, M3U, Library.xml, a Takeout zip) → a job in preview. */
export function uploadImportFiles(files: File[]): Promise<ImportJob> {
  const form = new FormData()
  for (const f of files) form.append('file', f, f.name)
  return request<ImportJob>('POST', '/api/import/files', form)
}
export const patchImportJob = (id: number, patch: ImportJobPatch) => request<ImportJob>('PATCH', `/api/import/jobs/${id}`, patch)
export const startImportJob = (id: number) => request<void>('POST', `/api/import/jobs/${id}/start`)
export const cancelImportJob = (id: number) => request<void>('POST', `/api/import/jobs/${id}/cancel`)
export const getImportReview = (id: number, signal?: AbortSignal) =>
  request<{ tracks: ReviewTrack[] }>('GET', `/api/import/jobs/${id}/review`, undefined, signal).then((r) => r.tracks)
export const getImportCandidates = (trackId: number, signal?: AbortSignal) =>
  request<{ candidates: Candidate[] }>('GET', `/api/import/tracks/${trackId}/candidates`, undefined, signal).then((r) => r.candidates)
export const searchImport = (q: string, signal?: AbortSignal) =>
  request<{ candidates: Candidate[] }>('GET', `/api/import/search${qs({ q })}`, undefined, signal).then((r) => r.candidates)
export const resolveImportTrack = (trackId: number, body: ImportResolve) => request<void>('POST', `/api/import/tracks/${trackId}/resolve`, body)
export const getImportGroups = (id: number, signal?: AbortSignal) =>
  request<{ groups: ImportGroup[] }>('GET', `/api/import/jobs/${id}/groups`, undefined, signal).then((r) => r.groups)
/** «Повторить»; anyFormat: «Скачать в другом формате» of an album with no release in «Формат загрузки». */
export const retryImportGroup = (id: number, anyFormat = false) =>
  request<void>('POST', `/api/import/groups/${id}/retry${anyFormat ? '?format=any' : ''}`)
export const getPlaylistPending = (playlistId: number, signal?: AbortSignal) =>
  request<{ pending: PendingEntry[] }>('GET', `/api/import/playlists/${playlistId}/pending`, undefined, signal).then((r) => r.pending ?? [])

// --- «Экранное время» ---
export const getMyScreenTime = (signal?: AbortSignal) => request<ScreenStatus>('GET', '/api/me/screen-time', undefined, signal)
export const getScreenLimited = (signal?: AbortSignal) => request<ScreenLimited>('GET', '/api/screen-time/limited', undefined, signal)
export const getScreenTime = (userId: number, signal?: AbortSignal) => request<ScreenReport>('GET', `/api/users/${userId}/screen-time`, undefined, signal)
export const saveScreenLimits = (userId: number, l: ScreenLimits) => request<ScreenReport>('PUT', `/api/users/${userId}/screen-time`, l)
export const addScreenBonus = (userId: number, minutes: number) => request<ScreenReport>('POST', `/api/users/${userId}/screen-time/bonus`, { minutes })
