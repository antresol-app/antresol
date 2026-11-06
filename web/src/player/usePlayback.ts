// The playback engine behind VideoPlayer: one /api/play session at a time.
//
// POST /api/play decides direct play (the file as is) or HLS (remux or
// transcode; hls.js, or Safari's native player). HLS starts at the position
// the server reports (a copied video starts on the keyframe before the one
// asked for), so the stream's clock is offset by `startSec`. Seeking outside
// what is transcoded restarts the session there; the new session replaces
// the old one on the server (`replaceSession`), and a closed one is DELETEd.
// While an HLS session lives the player sends a heartbeat every 20 s, so a
// long pause keeps it, and a session that went away is restarted at once.
//
// Failures go through `recover`: an expired session is restarted where it
// was, a decoding failure is retried once with transcode-only caps, and only
// then the viewer sees an error. Waiting is not a failure by itself: a slow
// transcode or a stream that still brings data keeps the spinner (see
// waitVerdict); a stream that brings nothing for minutes is «stalled» and
// never falls back to a transcode of the same slow source.
//
// Online playback (mode 'online', «Смотреть онлайн») plays a source through
// the server's proxy: hls.js, Safari's native HLS, or an MP4 as is. Its
// stream is the whole video from 0, so every seek is local, and there is no
// heartbeat (no /api/hls/ session). A stale link (a 410, a long stall) is
// played again with `refresh` at the same place, at most twice in 5 minutes
// (recoverOnline); after that the viewer is asked for another quality or
// source.
//
// AirPlay (Safari): the receiver fetches the stream without the cookie, so
// while the video plays on one (`setRemote(true)`) every session URL gets a
// media key (airplay.ts), and the switch itself reloads the session at the
// same place. Coming back keeps the keyed stream (no second reload).
//
// The position on the whole timeline (all parts of a movie) is kept in
// `absRef`, updated from the media element only while it plays what we think
// it plays — never from a detached or still-seeking element — so progress is
// always saved at the real position.
import { useCallback, useEffect, useMemo, useRef, useState, type RefObject } from 'react'
import type Hls from 'hls.js'
import type { ErrorData } from 'hls.js'
import { ApiError, play, stopHls, touchHls } from '../api/client'
import { nativeHls } from './hlsSupport'
import type { ClientCaps, MediaFile, OnlineQualityInfo, PlayRequest, PlayResponse, PlaySegment, Quality, QualityInfo, SubTrack, Track } from '../api/types'
import { EXTERNAL_AUDIO_BASE } from '../api/types'
import {
  endedEarly,
  mediaErrorReason,
  onlineExpired,
  recoverOnline,
  repeatedEarlyEnd,
  planSeek,
  recover,
  waitVerdict,
  type FailReason,
  type HlsHealth,
} from './recovery'
import { locate, partStarts } from './timeline'
import { withoutCodecs } from './useCaps'
import { forgetMediaKey, hasMediaKey, mediaKey, withMediaKey } from './airplay'
import { t } from '../i18n'

const START_TIMEOUT_MS = 30_000
/** An HLS stream that waits this long gets checked: a session that went away is restarted. */
const STALL_TIMEOUT_MS = 20_000
/** Heartbeat of an HLS session (the server drops sessions idle for 2 minutes). */
export const HEARTBEAT_MS = 20_000
/** Safari may stop the heartbeat in the background: a native HLS paused this long is reloaded on play. */
const NATIVE_PAUSE_LIMIT_MS = 100_000

export const finalError = () => t('player.video.finalError')
export const stalledError = () => t('player.video.stalledError')
export const onlineError = () => t('player.video.onlineError')
const offlineError = () => t('player.video.offline')

const sessionOf = (url: string) => /\/api\/hls\/([^/]+)\//.exec(url)?.[1]

// hls.js (about 175 KB gz) is fetched only by a browser without native HLS,
// as soon as such a browser starts playing (in parallel with the server's answer).
let hlsModule: Promise<typeof import('hls.js')> | null = null
const loadHls = () => (hlsModule ??= import('hls.js'))

