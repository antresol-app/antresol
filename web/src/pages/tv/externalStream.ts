// An external channel in the browser: which of its streams the web can try
// (the primary first, then the fallbacks), and why none can when so. The
// player tries them in order once each — no retry loop — and otherwise says
// the channel opens in the Antresol apps or an IPTV player.
// The viewer's audio and subtitle choice on a channel — an external one or
// ours — is kept per channel in this browser (localStorage), never on the
// server.
import type { Channel } from '../../api/channels'
import { cleanSession, type TrackSession } from './model'

/** Why the web cannot play an external channel. */
export type ExtBlock = 'platform' | 'mixed' | 'network'

const insecure = (url: string, pageHttps: boolean) => pageHttps && /^http:/i.test(url)

/** The URLs to try, in order; `blocked` when there are none. */
export function externalStreams(c: Channel, pageHttps = typeof location !== 'undefined' && location.protocol === 'https:'): { urls: string[]; blocked: ExtBlock | null } {
  const s = c.stream
  if (!s) return { urls: [], blocked: 'platform' }
  const all = [{ url: s.url, unsupported: s.unsupported ?? [] }, ...(s.fallbacks ?? []).map((f) => ({ url: f.url, unsupported: f.unsupported ?? [] }))]
  const web = all.filter((x) => x.url && !x.unsupported.includes('web'))
  const urls = web.filter((x) => !insecure(x.url, pageHttps)).map((x) => x.url)
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
