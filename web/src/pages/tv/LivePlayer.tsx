// /tv/:id — a channel live, full window. The
// stream is the channel's live HLS master (one encoder on the server shared
// by everyone watching; up to 8 audio and 6 subtitle renditions, chosen by NAME): Safari
// plays it itself, other browsers through hls.js. The picture is full
// bleed; a tap, the pointer moving or a key brings the info bar over it —
// the channel's number and name, the programme on air with its progress
// and times, what is next, and the controls: channel up/down, the star,
// «Звук и субтитры», «Каналы» (the list to switch from), sound, picture in
// picture, full screen — and it hides after a few quiet seconds.
//
// Live may be paused: the centre play/pause on the picture comes and goes
// with the bars (and stays while paused). A resume picks up where it
// stopped while the stream still holds that picture — about a minute on
// our channels, the seekable range on an external one — else at the live
// edge. More than 10 s behind the edge, «В эфир» replaces «Прямой эфир» and
// jumps back to it (liveEdge.ts).
//
// Keys: Space/K (and a keyboard's play/pause key) pause and resume;
// ↑/↓ (PageUp/PageDown) zap within the list the player was opened
// from (?f=: all, favourites or one category), in its order, wrapping;
// i or Enter shows the info (with the description) and hides it; C the
// channels; M sound; F full screen; Escape closes what is open, then the
// player.
//
// An external channel (an IPTV playlist's) plays the provider's stream
// directly: its URL, then its fallbacks, once each — an HLS one through
// hls.js, a raw MPEG-TS one through mpegts.js (externalStream.ts). When the web cannot
// play it (headers a browser cannot send, http on an https page, a
// provider that refuses other sites — CORS — or a dead stream) the player
// says so: the channel opens in the Antresol apps or an IPTV player — only
// when nothing plays. Once the picture has moved, an error is a hiccup, not
// a failure: while the picture still moves nothing is said (a live
// playlist's reload that failed, a late error of an attempt already
// abandoned, an MPEG-TS connection the relay ended); when it stops for
// liveTiming.stallMs the same stream is started again once, then the next
// one; the message shows only when none is left, and goes as soon as the
// picture moves again. Each attempt has its own engine and its errors count
// for it alone. Its audio and subtitles come from the stream.
//
// The voice, on every channel: the viewer's choice on that channel (kept
// per channel in this browser, externalStream.ts), else the profile's
// audio languages (as for a film), else the device's language, else the
// original (model.pickAudio); subtitles by the profile's «Субтитры». The
// server's master already names the renditions in the profile's language
// and marks its DEFAULTs so (channels.TrackSet.Localized/WithDefaults).
//
// Full screen: the player's element, else the video's own on an iPhone
// (liveFullscreen.ts). Loading and errors are centred on the picture,
// never over what is under it.
//
// TV is TV: this player writes nothing but the profile's «last channel»
// (PUT /api/channels/last) and its stars — no progress, no playback
// reports, no track memory on the server (__tests__/liveNoHistory.test.tsx).
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router'
import type Hls from 'hls.js'
import type Mpegts from 'mpegts.js'
import { APP_NAME } from '../../brand'
import { getPlaybackPrefs, imageUrl } from '../../api/client'
import { getChannelGuide, getChannels, getLiveQuality, liveUrl, setChannelFavorite, setLastChannel, type Channel, type ChannelProgram, type ChannelTrack } from '../../api/channels'
import type { PlaybackPrefs, Quality, QualityInfo, StreamQualityInfo } from '../../api/types'
import { Icon } from '../../components/Icon'
import { useT } from '../../i18n'
import { audio as audioEngine } from '../../player/audio/engine'
import { nativeHls } from '../../player/hlsSupport'
import { QualityMenu, saveQuality, savedQuality, StreamQualityMenu } from '../../player/QualityMenu'
import { useVolume } from '../../player/useVolume'
import { VolumeControl } from '../../player/VolumeControl'
import { Button } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { useMediaQuery } from '../../state/useMediaQuery'
import { VirtualList } from '../../ui/VirtualList'
import { ChannelArt } from './ChannelArt'
import { ChannelLogo } from './ChannelLogo'
import { activeCueText, applyRenditions, entriesFromNames, renditionNames, type NativeVideo } from './liveTracks'
import { LiveTrackMenu } from './LiveTrackMenu'
import { canFullscreen, fullscreenElement, isFullscreen, onFullscreenChange, toggleFullscreen, type FsVideo } from './liveFullscreen'
import { applyFilter, channelByNumber, chooseAudioIn, chooseSubsIn, isOnAir, numDigits, parseFilter, pickAudio, pickSubs, progressOf, renditionsToPlay, systemLanguages, withFavorite, zapTarget, type Rendition } from './model'
import { clock, useNow } from './useChannels'
import { chromeShown, readClock, resumeTarget, showGoLive, behindLive } from './liveEdge'
import { channelTracks, externalStreams, setChannelTracks, type ExtBlock, type ExtCandidate } from './externalStream'
import look from '../../player/SubtitleLook.module.css'
import styles from './LivePlayer.module.css'

const HIDE_MS = 4000
/**
 * stallMs: how long the picture may stand still after it has played before an error
 * counts (ms): a live stream's error while its buffer still plays is a
 * hiccup. numberMs: how long a typed channel number waits for its next
 * digit before it switches (ms). Variables for the tests.
 */
export const liveTiming = { stallMs: 4000, numberMs: 1500 }
const GUIDE_MS = 30_000
/** A row of the channel list beside the picture (px; a phone's, a wide window's). */
const PANEL_ROW_H = { narrow: 64, wide: 72 }

type MpegtsPlayer = Mpegts.Player
type Phase = 'starting' | 'playing' | { error: string } | { ext: ExtBlock }

type WebkitVideo = NativeVideo & {
  webkitSupportsPresentationMode?: (m: string) => boolean
  webkitSetPresentationMode?: (m: string) => void
}

