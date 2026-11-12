// «ТВ-каналы» (/api/channels…): live channels made of the library, their
// guide and the editor's options.
import { qs, request } from './client'

export type ChannelOrder = 'shuffle' | 'ordered'

/**
 * One entry of a channel's own list (rules.picks): a movie or a series
 * (`title`, a series plays all its episodes in order), one movie or episode
 * (`item`), or every video under a library folder (`folder`, the path as the
 * folder browser returns it).
 */
export type ChannelPick = { kind: 'title'; id: number } | { kind: 'item'; id: number } | { kind: 'folder'; path: string }

/** What a channel plays (the server's channels.Rules). */
export interface ChannelRules {
  kinds?: ('movie' | 'series')[]
  genres?: string[]
  yearFrom?: number
  yearTo?: number
  /** Round 1's chosen titles (read only: the editor saves them as picks). */
  titles?: number[]
  /** The channel's own ordered list; when set it is the whole pool (the filters are ignored). */
  picks?: ChannelPick[]
  libraryUser?: number
  maxRating?: string
  order?: ChannelOrder
  /** Also films of these genres the library lacks, from online sources (where the server offers it). */
  extras?: boolean
}

/** An audio or subtitle rendition of the live stream present in a programme. */
export interface ChannelTrack {
  /** Round 3's fixed slot (older servers; never relied on). */
  slot?: string
  /** The HLS rendition's NAME, unique in its group (select the rendition by it). */
  name: string
  /** What the picker shows («Русский — LostFilm»); may be longer than the name. */
  label: string
  /** BCP 47 / ISO 639 of the track ("" unknown). */
  lang: string
}

export interface ChannelProgram {
  /** "<channel>-<seq>" (older servers: absent). */
  id?: string
  itemId: number
  /** 'external': an external playlist's programme (no item page, itemId 0). */
  kind?: 'movie' | 'episode' | 'external'
  title: string
  subtitle?: string
  overview?: string
  year?: number
  rating?: string
  genres?: string[]
  start: string
  end: string
  poster?: string
  backdrop?: string
  /** From an online source, not in the library (itemId is negative). */
  external?: boolean
  /** Absent: unknown; empty: none. */
  audio?: ChannelTrack[]
  subtitles?: ChannelTrack[]
}

/** Where an external channel's stream is, as the server tells this client. */
export interface ExternalStream {
  /** The provider's URL, or /api/channels/live/ext/{id}/{n} (a redirect) when the source hides addresses. */
  url: string
  userAgent?: string
  referrer?: string
  /** The platforms that cannot play it (a header a browser cannot send, http on an https page…). */
  unsupported: string[]
  fallbacks: { url: string; userAgent?: string; referrer?: string; unsupported?: string[] }[]
}

export interface Channel {
  id: number
  number: number
  name: string
  category: string
  personal: boolean
  auto: boolean
  enabled: boolean
  canEdit: boolean
  /** Starred by this profile. */
  favorite?: boolean
  /** The uploaded logo ("/api/channels/{id}/logo.png?v=…"); absent: none (show number and name). */
  logoUrl?: string
  /** Only for a channel the profile may edit. */
  rules?: ChannelRules
  /** The editor's list: rules.picks described in order (only where the profile may edit). */
  pickInfo?: ChannelPickInfo[]
  now?: ChannelProgram
  next?: ChannelProgram
  /** From an external playlist: the client plays `stream` itself. */
  external?: boolean
  stream?: ExternalStream
}

export interface ChannelList {
  /** In number order: the one list. */
  channels: Channel[]
  /** The channel this profile tuned in last (a TV's «last channel»). */
  lastChannel?: number | null
  canManage: boolean
  canCreate: boolean
}

export interface ChannelGuide {
  channel: Channel
  programs: ChannelProgram[]
}

/** A channel in the guide grid. */
export interface ChannelEPG {
  id: number
  number: number
  name: string
  category: string
  favorite?: boolean
  logoUrl?: string
  programs: ChannelProgram[]
}

export interface ChannelEPGWindow {
  from: string
  to: string
  channels: ChannelEPG[]
}

export interface ChannelOptions {
  genres: { key: string; name: string }[]
  decades: number[]
  titles: { id: number; kind: 'movie' | 'series'; title: string; year?: number; episodes?: number }[]
  profiles: { id: number; name: string }[]
  /** The server can mix in films from outside the library (rules.extras). */
  extrasAvailable?: boolean
}

