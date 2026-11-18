// An external channel in the browser: which of its streams the web can try
// (the primary first, then the fallbacks), and why none can when so. A
// stream the browser cannot play as given (a User-Agent or Referer it cannot
// send, http on an https page) plays through the server's same-origin relay
// in its place; the relays of the streams tried directly come last (a
// provider without CORS). Each keeps the server's word on its format: an
// HLS playlist plays through hls.js (or Safari's own HLS), a raw MPEG-TS
// stream (an IPTV provider's «.ts», the relay of one) through mpegts.js —
// hls.js cannot read it — and a stream of unknown format tries HLS, then
// MPEG-TS. The player tries them in order once each — no retry loop — and
// otherwise says the channel opens in the Antresol apps or an IPTV player.
// The viewer's audio and subtitle choice on a channel — an external one or
// ours — is kept per channel in this browser (localStorage), never on the
// server.
import type { Channel, StreamFormat } from '../../api/channels'
import { cleanSession, type TrackSession } from './model'

/** Why the web cannot play an external channel. */
export type ExtBlock = 'platform' | 'mixed' | 'network'

const insecure = (url: string, pageHttps: boolean) => pageHttps && /^http:/i.test(url)

/** The format a provider's URL says it has (a server that did not say): .m3u8, .ts; '' otherwise. */
export function guessFormat(url: string): StreamFormat {
  const path = url.split(/[?#]/)[0]!.toLowerCase()
  if (/\.m3u8?$/.test(path)) return 'hls'
  if (/\.(ts|mts|m2ts|mpe?g)$/.test(path)) return 'ts'
  return ''
}

/** A stream to try: its URL and its format ('' unknown: HLS, then MPEG-TS). */
export interface ExtCandidate {
  url: string
  format: StreamFormat
}

/** The streams to try, in order; `blocked` when there are none. */
export function externalStreams(c: Channel, pageHttps = typeof location !== 'undefined' && location.protocol === 'https:'): { urls: ExtCandidate[]; blocked: ExtBlock | null } {
  const s = c.stream
  if (!s) return { urls: [], blocked: 'platform' }
  const all = [
    { url: s.url, relay: s.relay, unsupported: s.unsupported ?? [], format: s.format ?? guessFormat(s.url) },
    ...(s.fallbacks ?? []).map((f) => ({ url: f.url, relay: f.relay, unsupported: f.unsupported ?? [], format: f.format ?? guessFormat(f.url) })),
  ]
  const web = all.filter((x) => x.url && !x.unsupported.includes('web'))
  const direct = (x: (typeof all)[number]) => !!x.url && !x.unsupported.includes('web') && !insecure(x.url, pageHttps)
  const urls: ExtCandidate[] = []
  for (const x of all) {
    if (direct(x)) urls.push({ url: x.url, format: x.format })
    else if (x.relay) urls.push({ url: x.relay, format: x.format })
  }
  for (const x of all) if (direct(x) && x.relay) urls.push({ url: x.relay, format: x.format })
  if (urls.length) return { urls, blocked: null }
  return { urls: [], blocked: web.length ? 'mixed' : 'platform' }
}

const KEY = 'antresol.tv.extTracks'

function readAll(): Record<string, TrackSession> {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? '{}') as unknown
    return v && typeof v === 'object' ? (v as Record<string, TrackSession>) : {}
  } catch {
    return {}
  }
}

/** The audio and subtitles chosen on this channel before (empty: the defaults — the device's languages, the profile's, the original). */
export function channelTracks(channelId: number): TrackSession {
  return cleanSession(readAll()[String(channelId)] ?? {})
}

export function setChannelTracks(channelId: number, s: TrackSession) {
  try {
    const all = readAll()
    all[String(channelId)] = s
    localStorage.setItem(KEY, JSON.stringify(all))
  } catch {
    /* a private window: the choice lasts while the page does */
  }
}
