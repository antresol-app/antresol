// «ТВ-каналы» (/api/channels…): live channels made of the library, their
// guide and the editor's options.
import { qs, request } from './client'

export type ChannelOrder = 'shuffle' | 'ordered'

/** What a channel plays (the server's channels.Rules). */
export interface ChannelRules {
  kinds?: ('movie' | 'series')[]
  genres?: string[]
  yearFrom?: number
  yearTo?: number
  titles?: number[]
  libraryUser?: number
  maxRating?: string
  order?: ChannelOrder
  /** Also films of these genres the library lacks, from online sources (where the server offers it). */
  extras?: boolean
}

/** An audio or subtitle rendition of the live stream present in a programme. */
export interface ChannelTrack {
  slot: string
  /** The HLS rendition's NAME (select the rendition by it). */
  name: string
  /** What the picker shows («Русский — LostFilm»). */
  label: string
  lang: string
}

export interface ChannelProgram {
  /** "<channel>-<seq>" (older servers: absent). */
  id?: string
  itemId: number
  kind?: 'movie' | 'episode'
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
  /** Only for a channel the profile may edit. */
  rules?: ChannelRules
  now?: ChannelProgram
  next?: ChannelProgram
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