export function LivePlayer() {
  const t = useT()
  useLayoutEffect(() => audioEngine.yieldSession(), [])
  const { id = '0' } = useParams()
  const channelId = Number(id)
  const [params] = useSearchParams()
  const fParam = params.get('f')
  const filter = useMemo(() => parseFilter(fParam), [fParam])
  const navigate = useNavigate()
  const rootRef = useRef<HTMLDivElement>(null)
  const videoRef = useRef<HTMLVideoElement>(null)
  const hlsRef = useRef<Hls | null>(null)
  const [channels, setChannels] = useState<Channel[]>([])
  const [favFirst, setFavFirst] = useState(false)
  const [listed, setListed] = useState(false)
  const [programs, setPrograms] = useState<ChannelProgram[]>([])
  const [phase, setPhase] = useState<Phase>('starting')
  const { volume, muted, changeVolume, toggleMute, muteForNow } = useVolume(videoRef)
  const [full, setFull] = useState(false)
  const [chrome, setChrome] = useState(true)
  const [info, setInfo] = useState(false)
  const [panel, setPanel] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const [prefs, setPrefs] = useState<PlaybackPrefs | null>(null)
  const [session, setSession] = useState(() => channelTracks(channelId))
  // «Качество»: ours — the film player's menu (shared with films on this
  // device), a lower one a run of its own on the server; an external
  // stream's — its own variants, when its master has several (hls.js)
  const [quality, setQuality] = useState<Quality>(savedQuality)
  const [qualityInfo, setQualityInfo] = useState<QualityInfo | null>(null)
  const [extQuality, setExtQuality] = useState<StreamQualityInfo | null>(null)
  // paused (read from the video: a key, the picture-in-picture window or the system's controls may do it)
  const [paused, setPaused] = useState(false)
  // more than 10 s behind the live edge: «В эфир»
  const [behind, setBehind] = useState(false)
  // the browser's own HLS: how far before the stream's end playback started (liveEdge)
  const startOffset = useRef(-1)
  // the renditions the stream has, read again when the player lists them
  const [renditions, setRenditions] = useState<{ audio: Rendition[]; subs: Rendition[] }>({ audio: [], subs: [] })
  const now = useNow(5000)
  // a phone held upright: the picture at the top, the info and the controls
  // under it (always shown), then the channels to switch to
  const upright = useMediaQuery('(max-width: 699px) and (orientation: portrait)')
  // a narrow bar: info, picture in picture and full screen go into «…»
  const narrow = useMediaQuery('(max-width: 699px)')

  const channel = channels.find((c) => c.id === channelId)
  // what to play: the server's live stream, or an external channel's own
  // streams (known once the list is in; the key changes only when they do)
  const external = channel?.external ? externalStreams(channel) : null
  const source = !listed ? '' : external ? (external.blocked ? `blocked:${external.blocked}` : `ext:${JSON.stringify(external.urls)}`) : `own:${quality}`
  // the list ↑/↓ zap through: the one it was opened from
  const zapList = useMemo(() => {
    const l = applyFilter(channels, filter, favFirst)
    return l.length ? l : applyFilter(channels, { kind: 'all' }, favFirst)
  }, [channels, filter, favFirst])
  const onAir = programs.find((p) => Date.parse(p.end) > now) ?? (channel?.now && isOnAir(channel.now, now) ? channel.now : undefined)
  const next = onAir ? (programs[programs.indexOf(onAir) + 1] ?? channel?.next) : undefined

  // the list, its now/next for «Каналы»; the profile's defaults for the tracks
  useEffect(() => {
    let ac = new AbortController()
    const load = () => {
      ac.abort()
      ac = new AbortController()
      // brief: what is on, without descriptions (this channel's are in its guide)
      getChannels(ac.signal, false, true).then(
        (l) => {
          setChannels(l.channels)
          setFavFirst(Boolean(l.favoritesFirst))
          setListed(true)
        },
        () => setListed(true),
      )
    }
    load()
    const timer = window.setInterval(load, GUIDE_MS)
    return () => {
      window.clearInterval(timer)
      ac.abort()
    }
  }, [])
  useEffect(() => {
    const ac = new AbortController()
    getPlaybackPrefs(undefined, ac.signal).then(setPrefs, () => undefined)
    return () => ac.abort()
  }, [])
  // what «Авто» means here (at home the channel's own stream, outside it «Качество вне дома»)
  const ownChannel = Boolean(channel && !channel.external)
  useEffect(() => {
    setQualityInfo(null)
    if (!ownChannel) return
    const ac = new AbortController()
    getLiveQuality(quality, ac.signal).then(setQualityInfo, () => undefined)
    return () => ac.abort()
  }, [quality, ownChannel])
  useEffect(() => {
    let ac = new AbortController()
    setPrograms([])
    const load = () => {
      ac.abort()
      ac = new AbortController()
      getChannelGuide(channelId, 6, ac.signal).then((g) => setPrograms(g.programs), () => undefined)
    }
    load()
    const timer = window.setInterval(load, GUIDE_MS)
    return () => {
      window.clearInterval(timer)
      ac.abort()
    }
  }, [channelId])
  // a TV's «last channel» (never progress or history)
  useEffect(() => {
    if (channelId > 0) setLastChannel(channelId).catch(() => undefined)
  }, [channelId])
  useEffect(() => {
    document.title = `${channel ? t('tv.channelNumber', { number: channel.number, name: channel.name }) : t('tv.title')} — ${APP_NAME}`
  }, [channel, t])

  // the stream: native HLS or hls.js, or mpegts.js for a raw MPEG-TS one; a new channel (or «Повторить») starts over
  useEffect(() => {
    const video = videoRef.current
    if (!video || !source) return
    let hls: Hls | null = null
    let ts: MpegtsPlayer | null = null
    let gone = false
    setPhase('starting')
    setRenditions({ audio: [], subs: [] })
    setExtQuality(null)
    const own = source.startsWith('own:') ? (source.slice(4) as Quality) : null
    if (source.startsWith('blocked:')) {
      setPhase({ ext: source.slice('blocked:'.length) as ExtBlock })
      return
    }
    const ext = source.startsWith('ext:') ? (JSON.parse(source.slice(4)) as ExtCandidate[]) : null
    let at = 0
    // a stream of unknown format that is no HLS playlist gets one try as MPEG-TS
    let asTs = false
    // each attempt's errors count for it alone (an engine torn down may still report)
    let gen = 0
    // the picture has moved on this channel; when it last moved; the stream
    // started again after it stopped (once per stop)
    let started = false
    let movedAt = 0
    let restarted = false
    let later = 0
    // what was said when nothing was left to try (the picture moving again hides it until it stops again)
    let gaveUp: Phase | null = null
    const giveUp = (ph: Phase) => {
      gaveUp = ph
      setPhase(ph)
    }
    const watchStall = () => {
      window.clearTimeout(later)
      later = window.setTimeout(() => {
        if (gone || !gaveUp) return
        if (moving()) watchStall()
        else setPhase(gaveUp)
      }, liveTiming.stallMs)
    }
    const moving = () => started && !video.paused && Date.now() - movedAt < liveTiming.stallMs
    const relist = () => !gone && setRenditions(renditionNames(hls, video as NativeVideo))
    // a browser that refuses to start it with sound (no tap before it) starts it muted
    const play = () =>
      void video
        .play()
        .catch(() => {
          if (gone) return
          video.muted = true
          muteForNow()
          return video.play()
        })
        .catch(() => undefined)
    const stopTs = () => {
      ts?.destroy()
      ts = null
    }
    const stopHls = () => {
      hls?.destroy()
      hls = null
      hlsRef.current = null
    }
    // an attempt failed. Before anything played: the next stream (once each;
    // none left: the web cannot play it). After the picture has moved: not
    // while it still moves (asked again shortly); once it stopped, the same
    // stream once more, then on as before.
    const fail = (g: number, msg: string) => {
      if (gone || g !== gen) return
      window.clearTimeout(later)
      if (moving()) {
        later = window.setTimeout(() => fail(g, msg), 1000)
        return
      }
      if (started && !restarted) {
        restarted = true
        if (!ext) startHls(liveUrl(channelId, own ?? 'auto'))
        else if (asTs) startTs(ext[at]!.url)
        else startAt(at)
        return
      }
      if (!ext) return giveUp({ error: msg })
      const c = ext[at]!
      if (c.format === '' && !asTs) {
        asTs = true
        startTs(c.url)
        return
      }
      at++
      asTs = false
      if (at < ext.length) {
        startAt(at)
        return
      }
      giveUp({ ext: 'network' })
    }
    const startHls = (url: string) => {
      const g = ++gen
      stopTs()
      if (nativeHls(video)) {
        video.src = url
        play()
        return
      }
      // a new engine for every attempt: a late error of the one before never counts for this one
      stopHls()
      void import('hls.js').then(({ default: H }) => {
        if (gone || g !== gen) return
        const h = new H({
          liveSyncDurationCount: 3,
          enableWorker: true,
          backBufferLength: 30,
          // the server answers 503 while the channel's encoder starts; a
          // provider's stream gets one more try, then the next stream
          manifestLoadPolicy: {
            default: ext
              ? {
                  maxTimeToFirstByteMs: 15_000,
                  maxLoadTimeMs: 20_000,
                  timeoutRetry: { maxNumRetry: 1, retryDelayMs: 0, maxRetryDelayMs: 0 },
                  errorRetry: { maxNumRetry: 1, retryDelayMs: 1000, maxRetryDelayMs: 1000 },
                }
              : {
                  maxTimeToFirstByteMs: Infinity,
                  maxLoadTimeMs: 30_000,
                  timeoutRetry: { maxNumRetry: 4, retryDelayMs: 0, maxRetryDelayMs: 0 },
                  errorRetry: { maxNumRetry: 10, retryDelayMs: 2000, maxRetryDelayMs: 4000 },
                },
          },
        })
        hls = h
        hlsRef.current = h
        h.on(H.Events.ERROR, (_e, data) => {
          if (!data.fatal || g !== gen) return
          if (data.type === H.ErrorTypes.MEDIA_ERROR) {
            h.recoverMediaError()
            return
          }
          fail(g, t('tv.failed'))
        })
        if (H.Events.MANIFEST_PARSED) h.on(H.Events.MANIFEST_PARSED, relist)
        if (ext && H.Events.MANIFEST_PARSED) {
          // an external stream's own variants (its master's), «Авто» first
          const levels = () => {
            if (gone || g !== gen) return
            setExtQuality(streamLevels(h, t('common.auto')))
          }
          h.on(H.Events.MANIFEST_PARSED, levels)
          if (H.Events.LEVEL_SWITCHED) h.on(H.Events.LEVEL_SWITCHED, levels)
        }
        if (H.Events.AUDIO_TRACKS_UPDATED) h.on(H.Events.AUDIO_TRACKS_UPDATED, relist)
        if (H.Events.SUBTITLE_TRACKS_UPDATED) h.on(H.Events.SUBTITLE_TRACKS_UPDATED, relist)
        h.loadSource(url)
        h.attachMedia(video)
        play()
      }, () => fail(g, t('tv.failed')))
    }
    // a raw MPEG-TS stream: mpegts.js reads it as it comes (the provider's
    // or the relay's) and hands the picture to the video through MSE
    const startTs = (url: string) => {
      const g = ++gen
      stopHls()
      stopTs()
      video.removeAttribute('src')
      void import('mpegts.js').then(({ default: M }) => {
        if (gone || g !== gen) return
        if (!M.isSupported()) return fail(g, t('tv.failed'))
        const p = M.createPlayer(
          // absolute: its loader may run in a worker, where a path (the relay's) does not resolve
          { type: 'mpegts', isLive: true, url: new URL(url, location.href).href },
          // a live picture: catch up when behind, keep the buffer trimmed; the
          // transmuxing stays on this thread (no blob worker built from source)
          { enableWorker: false, liveBufferLatencyChasing: true, lazyLoad: false, autoCleanupSourceBuffer: true },
        )
        ts = p
        p.on(M.Events.ERROR, () => fail(g, t('tv.failed')))
        p.attachMediaElement(video)
        p.load()
        play()
      }, () => fail(g, t('tv.failed')))
    }
    const startAt = (i: number) => {
      const c = ext![i]!
      if (c.format === 'ts') {
        asTs = true
        startTs(c.url)
      } else startHls(c.url)
    }
    if (ext) startAt(0)
    else startHls(liveUrl(channelId, own ?? 'auto'))
    // the picture moves: playing (a message an error left goes: something plays)
    const onPlaying = () => {
      if (gone) return
      movedAt = Date.now()
      restarted = false
      if (started) {
        setPhase((ph) => (ph === 'playing' ? ph : 'playing'))
        if (gaveUp) watchStall()
        return
      }
      started = true
      const r = video.seekable
      startOffset.current = hls || !r?.length ? 0 : Math.min(40, Math.max(0, r.end(r.length - 1) - video.currentTime))
      setPhase('playing')
      relist()
    }
    // time moving is the picture moving (Safari's own HLS may start without a «playing» event)
    let last = -1
    const onTime = () => {
      if (gone || video.paused) return
      if (last >= 0 && video.currentTime > last) onPlaying()
      last = video.currentTime
    }
    // the video's own error (the browser's HLS): the attempt playing in it now
    const onError = () => {
      if (video.error) fail(gen, t('tv.failed'))
    }
    video.addEventListener('timeupdate', onTime)
    video.addEventListener('playing', onPlaying)
    video.addEventListener('error', onError)
    video.addEventListener('loadedmetadata', relist)
    const nv = video as NativeVideo
    nv.audioTracks?.addEventListener?.('addtrack', relist)
    video.textTracks?.addEventListener?.('addtrack', relist)
    return () => {
      gone = true
      window.clearTimeout(later)
      video.removeEventListener('timeupdate', onTime)
      video.removeEventListener('playing', onPlaying)
      video.removeEventListener('error', onError)
      video.removeEventListener('loadedmetadata', relist)
      nv.audioTracks?.removeEventListener?.('addtrack', relist)
      video.textTracks?.removeEventListener?.('addtrack', relist)
      stopTs()
      stopHls()
      video.removeAttribute('src')
      video.load()
    }
  }, [channelId, attempt, t, source])

  // ---------- pause, resume, the live edge ----------
  const clockNow = useCallback(() => {
    const video = videoRef.current
    return video ? readClock({ video, hls: hlsRef.current, startOffset: Math.max(0, startOffset.current) }) : null
  }, [])
  const own = !channel?.external
  useEffect(() => {
    const video = videoRef.current
    if (!video || !source) return
    startOffset.current = -1
    setPaused(false)
    setBehind(false)
    let pausedAt = 0
    let behindAtPause = 0
    const onPause = () => {
      setPaused(true)
      pausedAt = Date.now()
      const c = clockNow()
      behindAtPause = c ? behindLive(c) : 0
    }
    // a resume: from the stopped picture while the stream holds it, else at the edge
    const onPlay = () => {
      setPaused(false)
      if (!pausedAt) return
      const c = clockNow()
      const to = c ? resumeTarget({ own, pausedMs: Date.now() - pausedAt, behindAtPause, clock: c }) : null
      pausedAt = 0
      if (to !== null) video.currentTime = to
    }
    const tick = () => {
      const c = clockNow()
      setBehind(Boolean(c && showGoLive(c)))
    }
    video.addEventListener('pause', onPause)
    video.addEventListener('play', onPlay)
    const id = window.setInterval(tick, 500)
    return () => {
      window.clearInterval(id)
      video.removeEventListener('pause', onPause)
      video.removeEventListener('play', onPlay)
    }
  }, [source, channelId, attempt, own, clockNow])
  const togglePause = useCallback(() => {
    const v = videoRef.current
    if (!v) return
    if (v.paused) void v.play().catch(() => undefined)
    else v.pause()
  }, [])
  const goLive = useCallback(() => {
    const v = videoRef.current
    const c = clockNow()
    if (!v || !c?.live) return
    v.currentTime = c.edge
    if (v.paused) void v.play().catch(() => undefined)
    setBehind(false)
  }, [clockNow])

  // why it failed, in the server's words (screen time over, nothing to show…)
  const failed = typeof phase === 'object' && 'error' in phase
  const isExternal = Boolean(channel?.external)
  useEffect(() => {
    if (!failed || isExternal) return
    fetch(liveUrl(channelId), { credentials: 'same-origin' })
      .then(async (r) => {
        if (r.ok) return
        const b = (await r.json().catch(() => null)) as { error?: string } | null
        if (b?.error) setPhase({ error: b.error })
      })
      .catch(() => undefined)
  }, [failed, channelId, isExternal])

  // ---------- audio and subtitles ----------
  // the programme's entries (or, from an older server, the renditions by name)
  const audioEntries = onAir?.audio ?? (renditions.audio.length > 1 ? entriesFromNames(renditions.audio) : undefined)
  const subEntries = onAir?.subtitles ?? (renditions.subs.length ? entriesFromNames(renditions.subs) : onAir?.audio ? [] : undefined)
  const audioNow = pickAudio(audioEntries, prefs, session, systemLanguages())
  const subsNow = pickSubs(subEntries, prefs, audioNow, session)
  const play = renditionsToPlay(audioNow, subsNow, subEntries !== undefined, renditions, session)
  // switch the renditions when the choice, the programme or the renditions change
  useEffect(() => {
    applyRenditions(hlsRef.current, videoRef.current as NativeVideo | null, play.audio, play.subs)
  }, [play.audio, play.subs, renditions])
  // the subtitle lines on screen (drawn here, so they rise above the info bar)
  const [cues, setCues] = useState<string[]>([])
  useEffect(() => {
    const read = () => {
      const next = activeCueText(videoRef.current, play.subs)
      setCues((c) => (c.length === next.length && c.every((x, i) => x === next[i]) ? c : next))
    }
    read()
    const id = window.setInterval(read, 200)
    return () => window.clearInterval(id)
  }, [play.subs, channelId])
  // the choice sticks by name per channel in this browser (ours and external ones alike)
  useEffect(() => {
    setSession(channelTracks(channelId))
  }, [channelId])
  const keep = (s: ReturnType<typeof channelTracks>) => {
    setChannelTracks(channelId, s)
    setSession(s)
  }
  const chooseAudio = (e: ChannelTrack) => keep(chooseAudioIn(session, e))
  const chooseSubs = (e: ChannelTrack | null) => keep(chooseSubsIn(session, e))

  // ---------- navigation ----------
  const close = useCallback(() => {
    if (fullscreenElement()) void document.exitFullscreen?.().catch(() => undefined)
    if (window.history.length > 1) navigate(-1)
    else navigate(fParam ? `/tv?f=${encodeURIComponent(fParam)}` : '/tv', { replace: true })
  }, [navigate, fParam])

  const hideTimer = useRef(0)
  const poke = useCallback(() => {
    setChrome(true)
    window.clearTimeout(hideTimer.current)
    hideTimer.current = window.setTimeout(() => setChrome(false), HIDE_MS)
  }, [])
  useEffect(() => {
    poke()
    return () => window.clearTimeout(hideTimer.current)
  }, [poke, channelId])

  const tune = useCallback(
    (to: Channel | undefined) => {
      if (!to || to.id === channelId) return
      navigate(`/tv/${to.id}${fParam ? `?f=${encodeURIComponent(fParam)}` : ''}`, { replace: true })
      poke()
    },
    [channelId, fParam, navigate, poke],
  )
  const zap = useCallback((step: number) => tune(zapTarget(zapList, channelId, step, channel?.number, favFirst)), [tune, zapList, channelId, channel?.number, favFirst])

  const favorite = (on: boolean) => {
    if (!channel) return
    setChannels((l) => withFavorite(l, channel.id, on))
    setChannelFavorite(channel.id, on).catch(() => setChannels((l) => withFavorite(l, channel.id, !on)))
  }

  // ---------- sound, picture in picture, full screen ----------
  // full screen: the player's element (its chrome comes along), else the
  // video's own on an iPhone (liveFullscreen.ts)
  const [fsReady, setFsReady] = useState(false)
  useEffect(() => setFsReady(canFullscreen(videoRef.current as FsVideo | null)), [])
  const toggleFull = useCallback(() => {
    toggleFullscreen(rootRef.current, videoRef.current as FsVideo | null)
  }, [])
  useEffect(() => onFullscreenChange(videoRef.current as FsVideo | null, () => setFull(isFullscreen(videoRef.current as FsVideo | null))), [])
  const [pipSupported, setPipSupported] = useState(false)
  useEffect(() => {
    const v = videoRef.current as WebkitVideo | null
    setPipSupported(Boolean(v && (document.pictureInPictureEnabled || v.webkitSupportsPresentationMode?.('picture-in-picture'))))
  }, [])
  const togglePip = () => {
    const v = videoRef.current as WebkitVideo | null
    if (!v) return
    if (document.pictureInPictureElement) void document.exitPictureInPicture().catch(() => undefined)
    else if (v.requestPictureInPicture) void v.requestPictureInPicture().catch(() => undefined)
    else v.webkitSetPresentationMode?.('picture-in-picture')
  }

  // ---------- a channel by its number ----------
  // digits typed (a keyboard, a TV remote's number keys) show «23_» on the
  // picture; the channel switches after a pause, or at once with Enter
  // (TiviMate's way). A number no channel has says so and goes.
  const [numberEntry, setNumberEntry] = useState<{ digits: string; missing?: boolean } | null>(null)
  const typed = useRef('')
  const numberTimer = useRef(0)
  const commitNumber = useRef<() => void>(() => undefined)
  commitNumber.current = () => {
    window.clearTimeout(numberTimer.current)
    const digits = typed.current
    typed.current = ''
    if (!digits) return
    const to = channelByNumber(channels, Number(digits))
    if (to) {
      setNumberEntry(null)
      if (to.id !== channelId) tune(to)
      return
    }
    setNumberEntry({ digits, missing: true })
    numberTimer.current = window.setTimeout(() => setNumberEntry(null), liveTiming.numberMs)
  }
  const typeDigit = useCallback((d: string) => {
    window.clearTimeout(numberTimer.current)
    typed.current = (typed.current + d).slice(-4)
    setNumberEntry({ digits: typed.current })
    numberTimer.current = window.setTimeout(() => commitNumber.current(), liveTiming.numberMs)
  }, [])
  const cancelNumber = useCallback(() => {
    window.clearTimeout(numberTimer.current)
    typed.current = ''
    setNumberEntry(null)
  }, [])
  useEffect(() => () => window.clearTimeout(numberTimer.current), [])

  // ---------- keys ----------
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || menuOpen) return
      const tag = (e.target as HTMLElement | null)?.tagName
      const onControl = tag === 'BUTTON' || tag === 'A' || tag === 'INPUT'
      // the focused volume slider keeps the arrows (↑/↓ move it, as ←/→ do), they do not zap
      const onSlider = tag === 'INPUT' && (e.target as HTMLInputElement).type === 'range'
      if (/^[0-9]$/.test(e.key) && (tag !== 'INPUT' || onSlider)) {
        e.preventDefault()
        typeDigit(e.key)
        return
      }
      if (typed.current && (e.key === 'Enter' || e.key === 'Escape')) {
        e.preventDefault()
        if (e.key === 'Enter') commitNumber.current()
        else cancelNumber()
        return
      }
      switch (e.key) {
        case ' ':
          if (onControl) return
          e.preventDefault()
          togglePause()
          break
        case 'k':
        case 'K':
        case 'л':
        case 'MediaPlayPause':
          togglePause()
          break
        case 'ArrowUp':
        case 'PageUp':
          if (onSlider || (panel && e.key === 'ArrowUp')) return
          e.preventDefault()
          zap(1)
          break
        case 'ArrowDown':
        case 'PageDown':
          if (onSlider || (panel && e.key === 'ArrowDown')) return
          e.preventDefault()
          zap(-1)
          break
        case 'i':
        case 'I':
        case 'ш':
          setInfo((v) => !v)
          break
        case 'Enter':
          if (onControl) return
          setInfo((v) => !v)
          break
        case 'c':
        case 'C':
        case 'с':
          setPanel((v) => !v)
          break
        case 'm':
        case 'M':
        case 'ь':
          toggleMute()
          break
        case 'f':
        case 'F':
        case 'а':
          toggleFull()
          break
        case 'Escape':
          if (panel) setPanel(false)
          else if (info) setInfo(false)
          else if (!fullscreenElement()) close()
          break
        default:
          return
      }
      poke()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [zap, toggleFull, close, poke, panel, info, menuOpen, togglePause, toggleMute, typeDigit, cancelNumber])

  // ---------- layout ----------
  // a phone held upright and not in full screen: the picture at the top, the rest under it
  const compact = upright && !full
  // «Каналы»: under the picture on an upright phone, else a panel over it
  const sideList = compact
  const floatingPanel = panel && !sideList

  // the current channel in view in the list: when it opens and when the channel changes
  // (centred if it was out of view; the list alone scrolls, never the page). The
  // list renders only the rows near the screen: the row's place is its index.
  const listRef = useRef<HTMLDivElement>(null)
  const listOpen = floatingPanel || sideList
  const listedCount = zapList.length
  const panelRowH = narrow ? PANEL_ROW_H.narrow : PANEL_ROW_H.wide
  const currentIndex = zapList.findIndex((c) => c.id === channelId)
  useEffect(() => {
    const list = listRef.current
    if (!list || currentIndex < 0) return
    const top = currentIndex * panelRowH
    if (top >= list.scrollTop && top + panelRowH <= list.scrollTop + list.clientHeight) return
    list.scrollTop = top - (list.clientHeight - panelRowH) / 2
  }, [channelId, listOpen, listedCount, currentIndex, panelRowH])

  const playing = phase === 'playing'
  const show = chromeShown({ poked: chrome, paused, playing, upright: false, info, panel, menuOpen })
  // the controls on the picture (they hide after a few quiet seconds; what is under the picture stays)
  const shown = show.bars
  const pointerType = useRef('mouse')
  const swipe = useRef<{ x: number; y: number } | null>(null)
  const pct = onAir ? progressOf(onAir, now) * 100 : 0
  const fav = Boolean(channel?.favorite)

  // ---------- parts ----------
  const liveMark = behind ? (
    <Button variant="secondary" material="clear" size="small" icon="broadcast" className={styles.goLive} onClick={() => (goLive(), poke())}>
      {t('tv.goLive')}
    </Button>
  ) : (
    <span className={`${styles.liveBadge} glass-clear`}>
      <span className={styles.liveDot} aria-hidden />
      {t('tv.live')}
    </span>
  )
  const star = (
    <IconButton
      icon={fav ? 'starFill' : 'star'}
      label={fav ? t('tv.favRemove') : t('tv.favAdd')}
      size="sm"
      variant="plain"
      material="clear"
      toggled={fav}
      className={styles.star}
      onClick={() => favorite(!fav)}
      disabled={!channel}
    />
  )
  const channelLine = (
    <div className={styles.channelLine}>
      {channel && (
        <h1 className={styles.channel}>
          <span className={styles.channelNum}>{channel.number}</span>
          <ChannelLogo url={channel.logoUrl} fallback={channel.logoAlt} name={channel.name} size="sm" />
          <span className={styles.channelName}>{channel.name}</span>
        </h1>
      )}
      {star}
    </div>
  )
  const timeline = (withLive: boolean) =>
    !onAir ? (
      withLive && <div className={styles.timeline}>{liveMark}</div>
    ) : (
      <div className={styles.timeline}>
        <span className={styles.time}>{clock(onAir.start)}</span>
        <div className={styles.bar} role="progressbar" aria-label={t('tv.onAirNow')} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(pct)}>
          <div className={styles.barFill} style={{ width: `${pct}%` }} />
        </div>
        <span className={styles.time}>{clock(onAir.end)}</span>
        {withLive && liveMark}
      </div>
    )
  const programme = (opts: { line?: boolean; live?: boolean }) => (
    <div className={styles.info} aria-live="polite" data-testid="live-info">
      {opts.line && channelLine}
      <p className={styles.programTitle}>{onAir ? onAir.title : channel ? t('tv.nothing') : ''}</p>
      {onAir && (onAir.subtitle || onAir.year || onAir.genres?.length) && (
        <p className={styles.programSub}>{[onAir.subtitle, onAir.subtitle ? undefined : onAir.year, ...(onAir.subtitle ? [] : (onAir.genres ?? []).slice(0, 2))].filter(Boolean).join(' · ')}</p>
      )}
      {info && onAir?.overview && <p className={styles.overview}>{onAir.overview}</p>}
      {timeline(Boolean(opts.live))}
      {next && <p className={styles.next}>{t('tv.nextAt', { time: clock(next.start), title: [next.title, next.subtitle].filter(Boolean).join(' · ') })}</p>}
    </div>
  )
  const tracks = (
    <LiveTrackMenu
      audio={audioEntries}
      subtitles={subEntries}
      audioName={audioNow?.name}
      subsName={subsNow ? subsNow.name : null}
      onAudio={chooseAudio}
      onSubtitles={chooseSubs}
      onOpenChange={setMenuOpen}
      placement="bottom-end"
    />
  )
  const chooseExtQuality = (q: string) => {
    const h = hlsRef.current
    if (!h) return
    h.currentLevel = q === t('common.auto') ? -1 : levelOf(h, q)
    setExtQuality(streamLevels(h, t('common.auto')))
  }
  const qualityMenu = !listed ? null : channel?.external ? (
    extQuality && <StreamQualityMenu info={extQuality} onChange={chooseExtQuality} onOpenChange={setMenuOpen} />
  ) : (
    <QualityMenu
      quality={quality}
      info={qualityInfo}
      onChange={(q) => {
        saveQuality(q)
        setQuality(q)
      }}
      onOpenChange={setMenuOpen}
    />
  )
  const pipButton = pipSupported && <IconButton icon="pip" label={t('tv.pip')} size="sm" material="clear" onClick={togglePip} />
  const fullButton = fsReady && (
    <IconButton icon={full ? 'collapse' : 'expand'} label={full ? t('tv.exitFullscreen') : t('tv.fullscreen')} tooltip={`${full ? t('tv.exitFullscreen') : t('tv.fullscreen')} (F)`} size="sm" material="clear" onClick={() => (toggleFull(), poke())} />
  )
  const volumeControl = !narrow && (
    <VolumeControl volume={volume} muted={muted} onVolume={changeVolume} onToggleMute={toggleMute} muteLabel={t('tv.mute')} unmuteLabel={t('tv.unmute')} />
  )
  const channelsButton = (
    <IconButton icon="list" label={t('tv.channelsPanel')} tooltip={`${t('tv.channelsPanel')} (C)`} size="sm" material="clear" toggled={panel} aria-expanded={panel} onClick={() => setPanel((v) => !v)} />
  )
  const infoButton = !narrow && <IconButton icon="info" label={t('tv.info')} tooltip={`${t('tv.info')} (I)`} size="sm" material="clear" toggled={info} onClick={() => setInfo((v) => !v)} />
  const closeButton = <IconButton icon="close" label={t('tv.close')} size="sm" material="clear" className={styles.close} onClick={close} />
  const channelList = (
    <>
      <div className={styles.panelList} ref={listRef} style={{ '--num-digits': numDigits(zapList) } as CSSProperties}>
        <VirtualList
          count={zapList.length}
          itemHeight={panelRowH}
          itemKey={(i) => zapList[i]!.id}
          label={t('tv.channelsPanel')}
          renderItem={(i) => {
            const c = zapList[i]!
            const p = c.now && isOnAir(c.now, now) ? c.now : c.next && isOnAir(c.next, now) ? c.next : c.now
            const current = c.id === channelId
            return (
              <button
                type="button"
                className={styles.panelRow}
                aria-current={current || undefined}
                onClick={() => tune(c)}
                onKeyDown={(e) => {
                  if (e.key !== 'ArrowUp' && e.key !== 'ArrowDown') return
                  e.preventDefault()
                  const item = e.currentTarget.parentElement
                  const to = (e.key === 'ArrowUp' ? item?.previousElementSibling : item?.nextElementSibling)?.querySelector('button')
                  to?.focus()
                }}
              >
                <span className={styles.panelNum}>{c.number}</span>
                <ChannelLogo url={c.logoUrl} fallback={c.logoAlt} name={c.name} size="xs" />
                <span className={styles.panelText}>
                  <span className={styles.panelName}>
                    <span className={styles.panelNameText}>{c.name}</span>
                    {c.favorite && <Icon name="starFill" size={12} />}
                  </span>
                  <span className={styles.panelTitle}>{p ? p.title : t('tv.nothing')}</span>
                  {p && (
                    <span className={styles.panelBar}>
                      <span style={{ width: `${progressOf(p, now) * 100}%` }} />
                    </span>
                  )}
                </span>
                <ChannelArt program={p} width={96} optional className={styles.panelArt} />
              </button>
            )
          }}
        />
      </div>
    </>
  )
  return (
    <div
      ref={rootRef}
      className={[styles.root, shown ? '' : styles.hidden].join(' ')}
      data-compact={compact || undefined}
      data-full={full || undefined}
      onPointerMove={(e) => e.pointerType === 'mouse' && poke()}
      data-portal-host
      data-testid="live-player"
      role="region"
      aria-label={channel ? t('tv.channelNumber', { number: channel.number, name: channel.name }) : t('tv.title')}
    >
      {/* the picture and everything on it: loading, errors and controls stay inside it */}
      <div className={styles.stage}>
        {/* while the channel starts, the programme's still stands in for the picture */}
        <video
          ref={videoRef}
          className={styles.video}
          playsInline
          autoPlay
          x-webkit-airplay="allow"
          poster={phase !== 'playing' ? imageUrl(onAir?.backdrop, 1280) : undefined}
        />
        {/* a tap shows or hides the controls; a vertical swipe zaps; a double click is full screen */}
        <div
          className={styles.surface}
          aria-hidden
          onPointerDown={(e) => {
            pointerType.current = e.pointerType
            swipe.current = e.pointerType === 'touch' ? { x: e.clientX, y: e.clientY } : null
          }}
          onPointerUp={(e) => {
            const s = swipe.current
            swipe.current = null
            if (!s) return
            const dx = e.clientX - s.x
            const dy = e.clientY - s.y
            if (Math.abs(dy) > 60 && Math.abs(dy) > 2 * Math.abs(dx)) {
              pointerType.current = 'swipe'
              zap(dy < 0 ? 1 : -1)
            }
          }}
          onClick={() => {
            if (pointerType.current === 'swipe') return
            if (panel) setPanel(false)
            // a finger toggles: what shows hides
            else if (shown && phase === 'playing' && pointerType.current === 'touch') {
              setChrome(false)
              setInfo(false)
            } else poke()
          }}
          onDoubleClick={() => fsReady && toggleFull()}
        />
        <div className={styles.topScrim} aria-hidden />
        <div className={styles.bottomScrim} aria-hidden />
        {cues.length > 0 && (
          <div className={styles.subtitles} aria-live="off" data-size={prefs?.subSize ?? 'm'} data-bg={prefs?.subBackground ?? 'translucent'}>
            {cues.map((c, i) => (
              <p key={`${i}-${c}`}>
                <span className={look.cue}>{c}</span>
              </p>
            ))}
          </div>
        )}

        <header className={styles.top}>
          {closeButton}
          {!compact && <div className={styles.heading}>{channelLine}</div>}
          <div className={styles.topActions}>
            {tracks}
            {pipButton}
          </div>
        </header>

        {playing && (
          <div className={`${styles.center} ${styles.playCenter}`} data-hidden={show.center ? undefined : ''}>
            <button
              type="button"
              className={`${styles.bigPlay} glass-clear`}
              onClick={() => (togglePause(), poke())}
              tabIndex={show.center ? 0 : -1}
              aria-label={paused ? t('tv.resume') : t('common.pause')}
              title={`${paused ? t('tv.resume') : t('common.pause')} (K)`}
            >
              <Icon name={paused ? 'play' : 'pause'} size={compact ? 32 : 44} />
            </button>
          </div>
        )}
        {phase === 'starting' && (
          <div className={styles.center}>
            <span className={styles.spinner} role="status" aria-label={t('tv.starting')} />
            <span className={styles.note}>{t('tv.starting')}</span>
          </div>
        )}
        {typeof phase === 'object' && 'ext' in phase && (
          <div className={styles.center}>
            <div className={`${styles.error} ${styles.extError} glass-strong`} role="alert">
              <h2>{t('tv.extOnlyApps')}</h2>
              <p>{t(`tv.extWhy.${phase.ext}`)}</p>
              {phase.ext === 'network' && (
                <Button variant="secondary" size="small" onClick={() => setAttempt((n) => n + 1)}>
                  {t('tv.retry')}
                </Button>
              )}
            </div>
          </div>
        )}
        {typeof phase === 'object' && 'error' in phase && (
          <div className={styles.center}>
            <div className={`${styles.error} glass-strong`} role="alert">
              <h2>{t('tv.failed')}</h2>
              {phase.error !== t('tv.failed') && <p>{phase.error}</p>}
              <Button variant="secondary" size="small" onClick={() => setAttempt((n) => n + 1)}>
                {t('tv.retry')}
              </Button>
            </div>
          </div>
        )}

        {/* the bottom of the picture: on an upright phone «Прямой эфир» and full
            screen (the programme is under the picture); else the programme,
            its timeline and the tools */}
        {compact ? (
          <footer className={styles.bottom}>
            <div className={styles.tools}>
              <div className={styles.toolsLeft}>{liveMark}</div>
              <div className={styles.toolsRight}>
                {qualityMenu}
                {fullButton}
              </div>
            </div>
          </footer>
        ) : (
          <footer className={styles.bottom}>
            {programme({ live: true })}
            <div className={styles.tools}>
              <div className={styles.toolsLeft}>{volumeControl}</div>
              <div className={styles.toolsRight}>
                {qualityMenu}
                {channelsButton}
                {infoButton}
                {fullButton}
              </div>
            </div>
          </footer>
        )}
        {numberEntry && (
          <div className={`${styles.numberEntry} glass-strong`} role="status" aria-live="polite" data-testid="channel-number-entry">
            {numberEntry.missing ? t('tv.noChannelNumber', { number: numberEntry.digits }) : `${numberEntry.digits}_`}
          </div>
        )}
      </div>

      {/* under the picture on an upright phone: the programme, then the channels */}
      {compact && (
        <div className={styles.below}>
          {programme({ line: true })}
          <aside className={styles.panelInline} aria-label={t('tv.channelsPanel')}>
            <header className={styles.panelHead}>
              <h2>{t('tv.channelsPanel')}</h2>
            </header>
            {channelList}
          </aside>
        </div>
      )}

      {floatingPanel && (
        <aside className={`${styles.panel} glass-strong`} aria-label={t('tv.channelsPanel')}>
          <header className={styles.panelHead}>
            <h2>{t('tv.channelsPanel')}</h2>
          </header>
          {channelList}
        </aside>
      )}
    </div>
  )
}

/** A variant's label: its height («720p»), else its bit rate. */
function levelLabel(l: { height?: number; bitrate?: number }): string {
  return l.height ? `${l.height}p` : `${Math.round((l.bitrate ?? 0) / 1000)} kbps`
}

/** An external stream's «Качество»: «Авто» and its variants, best first (none: one variant only). */
function streamLevels(h: Hls, auto: string): StreamQualityInfo | null {
  const labels: string[] = []
  for (const l of [...(h.levels ?? [])].sort((a, b) => (b.height || 0) - (a.height || 0) || b.bitrate - a.bitrate)) {
    const x = levelLabel(l)
    if (!labels.includes(x)) labels.push(x)
  }
  if (labels.length < 2) return null
  const cur = h.levels?.[h.currentLevel]
  return { options: [auto, ...labels], current: h.autoLevelEnabled || !cur ? auto : levelLabel(cur) }
}

/** The variant of a label: the best one of that height. */
function levelOf(h: Hls, label: string): number {
  let best = -1
  h.levels?.forEach((l, i) => {
    if (levelLabel(l) === label && (best < 0 || l.bitrate > h.levels[best]!.bitrate)) best = i
  })
  return best
}