/** A pick as the server describes it (Channel.pickInfo). */
export type ChannelPickInfo = ChannelPick & {
  title: string
  subtitle?: string
  year?: number
  /** Nothing of it plays now (gone, above the age limit…). */
  missing?: boolean
}

/** A hit of the editor's search (movies, series, episodes). */
export interface ChannelSearchHit {
  kind: 'movie' | 'series' | 'episode'
  id: number
  title: string
  /** An episode's «Series · S1 E2»… */
  subtitle?: string
  year?: number
  /** What to put in rules.picks. */
  pick?: ChannelPick
}

/** One level of the library's folders for the editor's browser. */
export interface ChannelFolderLevel {
  folders: { path: string; name: string }[]
  /** Playable files here: their item ids. */
  files: { id: number; name: string; kind?: 'movie' | 'episode' }[]
}

export interface ChannelInput {
  name: string
  number?: number
  category: string
  personal: boolean
  enabled: boolean
  rules: ChannelRules
}

export const getChannels = (signal?: AbortSignal, manage = false) =>
  request<ChannelList>('GET', `/api/channels${qs({ manage: manage ? 1 : undefined })}`, undefined, signal)

export const getChannelGuide = (id: number, hours = 12, signal?: AbortSignal) =>
  request<ChannelGuide>('GET', `/api/channels/${id}/guide${qs({ hours })}`, undefined, signal)

/** The guide grid: every channel with the programmes overlapping [from, from+hours). */
export const getChannelEPG = (from: Date, hours = 6, signal?: AbortSignal) =>
  request<ChannelEPGWindow>('GET', `/api/channels/epg${qs({ from: from.toISOString().replace(/\.\d{3}Z$/, 'Z'), hours })}`, undefined, signal)

/** Star or unstar a channel for this profile. */
export const setChannelFavorite = (id: number, on: boolean) => request<void>(on ? 'PUT' : 'DELETE', `/api/channels/${id}/favorite`)

/** The channel was tuned in: the profile's «last channel» (never progress or history). */
export const setLastChannel = (id: number) => request<void>('PUT', '/api/channels/last', { channelId: id })

export const getChannelOptions = (signal?: AbortSignal) => request<ChannelOptions>('GET', '/api/channels/options', undefined, signal)

export const searchChannelOptions = (q: string, signal?: AbortSignal) =>
  request<{ results?: ChannelSearchHit[] } | ChannelSearchHit[]>('GET', `/api/channels/options/search${qs({ q })}`, undefined, signal).then((r) =>
    Array.isArray(r) ? r : (r?.results ?? []),
  )

/** The folders and files under `path` ("" : the library roots). */
export const getChannelFolders = (path: string, signal?: AbortSignal) =>
  request<ChannelFolderLevel>('GET', `/api/channels/options/folders${qs({ path: path || undefined })}`, undefined, signal).then((r) => ({
    folders: r?.folders ?? [],
    files: r?.files ?? [],
  }))

/** The largest logo the server takes. */
export const LOGO_MAX_BYTES = 2 * 1024 * 1024
export const LOGO_TYPES = ['image/png', 'image/jpeg', 'image/webp']

/** Upload a channel's logo (PNG, JPEG or WebP ≤ 2 MB): the channel as it is now, or nothing. */
export function uploadChannelLogo(id: number, file: File) {
  const body = new FormData()
  body.append('file', file)
  return request<Partial<Channel> | undefined>('POST', `/api/channels/${id}/logo`, body)
}
export const deleteChannelLogo = (id: number) => request<void>('DELETE', `/api/channels/${id}/logo`)

export const createChannel = (c: ChannelInput) => request<Channel>('POST', '/api/channels', c)
export const updateChannel = (id: number, c: ChannelInput) => request<Channel>('PUT', `/api/channels/${id}`, c)
export const deleteChannel = (id: number) => request<void>('DELETE', `/api/channels/${id}`)
export const generateChannels = () => request<{ created: number; updated: number; disabled: number }>('POST', '/api/channels/generate')


/** The web player's live stream of a channel. */
export const liveUrl = (id: number) => `/api/channels/live/${id}/index.m3u8`

/** How far a programme is (0…1) at `now`. */
export function programProgress(p: ChannelProgram, now = Date.now()): number {
  const s = Date.parse(p.start)
  const e = Date.parse(p.end)
  if (!(e > s)) return 0
  return Math.min(1, Math.max(0, (now - s) / (e - s)))
}
