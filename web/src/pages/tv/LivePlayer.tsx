// /tv/:id — a channel live, full window, in the film player's chrome. The
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
// from (?f=: all, favourites or one theme), in number order, wrapping;
// i or Enter shows the info (with the description) and hides it; C the
// channels; M sound; F full screen; Escape closes what is open, then the
// player.
//
// An external channel (an IPTV playlist's) plays the provider's stream
// directly: its URL, then its fallbacks, once each. When the web cannot
// play it (headers a browser cannot send, http on an https page, a
// provider that refuses other sites — CORS — or a dead stream) the player
// says so: the channel opens in the Antresol apps or an IPTV player. Its
// audio and subtitles come from the stream.
//
// The voice, on every channel: the viewer's choice on that channel (kept
// per channel in this browser, externalStream.ts), else the device's
// language, else the profile's audio languages, else the original
// (model.pickAudio). The interface language stays the profile's.
//
// TV is TV: this player writes nothing but the profile's «last channel»
// (PUT /api/channels/last) and its stars — no progress, no playback
// reports, no track memory on the server (__tests__/liveNoHistory.test.tsx).
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router'
import type Hls from 'hls.js'
import { APP_NAME } from '../../brand'
import { getPlaybackPrefs, imageUrl } from '../../api/client'
import { getChannelGuide, getChannels, liveUrl, setChannelFavorite, setLastChannel, type Channel, type ChannelProgram, type ChannelTrack } from '../../api/channels'
import type { PlaybackPrefs } from '../../api/types'
import { Icon } from '../../components/Icon'
import { useT } from '../../i18n'
import { audio as audioEngine } from '../../player/audio/engine'
import { nativeHls } from '../../player/hlsSupport'
import { Button } from '../../ui/Button'
import { Menu, MenuItem } from '../../ui/Menu'
import { useMediaQuery } from '../../state/useMediaQuery'
import { ChannelArt } from './ChannelArt'
import { ChannelLogo } from './ChannelLogo'
import { activeCueText, applyRenditions, entriesFromNames, renditionNames, type NativeVideo } from './liveTracks'
import { LiveTrackMenu } from './LiveTrackMenu'
import { applyFilter, chooseAudioIn, chooseSubsIn, isOnAir, minutesLeft, numDigits, parseFilter, pickAudio, pickSubs, progressOf, renditionsToPlay, systemLanguages, withFavorite, zapTarget, type Rendition } from './model'
import { clock, useNow } from './useChannels'
import { chromeShown, readClock, resumeTarget, showGoLive, behindLive } from './liveEdge'
import { channelTracks, externalStreams, setChannelTracks, type ExtBlock } from './externalStream'
import look from '../../player/SubtitleLook.module.css'
import styles from './LivePlayer.module.css'

const HIDE_MS = 4000
const GUIDE_MS = 30_000

type Phase = 'starting' | 'playing' | { error: string } | { ext: ExtBlock }