/** The codecs a failed attempt may have tripped on: everything but H.264/AAC. */
const retryCaps = (c: ClientCaps) =>
  withoutCodecs(c, [...c.videoCodecs.filter((v) => v !== 'h264'), ...c.audioCodecs.filter((a) => a !== 'aac')])

/** hls.js retries: the server answers 503 while ffmpeg works on the first segment. */
function hlsConfig(slow: boolean, online = false) {
  const manifest = {
    maxTimeToFirstByteMs: Infinity,
    maxLoadTimeMs: 30_000, // the server waits up to 15 s before its 503
    timeoutRetry: { maxNumRetry: 4, retryDelayMs: 0, maxRetryDelayMs: 0 },
    // online: the proxy answers at once (a source error is a 502, a stale link a 410)
    errorRetry: { maxNumRetry: online ? 2 : slow ? 600 : 60, retryDelayMs: 2000, maxRetryDelayMs: 2000 },
  }
  return {
    startPosition: 0,
    // online CDNs stall now and then: a longer buffer rides it out (the byte
    // cap, hls.js's 60 MB maxBufferSize, still bounds memory); the first
    // fragment is fetched while the manifest is still being parsed
    maxBufferLength: online ? 60 : 30,
    maxMaxBufferLength: online ? 120 : 600,
    startFragPrefetch: online,
    backBufferLength: 60,
    enableWorker: true,
    manifestLoadPolicy: { default: manifest },
  }
}

interface Session {
  token: number
  part: number
  /** '' while a session is being requested. */
  mode: string
  /** Stream time 0 = this many seconds into the part. */
  offset: number
  sid?: string
  native: boolean
  /** When the session was requested (ms). */
  at: number
  /** The first frame was shown: waiting after that is a stall. */
  started: boolean
}

export interface PlaybackOptions {
  videoRef: RefObject<HTMLVideoElement | null>
  parts: MediaFile[]
  startSec: number
  caps: ClientCaps
  /**
   * The audio stream index to ask for when a part starts (the first load,
   * the next part, a seek into another part); undefined: the file's
   * default. Asked of the part that starts: parts order their tracks
   * differently.
   */
  audioFor?: (file: MediaFile, partIdx: number) => number | undefined
  /** The quality menu's choice for the first load. */
  initialQuality?: Quality
  /** The last part ended. */
  onFinished: () => void
  /** Starts a session for a part; default POST /api/play/{file.id} (a remote stream uses /api/torrent-play). */
  start?: (file: MediaFile, body: PlayRequest) => Promise<PlayResponse>
  /** A remote stream: waiting is the source, not the device. */
  slowSource?: boolean
  /** Bytes the stream has downloaded so far (growing = data is still coming). */
  sourceProgress?: () => number
  /** Online playback: `start` plays a source through the proxy; `stop` closes its session. */
  online?: { stop: (session: string, keepalive?: boolean) => Promise<void>; initialQuality?: string }
}

