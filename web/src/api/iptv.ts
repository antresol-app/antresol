// «Внешние источники» (/api/tv/*, admin only): external IPTV playlists, their
// channels in the editor, guides, categories, sets, devices and IPTV links.
// Secrets never come back: a source's URL and its guides' URLs are masked
// («http://host/…***»), an Xtream password is never sent; a PUT with an
// empty `url` / `password` keeps the stored one.
import { qs, request } from './client'

export type RefreshHours = 0 | 6 | 12 | 24 | 72
export const REFRESH_HOURS: RefreshHours[] = [0, 6, 12, 24, 72]

export interface SourceCounts {
  total: number
  shown: number
  hidden: number
  new: number
}

export interface SourceEPG {
  id: number
  /** Masked: scheme://host/…*** */
  url: string
  /** From the playlist's url-tvg (not the admin's; kept on a save). */
  auto: boolean
  position: number
  refreshHours: RefreshHours
  refreshOnOpen: boolean
  lastOk: string | null
  lastTry: string | null
  lastError: string
  /** Channels of the source bound to this guide. */
  matched: number
}

export interface Source {
  id: number
  name: string
  kind: 'm3u' | 'xtream'
  /** Masked: scheme://host/…*** ("" for Xtream). */
  url: string
  xtream: { server: string; user: string } | null
  /** null: by the playlist's size (shown under 300 channels). */
  defaultShown: boolean | null
  hideUrls: boolean
  presets: { timeshift: boolean; adult: boolean }
  refreshHours: RefreshHours
  refreshOnOpen: boolean
  position: number
  /** Empty: every profile without limits. */
  profiles: number[]
  lastOk: string | null
  lastTry: string | null
  /** The last failure, in the viewer's language ("" none). */
  lastError: string
  /** Channels the last refresh lost, waiting for the admin (0: none). */
  pendingDrop: number
  counts: SourceCounts
  epg: SourceEPG[]
}

export interface SourceInput {
  name?: string
  kind?: 'm3u' | 'xtream'
  /** Sent only when changed ("" keeps the stored one). */
  url?: string
  xtream?: { server: string; user: string; password: string }
  defaultShown?: boolean | null
  hideUrls?: boolean
  presets?: { timeshift: boolean; adult: boolean }
  refreshHours?: RefreshHours
  refreshOnOpen?: boolean
  position?: number
  profiles?: number[]
  /** In priority order; admin guides left out are deleted, playlist guides stay. */
  epg?: { id?: number; url?: string; refreshHours: RefreshHours; refreshOnOpen: boolean }[]
}

export interface RefreshResult {
  ok: boolean
  error?: string
  result: { total: number; new: number; lost: number; shown: number; hidden: number; pendingDrop: number }
  source: Source
}

/** How a channel got its guide. */
export type EPGHow = 'tvg-id' | 'name' | 'norm-name' | 'manual' | ''

/** What decided a channel's fate (the «Почему» column). */
export type WhyCode = 'source-default' | 'preset-timeshift' | 'preset-adult' | 'manual' | ''

export interface SourceChannel {
  memberId: number
  channelId: number
  name: string
  /** The playlist's own name (the name, unless renamed). */
  providerName: string
  tvgId: string
  group: string
  logo: string
  shown: boolean
  number: number
  numberManual: boolean
  category: string
  epg: { xmlId: string; epgUrlId: number; how: EPGHow; manual: boolean }
  why: WhyCode
  whyText: string
  /** Still in the playlist. */
  present: boolean
  new: boolean
}

export interface ChannelGroup {
  name: string
  total: number
  shown: number
}

export interface SourceChannels {
  groups: ChannelGroup[]
  channels: SourceChannel[]
  total: number
}

export interface EPGHit {
  epgUrlId: number
  sourceId: number
  xmlId: string
  names: string[]
  icon?: string
}

export interface ChannelSet {
  id: number
  name: string
  sources: number[]
  hiddenGroups: string[]
  hiddenChannels: number[]
}

export type DeviceClient = 'ios' | 'ipados' | 'tvos' | 'macos' | 'android' | 'androidtv' | 'iptv' | 'other'

export interface Device {
  userId: number
  profile: string
  kind: 'app' | 'iptv'
  deviceId: string
  /** An IPTV link's id ("" for an app). */
  linkId: string
  name: string
  client: DeviceClient
  /** The server's own name for the client (when the code is «other»). */
  clientName: string
  lastSeen: string | null
  /** 0: «Все мои каналы». */
  setId: number
  defaultSetId: number
  playlist?: string
  guide?: string
}