type WebkitVideo = NativeVideo & {
  webkitSupportsPresentationMode?: (m: string) => boolean
  webkitSetPresentationMode?: (m: string) => void
}
type WebkitDocument = Document & { webkitFullscreenEnabled?: boolean; webkitFullscreenElement?: Element | null; webkitExitFullscreen?: () => void }
type WebkitElement = HTMLElement & { webkitRequestFullscreen?: () => void }
const doc = () => document as WebkitDocument
const fullscreenElement = () => doc().fullscreenElement ?? doc().webkitFullscreenElement ?? null
const fullscreenSupported = () => typeof document !== 'undefined' && Boolean(doc().fullscreenEnabled || doc().webkitFullscreenEnabled)

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
  const [listed, setListed] = useState(false)
  const [programs, setPrograms] = useState<ChannelProgram[]>([])
  const [phase, setPhase] = useState<Phase>('starting')
  const [muted, setMuted] = useState(false)
  const [volume, setVolume] = useState(1)
  const [full, setFull] = useState(false)
  const [chrome, setChrome] = useState(true)
  const [info, setInfo] = useState(false)
  const [panel, setPanel] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const [prefs, setPrefs] = useState<PlaybackPrefs | null>(null)
  const [session, setSession] = useState(() => channelTracks(channelId))
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
  const source = !listed ? '' : external ? (external.blocked ? `blocked:${external.blocked}` : `ext:${external.urls.join(' ')}`) : 'own'
  // the list ↑/↓ zap through: the one it was opened from
  const zapList = useMemo(() => {
    const l = applyFilter(channels, filter)
    return l.length ? l : applyFilter(channels, { kind: 'all' })
  }, [channels, filter])
  const onAir = programs.find((p) => Date.parse(p.end) > now) ?? (channel?.now && isOnAir(channel.now, now) ? channel.now : undefined)
  const next = onAir ? (programs[programs.indexOf(onAir) + 1] ?? channel?.next) : undefined

  // the list, its now/next for «Каналы»; the profile's defaults for the tracks
  useEffect(() => {
    let ac = new AbortController()
    const load = () => {
      ac.abort()
      ac = new AbortController()
      getChannels(ac.signal).then(
        (l) => {
          setChannels(l.channels)
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

  // the stream: native HLS or hls.js; a new channel (or «Повторить») starts over
  useEffect(() => {
    const video = videoRef.current
    if (!video || !source) return
    let hls: Hls | null = null
    let gone = false
    setPhase('starting')
    setRenditions({ audio: [], subs: [] })
    if (source.startsWith('blocked:')) {
      setPhase({ ext: source.slice('blocked:'.length) as ExtBlock })
      return
    }
    const ext = source.startsWith('ext:') ? source.slice(4).split(' ') : null
    let at = 0
    const url = ext ? ext[0]! : liveUrl(channelId)
    // an external stream that fails moves on to the next one, once; none left: the web cannot play it
    const fail = (msg: string) => {
      if (gone) return
      if (!ext) return setPhase({ error: msg })
      at++
      if (at < ext.length) {
        if (hls) {
          hls.stopLoad()
          hls.loadSource(ext[at]!)
        } else {
          video.src = ext[at]!
        }
        play()
        return
      }
      hls?.stopLoad()
      setPhase({ ext: 'network' })
    }
    const relist = () => !gone && setRenditions(renditionNames(hls, video as NativeVideo))
    // a browser that refuses to start it with sound (no tap before it) starts it muted
    const play = () =>
      void video
        .play()
        .catch(() => {
          if (gone) return
          video.muted = true
          setMuted(true)
          return video.play()
        })
        .catch(() => undefined)
    const native = nativeHls(video)
    if (native) {
      video.src = url
      play()
    } else {
      void import('hls.js').then(({ default: H }) => {
        if (gone) return
        hls = new H({
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
        hlsRef.current = hls
        hls.on(H.Events.ERROR, (_e, data) => {
          if (!data.fatal) return
          if (data.type === H.ErrorTypes.MEDIA_ERROR) {
            hls?.recoverMediaError()
            return
          }
          fail(t('tv.failed'))
        })
        if (H.Events.MANIFEST_PARSED) hls.on(H.Events.MANIFEST_PARSED, relist)
        if (H.Events.AUDIO_TRACKS_UPDATED) hls.on(H.Events.AUDIO_TRACKS_UPDATED, relist)
        if (H.Events.SUBTITLE_TRACKS_UPDATED) hls.on(H.Events.SUBTITLE_TRACKS_UPDATED, relist)
        hls.loadSource(url)
        hls.attachMedia(video)
        play()
      }, () => fail(t('tv.failed')))
    }
    let started = false
    const onPlaying = () => {
      if (gone || started) return
      started = true
      const r = video.seekable
      startOffset.current = hls || !r?.length ? 0 : Math.min(40, Math.max(0, r.end(r.length - 1) - video.currentTime))
      setPhase('playing')
      relist()
    }
    // Safari's own HLS may start without a «playing» event: time moving is enough
    let last = -1
    const onTime = () => {
      if (gone || video.paused) return
      if (last >= 0 && video.currentTime > last) onPlaying()
      last = video.currentTime
    }
    const onError = () => fail(t('tv.failed'))
    video.addEventListener('timeupdate', onTime)
    video.addEventListener('playing', onPlaying)
    video.addEventListener('error', onError)
    video.addEventListener('loadedmetadata', relist)
    const nv = video as NativeVideo
    nv.audioTracks?.addEventListener?.('addtrack', relist)
    video.textTracks?.addEventListener?.('addtrack', relist)
    return () => {
      gone = true
      video.removeEventListener('timeupdate', onTime)
      video.removeEventListener('playing', onPlaying)
      video.removeEventListener('error', onError)
      video.removeEventListener('loadedmetadata', relist)
      nv.audioTracks?.removeEventListener?.('addtrack', relist)
      video.textTracks?.removeEventListener?.('addtrack', relist)
      hls?.destroy()
      hlsRef.current = null
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
    else navigate('/tv', { replace: true })
  }, [navigate])

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
  const zap = useCallback((step: number) => tune(zapTarget(zapList, channelId, step, channel?.number)), [tune, zapList, channelId, channel?.number])

  const favorite = (on: boolean) => {
    if (!channel) return
    setChannels((l) => withFavorite(l, channel.id, on))
    setChannelFavorite(channel.id, on).catch(() => setChannels((l) => withFavorite(l, channel.id, !on)))
  }

  // ---------- sound, picture in picture, full screen ----------
  useEffect(() => {
    const v = videoRef.current
    if (!v) return
    v.muted = muted
    v.volume = volume
  }, [muted, volume])
  const canFullscreen = fullscreenSupported()
  const toggleFull = useCallback(() => {
    const root = rootRef.current as WebkitElement | null
    if (fullscreenElement()) {
      if (document.exitFullscreen) void document.exitFullscreen().catch(() => undefined)
      else doc().webkitExitFullscreen?.()
    } else if (root?.requestFullscreen) void root.requestFullscreen().catch(() => undefined)
    else root?.webkitRequestFullscreen?.()
  }, [])
  useEffect(() => {
    const on = () => setFull(Boolean(fullscreenElement()))
    document.addEventListener('fullscreenchange', on)
    document.addEventListener('webkitfullscreenchange', on)
    return () => {
      document.removeEventListener('fullscreenchange', on)
      document.removeEventListener('webkitfullscreenchange', on)
    }
  }, [])
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

  // ---------- keys ----------
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || menuOpen) return
      const tag = (e.target as HTMLElement | null)?.tagName
      const onControl = tag === 'BUTTON' || tag === 'A' || tag === 'INPUT'
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
          if (panel && e.key === 'ArrowUp') return
          e.preventDefault()
          zap(1)
          break
        case 'ArrowDown':
        case 'PageDown':
          if (panel && e.key === 'ArrowDown') return
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
          setMuted((m) => !m)
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
  }, [zap, toggleFull, close, poke, panel, info, menuOpen, togglePause])

  // the current channel in view in the list: when it opens and when the channel changes
  // (centred if it was out of view; the list alone scrolls, never the page)
  const listRef = useRef<HTMLUListElement>(null)
  const listOpen = panel || upright
  const listedCount = zapList.length
  useEffect(() => {
    const list = listRef.current
    const row = list?.querySelector<HTMLElement>('[aria-current]')
    if (!list || !row) return
    const lr = list.getBoundingClientRect()
    const rr = row.getBoundingClientRect()
    if (rr.top >= lr.top && rr.bottom <= lr.bottom) return
    list.scrollTop += rr.top - lr.top - (lr.height - rr.height) / 2
  }, [channelId, listOpen, listedCount])

  const playing = phase === 'playing'
  const show = chromeShown({ poked: chrome, paused, playing, upright, info, panel, menuOpen })
  const shown = show.bars
  const pointerType = useRef('mouse')
  const pct = onAir ? progressOf(onAir, now) * 100 : 0
  const fav = Boolean(channel?.favorite)

  return (
    <div
      ref={rootRef}
      className={[styles.root, shown ? '' : styles.hidden].join(' ')}
      onPointerMove={(e) => e.pointerType === 'mouse' && poke()}
      data-portal-host
      data-testid="live-player"
      role="region"
      aria-label={channel ? t('tv.channelNumber', { number: channel.number, name: channel.name }) : t('tv.title')}
    >
      {/* while the channel starts, the programme's still stands in for the picture */}
      <video
        ref={videoRef}
        className={styles.video}
        playsInline
        autoPlay
        x-webkit-airplay="allow"
        poster={phase !== 'playing' ? imageUrl(onAir?.backdrop, 1280) : undefined}
      />
      {/* a tap shows or hides the info; a double click is full screen */}
      <div
        className={styles.surface}
        aria-hidden
        onPointerDown={(e) => {
          pointerType.current = e.pointerType
        }}
        onClick={() => {
          if (panel) setPanel(false)
          // a finger toggles: what shows hides (on an upright phone the bars stay: the centre button goes)
          else if ((upright ? show.center : shown) && phase === 'playing' && pointerType.current === 'touch') {
            setChrome(false)
            setInfo(false)
          } else poke()
        }}
        onDoubleClick={() => canFullscreen && toggleFull()}
      />
      <div className={styles.topScrim} aria-hidden />
      {cues.length > 0 && (
        <div className={styles.subtitles} aria-live="off" data-size={prefs?.subSize ?? 'm'} data-bg={prefs?.subBackground ?? 'translucent'}>
          {cues.map((c, i) => (
            <p key={`${i}-${c}`}>
              <span className={look.cue}>{c}</span>
            </p>
          ))}
        </div>
      )}
      <div className={styles.bottomScrim} aria-hidden />

      <header className={styles.top}>
        <button type="button" className={`${styles.round} glass`} onClick={close} aria-label={t('tv.close')} title={t('tv.close')}>
          <Icon name="close" size={22} strokeWidth={2.2} />
        </button>
        <div className={styles.heading}>
          {channel && (
            <h1 className={styles.channel}>
              <span className={styles.channelNum}>{channel.number}</span>
              <ChannelLogo url={channel.logoUrl} size="md" />
              <span className={styles.channelName}>{channel.name}</span>
            </h1>
          )}
          {behind ? (
            <Button variant="secondary" material="clear" size="small" icon="broadcast" className={styles.goLive} onClick={() => (goLive(), poke())}>
              {t('tv.goLive')}
            </Button>
          ) : (
            <p className={styles.live}>
              <span className={styles.liveDot} aria-hidden />
              {t('tv.live')}
            </p>
          )}
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
            <Icon name={paused ? 'play' : 'pause'} size={44} />
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
              <Button variant="secondary" size={upright ? 'small' : 'regular'} onClick={() => setAttempt((n) => n + 1)}>
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
            <Button variant="secondary" onClick={() => setAttempt((n) => n + 1)}>
              {t('tv.retry')}
            </Button>
          </div>
        </div>
      )}

      <div className={styles.dock}>
      <footer className={styles.bottom}>
        <div className={styles.info} aria-live="polite">
          <p className={styles.programTitle}>{onAir ? onAir.title : channel ? t('tv.nothing') : ''}</p>
          {onAir && (onAir.subtitle || onAir.year || onAir.genres?.length) && (
            <p className={styles.programSub}>{[onAir.subtitle, onAir.subtitle ? undefined : onAir.year, ...(onAir.subtitle ? [] : (onAir.genres ?? []).slice(0, 2))].filter(Boolean).join(' · ')}</p>
          )}
          {info && onAir?.overview && <p className={styles.overview}>{onAir.overview}</p>}
          {onAir && (
            <div className={styles.timeline}>
              <span className={styles.time}>{clock(onAir.start)}</span>
              <div className={styles.bar} role="progressbar" aria-label={t('tv.onAirNow')} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(pct)}>
                <div className={styles.barFill} style={{ width: `${pct}%` }} />
              </div>
              <span className={styles.time}>{clock(onAir.end)}</span>
              <span className={styles.left}>{t('tv.left', { count: minutesLeft(onAir, now) })}</span>
            </div>
          )}
          {next && <p className={styles.next}>{t('tv.nextAt', { time: clock(next.start), title: [next.title, next.subtitle].filter(Boolean).join(' · ') })}</p>}
        </div>

        <div className={styles.tools}>
          <div className={styles.toolsLeft}>
            <button type="button" className={styles.tool} onClick={() => zap(1)} disabled={zapList.length < 2} aria-label={t('tv.channelUp')} title={`${t('tv.channelUp')} (↑)`}>
              <Icon name="chevronUp" size={24} strokeWidth={2.2} />
            </button>
            <button type="button" className={styles.tool} onClick={() => zap(-1)} disabled={zapList.length < 2} aria-label={t('tv.channelDown')} title={`${t('tv.channelDown')} (↓)`}>
              <Icon name="chevronDown" size={24} strokeWidth={2.2} />
            </button>
            <button
              type="button"
              className={styles.tool}
              onClick={() => favorite(!fav)}
              disabled={!channel}
              aria-pressed={fav}
              aria-label={fav ? t('tv.favRemove') : t('tv.favAdd')}
              title={fav ? t('tv.favRemove') : t('tv.favAdd')}
            >
              <Icon name={fav ? 'starFill' : 'star'} size={24} />
            </button>
          </div>
          <div className={styles.toolsRight}>
            {!upright && (
              <button
                type="button"
                className={styles.tool}
                onClick={() => setPanel((v) => !v)}
                aria-expanded={panel}
                aria-label={t('tv.channelsPanel')}
                title={`${t('tv.channelsPanel')} (C)`}
                data-active={panel || undefined}
              >
                <Icon name="list" size={24} />
              </button>
            )}
            {!narrow && (
              <button type="button" className={styles.tool} onClick={() => setInfo((v) => !v)} aria-pressed={info} aria-label={t('tv.info')} title={`${t('tv.info')} (I)`} data-active={info || undefined}>
                <Icon name="info" size={24} />
              </button>
            )}
            <LiveTrackMenu
              audio={audioEntries}
              subtitles={subEntries}
              audioName={audioNow?.name}
              subsName={subsNow ? subsNow.name : null}
              onAudio={chooseAudio}
              onSubtitles={chooseSubs}
              onOpenChange={setMenuOpen}
            />
            <div className={styles.volume}>
              <button type="button" className={styles.tool} onClick={() => setMuted((m) => !m)} aria-label={muted ? t('tv.unmute') : t('tv.mute')} title={`${muted ? t('tv.unmute') : t('tv.mute')} (M)`}>
                <Icon name={muted || volume === 0 ? 'speakerMute' : 'speaker'} size={24} />
              </button>
              <input
                type="range"
                min={0}
                max={1}
                step={0.05}
                value={muted ? 0 : volume}
                onChange={(e) => {
                  const v = Number(e.target.value)
                  setVolume(v)
                  if (v > 0) setMuted(false)
                }}
                aria-label={t('player.volume')}
                className={styles.volumeRange}
                style={{ ['--v' as string]: `${(muted ? 0 : volume) * 100}%` }}
              />
            </div>
            {narrow && (
              <Menu
                label={t('tv.more')}
                placement="top-end"
                onOpenChange={setMenuOpen}
                trigger={(tp) => (
                  <button {...tp} type="button" className={styles.tool} aria-label={t('tv.more')} title={t('tv.more')}>
                    <Icon name="ellipsis" size={24} />
                  </button>
                )}
              >
                {(closeMenu) => (
                  <>
                    <MenuItem icon={<Icon name="info" size={16} />} label={t('tv.info')} checked={info} multi onSelect={() => (closeMenu(), setInfo((v) => !v))} />
                    {pipSupported && <MenuItem icon={<Icon name="pip" size={16} />} label={t('tv.pip')} onSelect={() => (closeMenu(), togglePip())} />}
                    {canFullscreen && <MenuItem icon={<Icon name={full ? 'collapse' : 'expand'} size={16} />} label={full ? t('tv.exitFullscreen') : t('tv.fullscreen')} onSelect={() => (closeMenu(), toggleFull())} />}
                  </>
                )}
              </Menu>
            )}
            {!narrow && pipSupported && (
              <button type="button" className={styles.tool} onClick={togglePip} aria-label={t('tv.pip')} title={t('tv.pip')}>
                <Icon name="pip" size={24} />
              </button>
            )}
            {!narrow && canFullscreen && (
              <button type="button" className={styles.tool} onClick={toggleFull} aria-label={full ? t('tv.exitFullscreen') : t('tv.fullscreen')} title={`${full ? t('tv.exitFullscreen') : t('tv.fullscreen')} (F)`}>
                <Icon name={full ? 'collapse' : 'expand'} size={22} strokeWidth={2.1} />
              </button>
            )}
          </div>
        </div>
      </footer>

      {(panel || upright) && (
        <aside className={upright ? styles.panelInline : `${styles.panel} glass-strong`} aria-label={t('tv.channelsPanel')}>
          <header className={styles.panelHead}>
            <h2>{t('tv.channelsPanel')}</h2>
          </header>
          <ul className={styles.panelList} ref={listRef} style={{ '--num-digits': numDigits(zapList) } as CSSProperties}>
            {zapList.map((c) => {
              const stripLogos = zapList.some((x) => x.logoUrl)
              const p = c.now && isOnAir(c.now, now) ? c.now : c.next && isOnAir(c.next, now) ? c.next : c.now
              const current = c.id === channelId
              return (
                <li key={c.id}>
                  <button
                    type="button"
                    className={styles.panelRow}
                    data-logos={stripLogos || undefined}
                    aria-current={current || undefined}
                    onClick={() => tune(c)}
                    onKeyDown={(e) => {
                      if (e.key !== 'ArrowUp' && e.key !== 'ArrowDown') return
                      e.preventDefault()
                      const li = e.currentTarget.parentElement
                      const to = (e.key === 'ArrowUp' ? li?.previousElementSibling : li?.nextElementSibling)?.querySelector('button')
                      to?.focus()
                    }}
                  >
                    <span className={styles.panelNum}>{c.number}</span>
                    <ChannelArt program={p} width={96} className={styles.panelArt} />
                    {stripLogos && <ChannelLogo url={c.logoUrl} size="xs" reserve />}
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
                  </button>
                </li>
              )
            })}
          </ul>
        </aside>
      )}
      </div>
    </div>
  )
}