export function usePlayback({
  videoRef,
  parts,
  startSec,
  caps,
  audioFor,
  initialQuality = 'auto',
  onFinished,
  start,
  slowSource = false,
  sourceProgress,
  online,
}: PlaybackOptions) {
  const onlineRef = useRef(online)
  onlineRef.current = online
  const isOnline = Boolean(online)
  const startRef = useRef(start)
  startRef.current = start
  const progressRef = useRef(sourceProgress)
  progressRef.current = sourceProgress
  const hlsRef = useRef<Hls | null>(null)
  const session = useRef<Session>({ token: 0, part: 0, mode: '', offset: 0, native: false, at: 0, started: false })
  const capsRef = useRef(caps)
  const rec = useRef({ restarts: 0, fallbackUsed: false })
  const failedToken = useRef(-1)
  const startTimer = useRef<number | undefined>(undefined)
  const stallTimer = useRef<number | undefined>(undefined)
  const beat = useRef<number | undefined>(undefined)
  const pendingSeek = useRef<number | null>(null)
  const audioForRef = useRef(audioFor)
  audioForRef.current = audioFor
  /** The audio stream of the part `audioPart` (a stream index means nothing in another file). */
  const audioRef = useRef<number | undefined>(undefined)
  const audioPart = useRef(-1)
  /** The audio list of the part audioPart (the last play answer). */
  const audioListRef = useRef<Track[]>([])
  const qualityRef = useRef<Quality>(initialQuality)
  /** The last heartbeat's answer; gone = restart on the next play or wait. */
  const health = useRef<HlsHealth | undefined>(undefined)
  /** Progress seen at the last wait check, and checks in a row without any. */
  const lastSeen = useRef({ bytes: -1, segments: -1, idle: 0 })
  const stopped = useRef(false)
  /** Where an HLS stream last ended before the part's probed end. */
  const earlyEnd = useRef<{ part: number; pos: number } | null>(null)
  const pausedAt = useRef(0)
  // This session has shown frames: only then is a pause the viewer's (WebKit
  // also fires «pause» when it refuses a play() that had no tap behind it).
  const hasPlayed = useRef(false)
  /** Online: when the stale-link re-plays happened (ms), and whether the next load is one. */
  const replays = useRef<number[]>([])
  const refreshNext = useRef(false)
  /** A frame was shown at least once in this player. */
  const played = useRef(false)
  const onlineQualityRef = useRef(online?.initialQuality ?? '')
  /** The video plays on an AirPlay receiver: session URLs carry a media key. */
  const remote = useRef(false)
  /** The URL the element was given last (keyed or not). */
  const urlRef = useRef('')
  /** No media key could be had for AirPlay (an older server, no network). */
  const [keyFailed, setKeyFailed] = useState(false)

  const [durations, setDurations] = useState(() => parts.map((p) => (p.durationMs ?? 0) / 1000))
  const durRef = useRef(durations)
  durRef.current = durations
  const starts = useMemo(() => partStarts(durations.map((d) => ({ durationMs: d * 1000 }))), [durations])
  const startsRef = useRef(starts)
  startsRef.current = starts
  const total = durations.reduce((a, b) => a + b, 0)

  /** Position on the whole timeline, seconds. The source of truth for progress. */
  const absRef = useRef(startSec)
  const [time, setTime] = useState(startSec)
  const [localTime, setLocalTime] = useState(0)
  const [part, setPart] = useState(0)
  const [mode, setMode] = useState('')
  const [buffered, setBuffered] = useState(0)
  const [playing, setPlaying] = useState(false)
  const [waiting, setWaiting] = useState(true)
  const waitingRef = useRef(true)
  const [needsTap, setNeedsTap] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [audio, setAudio] = useState<Track[]>([])
  const [audioIndex, setAudioIndex] = useState(-1)
  const [subs, setSubs] = useState<SubTrack[]>([])
  /** The current part's recap, intro and credits (its file timeline). */
  const [segments, setSegments] = useState<PlaySegment[]>([])
  const segmentsPart = useRef(-1)
  const [quality, setQuality] = useState<Quality>(initialQuality)
  const [qualityInfo, setQualityInfo] = useState<QualityInfo | null>(null)
  const [onlineQuality, setOnlineQuality] = useState<OnlineQualityInfo | null>(null)
  /** Online: a stale link is being fetched again («Обновляем ссылку…»). */
  const [refreshing, setRefreshing] = useState(false)

  const setAbs = useCallback((t: number) => {
    absRef.current = t
    setTime(t)
  }, [])
  const setWait = useCallback((w: boolean) => {
    waitingRef.current = w
    setWaiting(w)
  }, [])

  /** Seconds into the current part (from absRef, valid even when the element is not). */
  const partTime = useCallback(() => absRef.current - (startsRef.current[session.current.part] ?? 0), [])

  /** Closes a server session: an online proxy session, else an HLS one. */
  const stopSession = (sid: string, keepalive = false) => {
    const o = onlineRef.current
    return (o ? o.stop(sid, keepalive) : stopHls(sid, keepalive)).catch(() => {})
  }

  /** Stops timers, hls.js and (unless it is being replaced) the server session. */
  const teardown = useCallback((keepalive = false, replaced = false) => {
    window.clearTimeout(startTimer.current)
    window.clearTimeout(stallTimer.current)
    window.clearInterval(beat.current)
    hlsRef.current?.destroy()
    hlsRef.current = null
    health.current = undefined
    const sid = session.current.sid
    session.current.sid = undefined
    if (sid && !replaced) void stopSession(sid, keepalive)
  }, [])

  const fail = useRef<(reason: FailReason, token: number) => void>(() => {})
  const load = useRef<(partIdx: number, localStart: number, opts?: { audioIndex?: number }) => Promise<void>>(async () => {})

  /** Asks the server about the HLS session (heartbeat); a gone session is restarted when it matters. */
  const heartbeat = useCallback(async (token: number): Promise<HlsHealth | undefined> => {
    const sid = session.current.sid
    if (!sid || session.current.token !== token) return undefined
    try {
      const st = await touchHls(sid)
      const h = { gone: false, running: st.running, segments: st.segments }
      if (session.current.token === token) health.current = h
      return h
    } catch (e) {
      if (!(e instanceof ApiError) || e.status !== 404) return health.current // offline: no news
      const h = { gone: true, running: false, segments: 0 }
      if (session.current.token !== token) return h
      health.current = h
      // Waiting on a session that no longer exists: restart it now.
      if (waitingRef.current && !videoRef.current?.paused) fail.current('expired', token)
      return h
    }
  }, [videoRef])

  /** A start or stall timer fired: keep waiting (re-arm) or fail. */
  const checkWait = useCallback(
    async (phase: 'start' | 'stall', token: number) => {
      const s = session.current
      if (s.token !== token || stopped.current) return
      const h = s.mode !== '' && s.mode !== 'direct' && s.mode !== 'online' ? await heartbeat(token) : undefined
      if (session.current.token !== token) return
      const bytes = progressRef.current?.() ?? -1
      const seen = lastSeen.current
      const progressed = (bytes >= 0 && bytes > seen.bytes) || (h !== undefined && h.segments > seen.segments && seen.segments >= 0)
      const verdict = waitVerdict({
        phase,
        mode: s.mode,
        slow: slowSource,
        progressed,
        idleChecks: seen.idle,
        elapsedSec: (Date.now() - s.at) / 1000,
        hls: h,
      })
      lastSeen.current = { bytes: Math.max(bytes, seen.bytes), segments: h?.segments ?? seen.segments, idle: progressed ? 0 : seen.idle + 1 }
      if (verdict !== 'wait') return fail.current(verdict, token)
      const timer = phase === 'start' ? startTimer : stallTimer
      window.clearTimeout(timer.current)
      timer.current = window.setTimeout(() => void checkWait(phase, token), phase === 'start' ? START_TIMEOUT_MS : STALL_TIMEOUT_MS)
    },
    [heartbeat, slowSource],
  )

  /** Starts playback; a browser that wants a tap first says so (the big play button). */
  const playNow = useCallback((video: HTMLVideoElement) => {
    video.play().then(
      () => setNeedsTap(false),
      (e: unknown) => {
        if (e instanceof DOMException && e.name === 'NotAllowedError') setNeedsTap(true)
      },
    )
  }, [])

  const attach = useCallback(
    (r: PlayResponse, token: number, localStart: number) => {
      const video = videoRef.current
      if (!video) return
      // Direct play seeks to the position; a copied (remux) video starts on
      // the keyframe before it and skips the rest inside the stream.
      // An online stream is the whole video: it seeks like direct play.
      const local = r.mode === 'direct' || r.mode === 'online'
      const skipIn = local ? localStart : localStart - r.startSec
      pendingSeek.current = skipIn > 0.3 ? skipIn : null
      lastSeen.current = { bytes: progressRef.current?.() ?? -1, segments: -1, idle: 0 }
      window.clearTimeout(startTimer.current)
      startTimer.current = window.setTimeout(() => void checkWait('start', token), START_TIMEOUT_MS)
      if (session.current.sid && r.mode !== 'online') {
        window.clearInterval(beat.current)
        beat.current = window.setInterval(() => void heartbeat(token), HEARTBEAT_MS)
      }
      if (r.mode === 'direct' || session.current.native || (r.mode === 'online' && r.kind === 'mp4')) {
        video.src = r.url
      } else {
        const startPosition = pendingSeek.current ?? 0
        pendingSeek.current = null // hls.js starts there itself
        void loadHls().then(({ default: Hls }) => {
          if (session.current.token !== token || videoRef.current !== video) return
          const hls = new Hls({ ...hlsConfig(slowSource, r.mode === 'online'), startPosition })
          hlsRef.current = hls
          hls.on(Hls.Events.ERROR, (_e, data: ErrorData) => {
            if (session.current.token !== token) return
            // online: the proxy's 410 (a stale link) never gets better by retrying
            const stale = r.mode === 'online' && onlineExpired(data.response?.code)
            if (!data.fatal && !stale) return
            const expired = stale || data.type === Hls.ErrorTypes.NETWORK_ERROR
            fail.current(expired ? 'expired' : 'media', token)
          })
          hls.loadSource(r.url)
          hls.attachMedia(video)
          playNow(video)
        }, () => fail.current('media', token))
        return
      }
      playNow(video)
    },
    [checkWait, heartbeat, slowSource, playNow, videoRef],
  )

  load.current = async (partIdx: number, localStart: number, opts: { audioIndex?: number } = {}) => {
    const file = parts[partIdx]
    if (!file || stopped.current) return
    const token = session.current.token + 1
    const replace = session.current.sid
    teardown(false, true)
    hasPlayed.current = false
    pausedAt.current = 0
    const video = videoRef.current
    session.current = {
      token,
      part: partIdx,
      mode: '',
      offset: Math.max(0, localStart),
      native: Boolean(video && nativeHls(video)),
      at: Date.now(),
      started: false,
    }
    // hls.js downloads while the server answers (unless the browser plays HLS itself)
    if (video && !session.current.native) void loadHls().catch(() => undefined)
    if (video) {
      video.pause()
      video.removeAttribute('src')
      video.load()
    }
    setAbs((startsRef.current[partIdx] ?? 0) + Math.max(0, localStart))
    setPart(partIdx)
    setWait(true)
    setError(null)
    setBuffered(0)
    const ai = opts.audioIndex ?? (audioPart.current === partIdx ? audioRef.current : audioForRef.current?.(file, partIdx))
    // an audio file beside the video goes by its id too (a new file shifts the positions)
    const tracks = audioPart.current === partIdx && audioListRef.current.length ? audioListRef.current : (file.audio ?? [])
    const audioSrc = ai !== undefined && ai >= EXTERNAL_AUDIO_BASE ? tracks.find((t) => t.index === ai)?.src : undefined
    let r: PlayResponse
    try {
      const refresh = refreshNext.current
      refreshNext.current = false
      const body: PlayRequest = {
        caps: capsRef.current,
        startSec: Math.max(0, localStart),
        quality: onlineRef.current ? onlineQualityRef.current : qualityRef.current,
        ...(ai !== undefined && ai >= 0 ? { audioIndex: ai } : {}),
        ...(audioSrc ? { audioSrc } : {}),
        ...(replace ? { replaceSession: replace } : {}),
        ...(refresh ? { refresh: true } : {}),
      }
      r = await (startRef.current ? startRef.current(file, body) : play(file.id, body))
    } catch (e) {
      if (session.current.token !== token) return
      if (e instanceof DOMException && e.name === 'AbortError') return
      setWait(false)
      setRefreshing(false)
      setError(e instanceof ApiError && e.status !== 0 ? e.message : offlineError())
      return
    }
    const sid = r.mode === 'online' ? r.session : r.mode === 'direct' ? undefined : sessionOf(r.url)
    if (session.current.token !== token || stopped.current) {
      if (sid) void stopSession(sid)
      return
    }
    if (remote.current && !hasMediaKey(r.url)) {
      try {
        r = { ...r, url: withMediaKey(r.url, await mediaKey()) }
        setKeyFailed(false)
      } catch {
        setKeyFailed(true) // the receiver will be refused; the player's recovery takes it from there
      }
      if (session.current.token !== token || stopped.current) {
        if (sid) void stopSession(sid)
        return
      }
    }
    urlRef.current = r.url
    const offset = r.mode === 'direct' || r.mode === 'online' ? 0 : r.startSec
    session.current = { ...session.current, mode: r.mode, offset, sid }
    setMode(r.mode)
    if (r.durationMs && r.durationMs > 0) {
      const d = r.durationMs / 1000
      setDurations((ds) => (Math.abs((ds[partIdx] ?? 0) - d) > 0.5 ? ds.map((x, i) => (i === partIdx ? d : x)) : ds))
    }
    setAudio(r.audio ?? [])
    audioListRef.current = r.audio ?? []
    setAudioIndex(r.audioIndex)
    audioRef.current = r.audioIndex
    audioPart.current = partIdx
    setSubs((old) => (sameSubs(old, r.subtitles ?? []) ? old : (r.subtitles ?? [])))
    // a reload of the same part (a seek, a track) keeps the list it had
    if (r.segments || segmentsPart.current !== partIdx) setSegments(r.segments ?? [])
    segmentsPart.current = partIdx
    if (r.quality && 'options' in r.quality) {
      setOnlineQuality(r.quality)
      onlineQualityRef.current = r.quality.current
      setQualityInfo(null)
    } else setQualityInfo(r.quality ?? null)
    attach(r, token, localStart)
  }

  fail.current = (reason, token) => {
    if (session.current.token !== token || failedToken.current === token || stopped.current) return
    failedToken.current = token
    const at = partTime()
    // On a receiver a failure may be its refused key (a session ended
    // elsewhere): the restart asks for a new one.
    if (remote.current) forgetMediaKey()
    if (onlineRef.current) {
      // A decoding failure is this device; anything else is a stale link.
      const o = reason === 'media' ? ({ action: 'error' } as const) : recoverOnline(replays.current, Date.now(), played.current)
      if (o.action === 'replay') {
        replays.current = o.replays
        refreshNext.current = true
        setRefreshing(true)
        void load.current(session.current.part, at)
      } else {
        teardown()
        setWait(false)
        setRefreshing(false)
        setError(reason === 'media' ? finalError() : onlineError())
      }
      return
    }
    const next = recover(rec.current, reason)
    if (next.action === 'restart') {
      rec.current.restarts++
      void load.current(session.current.part, at)
    } else if (next.action === 'fallback') {
      rec.current.fallbackUsed = true
      capsRef.current = retryCaps(capsRef.current)
      void load.current(session.current.part, at)
    } else {
      teardown()
      setWait(false)
      // stalls are another build's
      setError(!__LIBRARY_EDITION__ && reason === 'stalled' ? stalledError() : finalError())
    }
  }

  // First load; everything stops on unmount.
  useEffect(() => {
    stopped.current = false
    capsRef.current = caps
    const loc = locate(
      durRef.current.map((d) => ({ durationMs: d * 1000 })),
      startSec,
    )
    void load.current(loc.part, loc.offset)
    // A reload or closed tab never unmounts React: free the server session
    // now, or it counts against the profile's limit until it idles out. The
    // player stays intact in case the page comes back from the bfcache (the
    // heartbeat then finds the session gone and restarts it).
    const onPageHide = () => {
      const sid = session.current.sid
      if (sid) void stopSession(sid, true)
    }
    window.addEventListener('pagehide', onPageHide)
    return () => {
      window.removeEventListener('pagehide', onPageHide)
      stopped.current = true
      teardown(true)
    }
    // Starts once per mounted item; the parent remounts the player (key) for another one.
  }, [])

  const reload = useCallback((partIdx: number, at: number, opts?: { audioIndex?: number }) => load.current(partIdx, at, opts), [])

  /**
   * AirPlay: the video went to a receiver (true) or came back. Going there
   * reloads the session at the same place with a media key, unless the
   * stream has one already or a session is still being asked for (it gets
   * one when it comes).
   */
  const setRemote = useCallback(
    (on: boolean) => {
      if (remote.current === on) return
      remote.current = on
      if (!on || stopped.current) return
      const s = session.current
      if (s.mode === '' || hasMediaKey(urlRef.current)) return
      void reload(s.part, partTime())
    },
    [reload, partTime],
  )

  const seek = useCallback(
    (target: number) => {
      const dur = durRef.current.reduce((a, b) => a + b, 0)
      const t = Math.min(Math.max(0, target), Math.max(0, dur - 0.5))
      const v = videoRef.current
      const s = session.current
      const loc = locate(
        durRef.current.map((d) => ({ durationMs: d * 1000 })),
        t,
      )
      setAbs(t)
      // Still waiting for the server or for the first frame: move the start instead.
      if (loc.part === s.part && s.mode === '') return void reload(s.part, loc.offset)
      if (loc.part === s.part && (s.mode === 'direct' || s.mode === 'online') && pendingSeek.current !== null) {
        pendingSeek.current = loc.offset
        return
      }
      const end = v && v.seekable.length ? v.seekable.end(v.seekable.length - 1) : 0
      const plan = planSeek({ part: s.part, mode: s.mode, offset: s.offset, seekableEnd: end }, loc.part, loc.offset)
      if (plan.kind === 'local' && v) v.currentTime = plan.time
      else if (plan.kind === 'reload') void reload(plan.part, plan.at)
    },
    [reload, setAbs, videoRef],
  )

  const skip = useCallback((d: number) => seek(absRef.current + d), [seek])
  /** Seeks to t seconds into the current part (a segment's end). */
  const seekPart = useCallback((t: number) => seek((startsRef.current[session.current.part] ?? 0) + t), [seek])

  const toggle = useCallback(() => {
    const v = videoRef.current
    if (!v || error) return
    if (v.paused) void v.play().then(() => setNeedsTap(false), () => {})
    else v.pause()
  }, [error, videoRef])

  const chooseAudio = useCallback(
    (index: number) => {
      audioRef.current = index
      audioPart.current = session.current.part
      void reload(session.current.part, partTime(), { audioIndex: index })
    },
    [reload, partTime],
  )

  /** The quality menu: restarts the session at the current position. */
  const chooseQuality = useCallback(
    (q: Quality) => {
      if (q === qualityRef.current) return
      qualityRef.current = q
      setQuality(q)
      rec.current = { restarts: 0, fallbackUsed: false }
      void reload(session.current.part, partTime())
    },
    [reload, partTime],
  )

  /** Online: another source quality, played again at the current position. */
  const chooseOnlineQuality = useCallback(
    (q: string) => {
      if (q === onlineQualityRef.current) return
      onlineQualityRef.current = q
      setOnlineQuality((info) => (info ? { ...info, current: q } : info))
      replays.current = []
      void reload(session.current.part, partTime())
    },
    [reload, partTime],
  )

  const retry = useCallback(() => {
    rec.current = { restarts: 0, fallbackUsed: false }
    replays.current = []
    capsRef.current = caps
    void reload(session.current.part, partTime())
  }, [caps, reload, partTime])

  const stop = useCallback(
    (keepalive = false) => {
      stopped.current = true
      videoRef.current?.pause()
      teardown(keepalive)
    },
    [teardown, videoRef],
  )

  // ---------- media element events ----------
  const isHls = () => session.current.mode !== '' && session.current.mode !== 'direct'
  const nativeLibraryHls = () => session.current.native && isHls() && session.current.mode !== 'online'

  const armStall = () => {
    window.clearTimeout(stallTimer.current)
    const s = session.current
    // Before the first frame the start timer is in charge (a slow 4K start is not a stall).
    if (!s.started || (!isHls() && !slowSource)) return
    lastSeen.current = { bytes: progressRef.current?.() ?? -1, segments: health.current?.segments ?? -1, idle: 0 }
    const token = s.token
    stallTimer.current = window.setTimeout(() => void checkWait('stall', token), STALL_TIMEOUT_MS)
  }
  const clearStall = () => window.clearTimeout(stallTimer.current)

  /** A heartbeat found the session gone: restart where we are instead of freezing. */
  const restartIfGone = () => {
    if (isHls() && health.current?.gone) {
      fail.current('expired', session.current.token)
      return true
    }
    return false
  }

  const update = () => {
    const v = videoRef.current
    const s = session.current
    if (!v || s.mode === '' || pendingSeek.current !== null) return
    const st = startsRef.current[s.part] ?? 0
    const local = s.offset + v.currentTime
    setAbs(st + local)
    setLocalTime(local)
    if (v.buffered.length) setBuffered(st + s.offset + v.buffered.end(v.buffered.length - 1))
  }

  const mediaProps = {
    onLoadedMetadata: () => {
      const v = videoRef.current
      if (!v) return
      // An online stream knows its own length (the proxy has no probe of it).
      if (session.current.mode === 'online' && Number.isFinite(v.duration) && v.duration > 0) {
        const d = v.duration
        setDurations((ds) => (Math.abs((ds[0] ?? 0) - d) > 0.5 ? [d] : ds))
        durRef.current = [d]
      }
      if (pendingSeek.current !== null) {
        v.currentTime = pendingSeek.current
        pendingSeek.current = null
      } else if (nativeLibraryHls() && v.currentTime > 1) {
        // Safari opens a growing (EVENT) playlist at its live edge; start at the beginning.
        v.currentTime = 0
      }
    },
    onLoadedData: () => {
      const v = videoRef.current
      const token = session.current.token
      // Sound but no picture: the browser claimed a codec it cannot decode
      // (not on AirPlay: the receiver decodes, the page may see no frame).
      window.setTimeout(() => {
        if (v && !remote.current && session.current.token === token && v.readyState >= 2 && v.videoWidth === 0 && v.videoHeight === 0) {
          fail.current('media', token)
        }
      }, 1200)
    },
    onCanPlay: () => {
      window.clearTimeout(startTimer.current)
      session.current.started = true
      played.current = true
      clearStall()
      setWait(false)
    },
    onPlaying: () => {
      hasPlayed.current = true
      clearStall()
      setWait(false)
      setPlaying(true)
      setRefreshing(false)
      rec.current.restarts = 0
    },
    onPlay: () => {
      setPlaying(true)
      if (restartIfGone()) return
      const s = session.current
      if (nativeLibraryHls() && pausedAt.current && Date.now() - pausedAt.current > NATIVE_PAUSE_LIMIT_MS) {
        pausedAt.current = 0
        void reload(s.part, partTime())
      }
    },
    onPause: () => {
      // A refused autoplay is not a pause: a late tap must play this
      // session, not reload it (the reload's play() comes after an await,
      // so iOS refuses it again and «Смотреть» seems to do nothing).
      pausedAt.current = hasPlayed.current ? Date.now() : 0
      clearStall()
      setPlaying(false)
    },
    onWaiting: () => {
      setWait(true)
      if (!restartIfGone()) armStall()
    },
    onTimeUpdate: update,
    onProgress: update,
    onSeeked: update,
    onEnded: () => {
      const s = session.current
      const v = videoRef.current
      // A stream that broke off (a long swarm stall) is not the end of the movie.
      const endPos = s.offset + (v?.currentTime ?? 0)
      if (isHls() && v && endedEarly(durRef.current[s.part] ?? 0, endPos) && !repeatedEarlyEnd(earlyEnd.current, s.part, endPos)) {
        // Once per place: a second end at the same spot after the restart is
        // the real end (a container whose header duration is too long).
        earlyEnd.current = { part: s.part, pos: endPos }
        fail.current(slowSource ? 'stalled' : 'expired', s.token)
        return
      }
      earlyEnd.current = null
      if (s.part < parts.length - 1) {
        // audioFor picks the next file's own track (by the title and language of the one that played)
        void reload(s.part + 1, 0)
      } else onFinished()
    },
    onError: () => {
      const v = videoRef.current
      if (!v?.error || (!v.getAttribute('src') && !hlsRef.current)) return
      fail.current(mediaErrorReason(session.current.mode, v.error.code, slowSource, session.current.started), session.current.token)
    },
  }

  return {
    absRef,
    durRef,
    time,
    localTime,
    total,
    part,
    mode,
    buffered,
    playing,
    waiting,
    needsTap,
    error,
    audio,
    audioIndex,
    subs,
    segments,
    quality,
    qualityInfo,
    onlineQuality,
    refreshing,
    online: isOnline,
    seek,
    seekPart,
    skip,
    toggle,
    chooseAudio,
    chooseQuality,
    chooseOnlineQuality,
    retry,
    stop,
    setRemote,
    keyFailed,
    mediaProps,
  }
}

/** The same subtitle tracks (a reload must not refetch the cues). */
function sameSubs(a: SubTrack[], b: SubTrack[]): boolean {
  return a.length === b.length && a.every((s, i) => s.index === b[i]!.index && s.url === b[i]!.url)
}