// ---------- sources ----------

export const getSources = (signal?: AbortSignal) => request<{ sources: Source[]; xtreamEnabled: boolean }>('GET', '/api/tv/sources', undefined, signal)
export const createSource = (body: SourceInput) => request<Source>('POST', '/api/tv/sources', body)
export const updateSource = (id: number, body: SourceInput) => request<Source>('PUT', `/api/tv/sources/${id}`, body)
export const deleteSource = (id: number) => request<void>('DELETE', `/api/tv/sources/${id}`)
export const refreshSource = (id: number) => request<RefreshResult>('POST', `/api/tv/sources/${id}/refresh`)
/** Take the shorter list. */
export const confirmDrop = (id: number) => request<RefreshResult>('POST', `/api/tv/sources/${id}/confirm`)
/** Keep the old list. */
export const rejectDrop = (id: number) => request<Source>('POST', `/api/tv/sources/${id}/reject`)
export const refreshEPG = (id: number) => request<{ started: boolean }>('POST', `/api/tv/epg/${id}/refresh`)

// ---------- the editor ----------

/** Every channel of a source (no limit): the page filters them itself. */
export const getSourceChannels = (id: number, signal?: AbortSignal) =>
  request<SourceChannels>('GET', `/api/tv/sources/${id}/channels${qs({ state: 'all' })}`, undefined, signal).then((r) => ({
    groups: r?.groups ?? [],
    channels: r?.channels ?? [],
    total: r?.total ?? 0,
  }))

/** "" category/name: the playlist's; number 0: automatic; reset: back to the playlist's everything. */
export const patchChannels = (body: { channelIds: number[]; shown?: boolean; category?: string; name?: string; number?: number; reset?: boolean }) =>
  request<{ updated: number }>('PATCH', '/api/tv/channels', body)
export const patchGroup = (body: { sourceId: number; group: string; shown: boolean }) => request<{ updated: number }>('PATCH', '/api/tv/groups', body)
export const searchEPG = (q: string, signal?: AbortSignal) =>
  request<{ results: EPGHit[] }>('GET', `/api/tv/epg/search${qs({ q })}`, undefined, signal).then((r) => r?.results ?? [])
export const bindEPG = (channelId: number, epgUrlId: number, xmlId: string) => request<void>('PUT', `/api/tv/channels/${channelId}/epg`, { epgUrlId, xmlId })
export const unbindEPG = (channelId: number) => request<void>('DELETE', `/api/tv/channels/${channelId}/epg`)
export const getCategories = (signal?: AbortSignal) =>
  request<{ categories: string[] }>('GET', '/api/tv/categories', undefined, signal).then((r) => r?.categories ?? [])

// ---------- sets, devices, links, settings ----------

export const getSets = (signal?: AbortSignal) => request<{ sets: ChannelSet[] }>('GET', '/api/tv/sets', undefined, signal).then((r) => r?.sets ?? [])
export const createSet = (body: Omit<ChannelSet, 'id'>) => request<ChannelSet>('POST', '/api/tv/sets', body)
export const updateSet = (id: number, body: Omit<ChannelSet, 'id'>) => request<ChannelSet>('PUT', `/api/tv/sets/${id}`, body)
export const deleteSet = (id: number) => request<void>('DELETE', `/api/tv/sets/${id}`)

export const getDevices = (signal?: AbortSignal) => request<{ devices: Device[] }>('GET', '/api/tv/devices', undefined, signal).then((r) => r?.devices ?? [])
export const assignDevice = (body: { userId: number; deviceId: string; setId: number } | { linkId: string; setId: number }) =>
  request<void>('PUT', '/api/tv/devices/assign', body)
export const createLink = (body: { userId: number; setId: number; name: string }) =>
  request<{ linkId: string; playlist: string; guide: string }>('POST', '/api/tv/links', body)

/** Revokes an IPTV link: the TV's playlist and guide stop working. */
export const deleteLink = (linkId: string) => request<void>('DELETE', `/api/tv/links/${encodeURIComponent(linkId)}`)

export const getTvSettings = (signal?: AbortSignal) => request<{ xtreamEnabled: boolean }>('GET', '/api/tv/settings', undefined, signal)
export const putTvSettings = (body: { xtreamEnabled: boolean }) => request<{ xtreamEnabled: boolean }>('PUT', '/api/tv/settings', body)

/** «ТВ» opened: the server refreshes the sources marked «при открытии» (any profile). */
export const channelsOpened = () => request<void>('POST', '/api/channels/opened')
