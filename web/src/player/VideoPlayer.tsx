// Full-screen video player with Apple TV–style controls. The engine lives in
// usePlayback (sessions, seeking, recovery), saving in useProgress and the
// shortcuts in usePlayerKeys; this file is the picture and the controls.
//
// Multi-part movies play their parts in a row on one timeline. Subtitles are
// drawn by the player itself (HLS streams start mid-file, so the cue times
// need the session offset), which also keeps them on screen in the player's
// own full-window mode — the only full screen an iPhone offers a web page
// without dropping them. On AirPlay (Safari) they stay on the page: the
// player says so (see airplay.ts for how the receiver gets the stream).
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { getItem, getPlaybackPrefs, imageUrl, setSubtitleOffset } from '../api/client'
import { queueRemember } from './rememberQueue'
import type { MediaFile, PlaybackPrefs, PlayRequest, PlayResponse, Quality, SubTrack, TrackChoice, TrackPrefs } from '../api/types'
import { Icon } from '../components/Icon'
import { FamilyMarks, ReturnPill } from '../components/FamilyMarks'
import { useFamily } from '../components/FamilyProgress'
import { Scrubber } from './Scrubber'
import { useTrickplay } from './trickplay'
import { hasPlace, useReturnPoint, type FamilyPlace, type Hold } from './familyMarks'
import { activeCues, clock, langKey, parseVtt, type Cue } from './timeline'
import { asPrefs, choiceOf, offsetCueTime, pickAudio, pickSubtitle } from './trackPrefs'
import { clampSubtitleOffset, useSubtitleOffset } from './useSubtitleOffset'
import look from './SubtitleLook.module.css'
import { OnlineQualityMenu, QualityMenu, QUALITIES } from './QualityMenu'
import { TrackMenu } from './TrackMenu'
import { SubtitleSearchSheet, tracksNotRefreshed, type SubtitleLang } from './SubtitleSearchSheet'
import { VoiceOverSheet } from '../components/VoiceOverSheet'
import { useSession } from '../state/session'
import { useScreenGuard } from '../state/screenTime'
import { useCaps } from './useCaps'
import { usePlayback } from './usePlayback'
import { usePlayerKeys } from './usePlayerKeys'
import { SkipControls, SkipMenu, type AutoSkip } from './SkipControls'
import { segmentAt, usableSegments } from './skipSegments'
import { useProgress } from './useProgress'
import styles from './VideoPlayer.module.css'
import { audio } from './audio/engine'
import { airplaySupported, type WirelessVideo } from './airplay'
import { useT } from '../i18n'

export interface UpNext {
  /** «С1 Э4» */
  code: string
  title: string
  image?: string
  play: () => void
}

export interface VideoPlayerProps {
  /** Item the progress belongs to (movie or episode). */
  itemId: number
  title: string
  subtitle?: string
  artwork?: string
  parts: MediaFile[]
  /** Where to start, seconds on the whole timeline. */
  startSec: number
  next?: UpNext
  /** Leave the player (progress is already saved). */
  onClose: () => void
  /** Starts a session for a part (remote playback); default /api/play. */
  start?: (file: MediaFile, body: PlayRequest) => Promise<PlayResponse>
  /** Shown under the spinner while the video is loading (stream buffering). */
  waitingNote?: ReactNode
  /** The video started or stopped waiting for data. */
  onWaitingChange?: (waiting: boolean) => void
  /** A remote stream: a slow start or a stall is the source, not the device. */
  slowSource?: boolean
  /** Bytes the stream has so far (growing = data still comes). */
  sourceProgress?: () => number
  /** Extra controls at the right of the top bar (e.g. «Оставить на сервере»). */
  topActions?: ReactNode
  /**
   * A sheet over the player (the online «Другой источник» picker), drawn
   * inside the player's root so it shows in full screen too: `root` is
   * the Sheet's container, `position` the profile's own place now
   * (seconds; the one held during a family jump).
   */
  overlay?: (root: HTMLElement | null, position: () => number) => ReactNode
  /** A sheet is over the player (`overlay`): the controls and the cursor stay. */
  overlayOpen?: boolean
  /** «Смотреть онлайн»: a source through the server's proxy (see usePlayback). */
  online?: OnlinePlayback
  /** Keyboard shortcuts off (a sheet over the player has the keys). */
  suspendKeys?: boolean
  /**
   * The profile's track defaults, what it chose in this series and the
   * files' subtitle offsets (GET /api/items/{id}/track-prefs); without it
   * (a stream) the player asks for the profile's defaults itself.
   */
  tracks?: TrackPrefs
  /**
   * Sources build: the film or series «Найти озвучку…» looks for (the audio
   * menu's last row; adult profiles only).
   */
  voiceOverItem?: { id: number; title: string }
}

export interface OnlinePlayback {
  /** Closes the proxy session. */
  stop: (session: string, keepalive?: boolean) => Promise<void>
  /** The source quality asked for first («1080p»; '' = the server's default). */
  initialQuality?: string
  /** Saves the position (instead of the library's progress). */
  saveProgress: (positionMs: number, durationMs: number, keepalive: boolean) => Promise<void>
  /** «Другой источник»: the picker again. */
  onOtherSource: () => void
  /** The source quality changed (to remember it with the progress). */
  onQuality?: (q: string) => void
}

/** «Другое качество» after a failure: the next quality of the list (wrapping), '' when there is no other. */
export function otherQuality(options: string[], current: string): string {
  if (options.length < 2) return ''
  const i = options.indexOf(current)
  return options[(i + 1) % options.length]!
}

const HIDE_AFTER_MS = 3000
const UP_NEXT_SEC = 30
const SKIP = 10

const PREF = {
  volume: 'vault.player.volume',
  muted: 'vault.player.muted',
  sub: 'vault.player.subLang',
  audio: 'vault.player.audioLang',
  quality: 'vault.player.quality',
  // «Пропускать сам»: '1' skips the recap / the intro by itself
  autoRecap: 'vault.player.autoSkipRecap',
  autoIntro: 'vault.player.autoSkipIntro',
}

/** The quality chosen last time on this device («Авто» by default). */
function preferredQuality(): Quality {
  const q = pref(PREF.quality)
  return QUALITIES.some((x) => x.value === q) ? (q as Quality) : 'auto'
}

/** How long to wait before asking again for subtitles the server is still extracting. */
const SUB_RETRY_MS = 3000

function pref(k: string): string | null {
  try {
    return localStorage.getItem(k)
  } catch {
    return null
  }
}
function setPref(k: string, v: string) {
  try {
    localStorage.setItem(k, v)
  } catch {
    /* private mode */
  }
}

type WebkitVideo = HTMLVideoElement & {
  webkitSupportsPresentationMode?: (m: string) => boolean
  webkitSetPresentationMode?: (m: string) => void
}
type WebkitDocument = Document & {
  webkitFullscreenEnabled?: boolean
  webkitFullscreenElement?: Element | null
  webkitExitFullscreen?: () => void
}
type WebkitElement = HTMLElement & { webkitRequestFullscreen?: () => void }

const doc = () => document as WebkitDocument
const fullscreenElement = () => doc().fullscreenElement ?? doc().webkitFullscreenElement ?? null
/** Element full screen (desktop, iPad). An iPhone has none: the player already fills the window. */
const fullscreenSupported = () => typeof document !== 'undefined' && Boolean(doc().fullscreenEnabled || doc().webkitFullscreenEnabled)

/** Until the profile's defaults are known (or an older server has none): this device's choices, the file's defaults. */
const NO_PREFS: PlaybackPrefs = { audioLangs: [], subMode: 'off', subLangs: [], subSize: 'm', subBackground: 'translucent', custom: false }


/** What the viewer chose in this player (it outranks what was remembered). */
interface Chosen {
  audio?: TrackChoice
  /** 'off': subtitles turned off. */
  sub?: TrackChoice | 'off'
}

export function VideoPlayer({
  itemId,
  title,
  subtitle,
  artwork,
  parts,
  startSec,
  next,
  onClose,
  start,
  waitingNote,
  onWaitingChange,
  slowSource,
  sourceProgress,
  topActions,
  overlay,
  overlayOpen = false,
  online,
  suspendKeys = false,
  tracks,
  voiceOverItem,
}: VideoPlayerProps) {
  const t = useT()
  const caps = useCaps()
  const rootRef = useRef<HTMLDivElement>(null)
  const videoRef = useRef<HTMLVideoElement>(null)
  const pointerType = useRef('mouse')
  const closing = useRef(false)
  const [upNextDismissed, setUpNextDismissed] = useState(false)
  const finishRef = useRef<() => void>(() => {})
  // The profile's defaults: handed over with the item, else asked for (a stream).
  const [prefs, setPrefs] = useState<PlaybackPrefs>(tracks?.prefs ?? NO_PREFS)
  const prefsRef = useRef(prefs)
  prefsRef.current = prefs
  // The audio of the part that played last, carried to the next one by
  // title, then language (a part orders its tracks its own way).
  const lastAudio = useRef<TrackChoice | undefined>(undefined)
  /** The audio a part starts with: the voice that played, the series' one, this device's or the profile's language (trackPrefs). */
  const audioFor = (file: MediaFile) =>
    pickAudio(file.audio ?? [], { series: lastAudio.current ?? tracks?.series?.audio, prefs: prefsRef.current, device: pref(PREF.audio) })
  const [initialQuality] = useState(preferredQuality)

  const p = usePlayback({
    videoRef,
    parts,
    startSec,
    caps,
    audioFor,
    initialQuality,
    onFinished: () => finishRef.current(),
    start,
    slowSource,
    sourceProgress,
    online: online ? { stop: online.stop, initialQuality: online.initialQuality } : undefined,
  })
  // a jump to another profile's place is only looked at until it plays on (useProgress)
  const holdRef = useRef<Hold | null>(null)
  const save = useProgress({ itemId, absRef: p.absRef, durRef: p.durRef, playing: p.playing, saver: online?.saveProgress, holdRef })
  // «Экранное время» of a kid: a toast five minutes before the end, a
  // friendly stop at it (the server stops the stream there anyway)
  const kid = Boolean(useSession().user?.isKid)
  const screenGuard = useScreenGuard(kid && !online, t('player.screen.overFallback'))
  useEffect(() => {
    const v = videoRef.current
    if (!screenGuard.over || !v) return
    v.pause()
    // nothing plays on after the stop: not the media keys, not a tap
    const hold = () => v.pause()
    v.addEventListener('play', hold)
    return () => v.removeEventListener('play', hold)
  }, [screenGuard.over])
  // a refused play or stream: was it the time?
  const refreshScreen = screenGuard.refresh
  useEffect(() => {
    if (p.error) refreshScreen()
  }, [p.error, refreshScreen])
  const waitingNow = p.waiting && !p.needsTap && !p.error
  const waitingCb = useRef(onWaitingChange)
  waitingCb.current = onWaitingChange
  useEffect(() => waitingCb.current?.(waitingNow), [waitingNow])

  const close = useCallback(async () => {
    if (closing.current) return
    closing.current = true
    videoRef.current?.pause()
    await Promise.race([save({ final: true }), new Promise((r) => setTimeout(r, 1500))])
    p.stop()
    if (fullscreenElement()) await document.exitFullscreen?.().catch(() => {})
    onClose()
  }, [onClose, p, save])

  finishRef.current = () => {
    void save({ finished: true }).then(() => {
      if (next && !upNextDismissed) next.play()
      else void close()
    })
  }

  // ---------- controls visibility ----------
  const [visible, setVisible] = useState(true)
  const [menuOpen, setMenuOpen] = useState(false)
  const [findingSubs, setFindingSubs] = useState(false)
  const [findingVoices, setFindingVoices] = useState(false)
  const [scrubbing, setScrubbing] = useState(false)
  const hideTimer = useRef<number | undefined>(undefined)
  const poke = useCallback(() => {
    setVisible(true)
    window.clearTimeout(hideTimer.current)
    hideTimer.current = window.setTimeout(() => setVisible(false), HIDE_AFTER_MS)
  }, [])
  useEffect(() => () => window.clearTimeout(hideTimer.current), [])

  // ---------- «Семья»: the other profiles' places on the timeline ----------
  // (library items only: an online or remote stream has no family progress)
  const { user } = useSession()
  const family = useFamily(itemId, !online && !user?.isKid)
  const places = useMemo<FamilyPlace[]>(
    () =>
      family
        .filter((m) => hasPlace(m, itemId) && m.positionMs > 0)
        .map((m) => ({ member: m, at: m.positionMs / 1000, label: clock(m.positionMs / 1000) })),
    [family, itemId],
  )
  const ret = useReturnPoint<number>()
  const jumpToPlace = (pl: FamilyPlace) => {
    // a second jump keeps the way back to where this profile was before the first
    const mine = ret.back?.to ?? holdRef.current?.pos ?? p.absRef.current
    holdRef.current = { pos: mine, from: pl.at }
    ret.offer(mine, clock(mine))
    p.seek(pl.at)
    poke()
  }
  const returnBack = () => {
    if (!ret.back) return
    holdRef.current = null
    p.seek(ret.back.to)
    ret.clear()
    poke()
  }
  const seek = (sec: number) => {
    ret.clear()
    p.seek(sec)
  }
  const skipBy = (d: number) => {
    ret.clear()
    p.skip(d)
  }

  const shown =
    visible || !p.playing || menuOpen || scrubbing || Boolean(p.error) || p.needsTap || findingSubs || findingVoices || overlayOpen || Boolean(ret.back)

  // ---------- volume ----------
  const [volume, setVolume] = useState(() => {
    const v = Number(pref(PREF.volume))
    return Number.isFinite(v) && v > 0 && v <= 1 ? v : 1
  })
  const [muted, setMuted] = useState(() => pref(PREF.muted) === '1')
  useEffect(() => {
    const v = videoRef.current
    if (!v) return
    v.volume = volume
    v.muted = muted
  }, [volume, muted])
  const changeVolume = useCallback((val: number) => {
    const x = Math.min(Math.max(val, 0), 1)
    setVolume(x)
    setPref(PREF.volume, String(x))
    if (x > 0) {
      setMuted(false)
      setPref(PREF.muted, '0')
    }
  }, [])
  const toggleMute = useCallback(() => {
    setMuted((m) => {
      setPref(PREF.muted, m ? '0' : '1')
      return !m
    })
  }, [])

  // ---------- full screen, PiP, AirPlay ----------
  const [fullscreen, setFullscreen] = useState(false)
  const canFullscreen = fullscreenSupported()
  const toggleFullscreen = useCallback(() => {
    const root = rootRef.current as WebkitElement | null
    if (fullscreenElement()) {
      if (document.exitFullscreen) void document.exitFullscreen().catch(() => {})
      else doc().webkitExitFullscreen?.()
    } else if (root?.requestFullscreen) void root.requestFullscreen().catch(() => {})
    else root?.webkitRequestFullscreen?.()
  }, [])
  useEffect(() => {
    const on = () => setFullscreen(Boolean(fullscreenElement()))
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
    if (!v) return
    setPipSupported(Boolean(document.pictureInPictureEnabled || v.webkitSupportsPresentationMode?.('picture-in-picture')))
  }, [])
  // AirPlay (Safari): the button while a receiver is around; on one, the
  // session is reloaded with a media key (usePlayback.setRemote).
  const [airplayAvailable, setAirplayAvailable] = useState(false)
  const [wireless, setWireless] = useState(false)
  const { setRemote } = p
  useEffect(() => {
    const v = videoRef.current as WirelessVideo | null
    if (!v || !airplaySupported()) return
    const onAvailability = (e: Event) => setAirplayAvailable((e as Event & { availability?: string }).availability === 'available')
    const onTarget = () => {
      const on = Boolean(v.webkitCurrentPlaybackTargetIsWireless)
      setWireless(on)
      setRemote(on)
    }
    v.addEventListener('webkitplaybacktargetavailabilitychanged', onAvailability)
    v.addEventListener('webkitcurrentplaybacktargetiswirelesschanged', onTarget)
    return () => {
      v.removeEventListener('webkitplaybacktargetavailabilitychanged', onAvailability)
      v.removeEventListener('webkitcurrentplaybacktargetiswirelesschanged', onTarget)
    }
  }, [setRemote])
  const showAirplayPicker = () => (videoRef.current as WirelessVideo | null)?.webkitShowPlaybackTargetPicker?.()

  const togglePip = () => {
    const v = videoRef.current as WebkitVideo | null
    if (!v) return
    if (document.pictureInPictureElement) void document.exitPictureInPicture().catch(() => {})
    else if (v.requestPictureInPicture) void v.requestPictureInPicture().catch(() => {})
    else v.webkitSetPresentationMode?.('picture-in-picture')
  }

  // ---------- tracks ----------
  const [subIndex, setSubIndex] = useState(-1)
  // «Найти субтитры…» (admin, library files): a downloaded subtitle joins
  // the list of this part until the next session brings the server's list.
  const fileId = parts[p.part]?.id ?? 0
  const canFindSubs = Boolean(user?.isAdmin) && itemId > 0 && fileId > 0
  const canFindVoices = !__LIBRARY_EDITION__ && Boolean(voiceOverItem && user && !user.isKid && !user.libraryOnly && !online)
  const [subsOverride, setSubsOverride] = useState<{ fileId: number; subs: SubTrack[] } | null>(null)
  useEffect(() => setSubsOverride(null), [p.subs])
  const subs = subsOverride && subsOverride.fileId === fileId ? subsOverride.subs : p.subs
  // scrub previews of a library file (a film in parts: its timeline spans
  // several files, so none)
  const preview = useTrickplay(!online && itemId > 0 && parts.length === 1 ? fileId : undefined)
  const [cues, setCues] = useState<Cue[]>([])
  useEffect(() => {
    if (tracks) return
    const ac = new AbortController()
    getPlaybackPrefs(undefined, ac.signal).then((x) => setPrefs(asPrefs(x) ?? NO_PREFS), () => {})
    return () => ac.abort()
  }, [tracks])
  const chosen = useRef<Chosen>({})
  const remember = (choice: Parameters<typeof queueRemember>[1]) => {
    if (itemId > 0) queueRemember(itemId, choice)
  }
  // The subtitles of each part (and after an audio change): what was chosen
  // here, in the series, on this device, or the profile's defaults.
  const playingAudio = p.audio.find((a) => a.index === p.audioIndex)
  if (playingAudio) lastAudio.current = choiceOf(playingAudio)
  useEffect(() => {
    const c = chosen.current.sub
    const series = tracks?.series
    setSubIndex(
      pickSubtitle(p.subs, playingAudio, {
        series: c === 'off' ? undefined : (c ?? series?.subtitle),
        seriesOff: c === 'off' || (c === undefined && Boolean(series?.subtitlesOff)),
        prefs,
        device: pref(PREF.sub),
      }),
    )
    // playingAudio by its index: a new array of the same tracks changes nothing
  }, [p.subs, p.audioIndex, prefs, tracks])
  // Fetched once per URL (a session reload keeps the same tracks). An
  // embedded track takes the server minutes to extract: 503 = ask again.
  const subUrl = subs.find((x) => x.index === subIndex)?.url
  useEffect(() => {
    if (!subUrl) {
      setCues([])
      return
    }
    const ac = new AbortController()
    let timer: number | undefined
    const get = () =>
      fetch(subUrl, { credentials: 'same-origin', signal: ac.signal })
        .then((r) => {
          if (r.status === 503) {
            timer = window.setTimeout(get, SUB_RETRY_MS)
            return
          }
          return r.ok ? r.text().then((vtt) => setCues(parseVtt(vtt))) : Promise.reject(new Error(String(r.status)))
        })
        .catch(() => !ac.signal.aborted && setCues([]))
    void get()
    return () => {
      ac.abort()
      window.clearTimeout(timer)
    }
  }, [subUrl])
  // «Субтитры раньше / позже» of this part's file, kept for the profile (library files)
  const [offsetMs, moveOffset] = useSubtitleOffset(fileId, tracks?.offsets[String(fileId)] ?? 0, itemId > 0 && fileId > 0 ? setSubtitleOffset : undefined)
  const shownCues = useMemo(() => activeCues(cues, offsetCueTime(p.localTime, offsetMs)), [cues, p.localTime, offsetMs])

  const chooseAudio = (index: number) => {
    const tr = p.audio.find((a) => a.index === index)
    if (tr) {
      setPref(PREF.audio, langKey(tr.lang))
      chosen.current.audio = choiceOf(tr)
      remember({ audio: choiceOf(tr) })
    }
    p.chooseAudio(index)
  }
  const chooseSub = (index: number) => {
    const s = subs.find((x) => x.index === index)
    setPref(PREF.sub, s ? langKey(s.lang) : '')
    chosen.current.sub = s ? choiceOf(s) : 'off'
    remember(s ? { subtitle: choiceOf(s) } : { subtitleOff: true })
    setSubIndex(index)
  }
  /**
   * After a download: reload the file's tracks and turn the new one on; the
   * sheet closes only then (a failure keeps it open with the message).
   */
  const subtitleDownloaded = async (lang: SubtitleLang) => {
    const forFile = fileId
    let fresh: SubTrack[]
    try {
      const it = await getItem(itemId)
      fresh = it.files.find((f) => f.id === forFile)?.subs ?? []
    } catch {
      throw new Error(tracksNotRefreshed())
    }
    const known = new Set(subs.map((x) => `${x.url}|${x.lang}|${x.title}`))
    const added =
      fresh.find((x) => x.external && x.url && x.lang === lang && !known.has(`${x.url}|${x.lang}|${x.title}`)) ??
      [...fresh].reverse().find((x) => x.external && x.url && x.lang === lang)
    if (!added) throw new Error(tracksNotRefreshed())
    setSubsOverride({ fileId: forFile, subs: fresh })
    setPref(PREF.sub, langKey(added.lang))
    chosen.current.sub = choiceOf(added)
    remember({ subtitle: choiceOf(added) })
    setSubIndex(added.index)
    setFindingSubs(false)
  }

  // ---------- skip segments: recap, intro, credits ----------
  const segs = useMemo(() => usableSegments(p.segments), [p.segments])
  const lastPart = p.part === parts.length - 1
  // «Смотреть титры»: until the next part (a new episode remounts the player)
  const [creditsDismissed, setCreditsDismissed] = useState(false)
  useEffect(() => setCreditsDismissed(false), [p.part])
  const inCredits = segmentAt(segs, p.localTime)?.kind === 'credits'
  const nextPillShown = Boolean(next) && lastPart && inCredits && !creditsDismissed
  const skipAction = useRef<(() => void) | null>(null)
  const [autoSkip, setAutoSkip] = useState<AutoSkip>(() => ({ recap: pref(PREF.autoRecap) === '1', intro: pref(PREF.autoIntro) === '1' }))
  const changeAutoSkip = (v: AutoSkip) => {
    setPref(PREF.autoRecap, v.recap ? '1' : '0')
    setPref(PREF.autoIntro, v.intro ? '1' : '0')
    setAutoSkip(v)
  }
  const playNext = () => {
    if (!next) return
    videoRef.current?.pause()
    void save({ finished: true }).then(() => next.play())
  }

  usePlayerKeys({
    toggle: p.toggle,
    skip: skipBy,
    volume: (d) => changeVolume((muted ? 0 : volume) + d),
    fullscreen: () => canFullscreen && toggleFullscreen(),
    mute: toggleMute,
    close: () => void close(),
    poke,
    segment: () => {
      const a = skipAction.current
      if (!a) return false
      a()
      return true
    },
    // stopped by «Экранное время»: no key plays on
    suspended: findingSubs || findingVoices || suspendKeys || Boolean(screenGuard.over),
  })

  // System media controls (lock screen, keyboard media keys).
  const { skip } = p
  useEffect(() => {
    // Music hands the system controls over first, so its clean-up can never
    // run after (and wipe) the video's registration below.
    audio.yieldSession()
    if (!('mediaSession' in navigator)) return
    const art = imageUrl(artwork)
    try {
      navigator.mediaSession.metadata = new MediaMetadata({ title, artist: subtitle ?? '', artwork: art ? [{ src: art }] : [] })
      navigator.mediaSession.setActionHandler('seekbackward', () => skip(-SKIP))
      navigator.mediaSession.setActionHandler('seekforward', () => skip(SKIP))
    } catch {
      /* unsupported action */
    }
    return () => {
      try {
        navigator.mediaSession.metadata = null
        navigator.mediaSession.setActionHandler('seekbackward', null)
        navigator.mediaSession.setActionHandler('seekforward', null)
      } catch {
        /* ignore */
      }
    }
  }, [artwork, skip, subtitle, title])

  const remaining = p.total - p.time
  // the card waits while «Следующая серия» shows at the credits; after
  // «Смотреть титры» or in a scene after the credits it is back (the end
  // of the file still plays the next episode)
  const showUpNext = Boolean(next) && !nextPillShown && !upNextDismissed && p.total > 0 && remaining <= UP_NEXT_SEC && remaining > 0 && !p.error

  return (
    <div
      ref={rootRef}
      className={[styles.root, shown ? styles.shown : styles.hidden].join(' ')}
      data-testid="player"
      // menus open inside the player: it stands above the page (ui/floating.ts)
      data-portal-host
      data-mode={p.mode || undefined}
      data-part={p.part + 1}
      onPointerMove={(e) => e.pointerType === 'mouse' && poke()}
      onPointerDown={poke}
      role="region"
      aria-label={t('player.video.aria', { title })}
    >
      <video
        ref={videoRef}
        className={styles.video}
        playsInline
        x-webkit-airplay="allow"
        preload="auto"
        {...p.mediaProps}
        onPause={() => {
          p.mediaProps.onPause()
          if (!closing.current) void save()
        }}
      />

      {/* Click plays/pauses with a mouse; a tap on a touch screen shows or hides the controls. */}
      <div
        className={styles.surface}
        aria-hidden
        onPointerDown={(e) => {
          pointerType.current = e.pointerType
        }}
        onClick={() => {
          if (pointerType.current === 'touch') {
            if (shown && p.playing) setVisible(false)
            else poke()
          } else p.toggle()
        }}
        onDoubleClick={() => canFullscreen && toggleFullscreen()}
      />

      <div className={styles.topScrim} aria-hidden />
      <div className={styles.bottomScrim} aria-hidden />

      {shownCues.length > 0 && (
        <div className={styles.subtitles} aria-live="off" data-size={prefs.subSize} data-bg={prefs.subBackground}>
          {shownCues.map((c, i) => (
            <p key={`${c.start}-${i}`}>
              <span className={look.cue}>{c.text}</span>
            </p>
          ))}
        </div>
      )}

      <header className={styles.top}>
        <button type="button" className={`${styles.round} glass`} onClick={() => void close()} aria-label={t('player.video.close')} title={t('player.video.closeHint')}>
          <Icon name="close" size={22} strokeWidth={2.2} />
        </button>
        <div className={styles.heading}>
          <h1 className={styles.title}>{title}</h1>
          {subtitle && <p className={styles.subtitle}>{subtitle}</p>}
        </div>
        {topActions && <div className={styles.topActions}>{topActions}</div>}
      </header>

      {/* waiting for a tap to start: «Смотреть» below is the only control, not a second play button */}
      <div className={styles.center} hidden={p.needsTap && !p.error}>
        <button type="button" className={styles.skip} onClick={() => skipBy(-SKIP)} aria-label={t('player.video.back10')} title={t('player.video.back10Hint')}>
          <Icon name="back10" size={36} strokeWidth={1.9} />
        </button>
        <button
          type="button"
          className={styles.bigPlay}
          onClick={p.toggle}
          aria-label={p.playing ? t('common.pause') : t('common.watch')}
          title={p.playing ? t('player.video.pauseHint') : t('player.video.playHint')}
          disabled={Boolean(p.error)}
        >
          {p.waiting && !p.needsTap && !p.error ? (
            <span className={styles.spinner} role="status" aria-label={t('common.loading')} />
          ) : (
            <Icon name={p.playing ? 'pause' : 'play'} size={44} />
          )}
        </button>
        <button type="button" className={styles.skip} onClick={() => skipBy(SKIP)} aria-label={t('player.video.forward10')} title={t('player.video.forward10Hint')}>
          <Icon name="forward10" size={36} strokeWidth={1.9} />
        </button>
      </div>

      {wireless && (p.keyFailed || subIndex >= 0) && (
        <div className={`${styles.airplayNote} glass`} role="status">
          {p.keyFailed ? t('player.video.airplayNoAccess') : t('player.video.airplayNoSubtitles')}
        </div>
      )}

      {(p.refreshing || waitingNote) && p.waiting && !p.needsTap && !p.error && (
        <div className={`${styles.waitNote} glass`}>{p.refreshing ? t('player.video.refreshing') : waitingNote}</div>
      )}

      <footer className={styles.bottom}>
        <div className={styles.scrubBox}>
          <Scrubber
            time={p.time}
            duration={p.total}
            buffered={p.buffered}
            onSeek={seek}
            onScrubbing={setScrubbing}
            marks={places.length > 0 && <FamilyMarks places={places} total={p.total} onJump={jumpToPlace} />}
            preview={preview}
          />
          {ret.back && <ReturnPill className={styles.returnPill} label={ret.back.label} onReturn={returnBack} onDismiss={ret.clear} />}
        </div>
        <div className={styles.tools}>
          <div className={styles.toolsLeft}>
            {parts.length > 1 && (
              <span className={styles.partLabel}>
                {t('player.video.part', { n: p.part + 1, total: parts.length })}
              </span>
            )}
          </div>
          <div className={styles.toolsRight}>
            {online ? (
              p.onlineQuality && (
                <OnlineQualityMenu
                  info={p.onlineQuality}
                  onChange={(q) => {
                    online.onQuality?.(q)
                    p.chooseOnlineQuality(q)
                  }}
                  onOpenChange={setMenuOpen}
                />
              )
            ) : (
              <QualityMenu
                quality={p.quality}
                info={p.qualityInfo}
                onChange={(q) => {
                  setPref(PREF.quality, q)
                  p.chooseQuality(q)
                }}
                onOpenChange={setMenuOpen}
              />
            )}
            <SkipMenu
              kinds={{ recap: segs.some((s) => s.kind === 'recap'), intro: segs.some((s) => s.kind === 'intro') }}
              value={autoSkip}
              onChange={changeAutoSkip}
              onOpenChange={setMenuOpen}
            />
            <TrackMenu
              audio={p.audio}
              audioIndex={p.audioIndex}
              subtitles={subs}
              subIndex={subIndex}
              onAudio={chooseAudio}
              onSubtitle={chooseSub}
              onOpenChange={setMenuOpen}
              onFindSubtitles={canFindSubs ? () => setFindingSubs(true) : undefined}
              onFindVoiceOvers={canFindVoices ? () => setFindingVoices(true) : undefined}
              subOffsetMs={offsetMs}
              onSubOffset={(ms) => moveOffset(clampSubtitleOffset(ms))}
            />
            <div className={styles.volume}>
              <button
                type="button"
                className={styles.tool}
                onClick={toggleMute}
                aria-label={muted || volume === 0 ? t('player.video.unmute') : t('player.video.mute')}
                title={t('player.video.soundHint')}
              >
                <Icon name={muted || volume === 0 ? 'speakerMute' : 'speaker'} size={24} />
              </button>
              <input
                type="range"
                min={0}
                max={1}
                step={0.05}
                value={muted ? 0 : volume}
                onChange={(e) => changeVolume(Number(e.target.value))}
                aria-label={t('player.volume')}
                className={styles.volumeRange}
                style={{ ['--v' as string]: `${(muted ? 0 : volume) * 100}%` }}
              />
            </div>
            {airplayAvailable && (
              <button
                type="button"
                className={styles.tool}
                onClick={showAirplayPicker}
                aria-label="AirPlay"
                aria-pressed={wireless}
                title="AirPlay"
                data-active={wireless || undefined}
              >
                <Icon name="airplay" size={24} />
              </button>
            )}
            {pipSupported && (
              <button type="button" className={styles.tool} onClick={togglePip} aria-label={t('player.video.pip')} title={t('player.video.pip')}>
                <Icon name="pip" size={24} />
              </button>
            )}
            {canFullscreen && (
              <button
                type="button"
                className={styles.tool}
                onClick={toggleFullscreen}
                aria-label={fullscreen ? t('player.video.exitFullScreen') : t('player.video.fullScreen')}
                title={t('player.video.fullScreenHint')}
              >
                <Icon name={fullscreen ? 'collapse' : 'expand'} size={22} strokeWidth={2.1} />
              </button>
            )}
          </div>
        </div>
      </footer>

      {!p.error && segs.length > 0 && (
        <SkipControls
          segments={segs}
          part={p.part}
          time={p.localTime}
          durationSec={p.durRef.current[p.part] ?? 0}
          playing={p.playing}
          controlsShown={shown}
          hasNext={Boolean(next) && lastPart}
          creditsDismissed={creditsDismissed}
          onDismissCredits={() => setCreditsDismissed(true)}
          autoSkip={autoSkip}
          onSeek={(sec) => {
            ret.clear()
            p.seekPart(sec)
          }}
          onNext={playNext}
          onEnd={() => (lastPart ? finishRef.current() : p.seekPart(p.durRef.current[p.part] ?? 0))}
          actionRef={skipAction}
        />
      )}

      {showUpNext && next && (
        <aside className={`${styles.upNext} glass-strong`} aria-label={t('player.video.upNext')}>
          {next.image && <img src={imageUrl(next.image, 480)} alt="" className={styles.upNextArt} />}
          <div className={styles.upNextText}>
            <span className={styles.upNextEyebrow}>{t('player.video.upNextCode', { code: next.code })}</span>
            <span className={styles.upNextTitle}>{next.title}</span>
            <div className={styles.upNextActions}>
              <button type="button" className={styles.upNextPlay} onClick={next.play}>
                <Icon name="play" size={16} /> {t('common.watch')}
              </button>
              <button type="button" className={styles.upNextDismiss} onClick={() => setUpNextDismissed(true)}>
                {t('player.video.hide')}
              </button>
            </div>
          </div>
        </aside>
      )}

      {p.needsTap && !p.error && (
        <button type="button" className={styles.tapToPlay} onClick={p.toggle}>
          <Icon name="play" size={30} />
          <span>{t('common.watch')}</span>
        </button>
      )}

      {screenGuard.warnMinutes !== null && !screenGuard.over && (
        <div className={`${styles.screenToast} glass-strong`} role="status">
          <Icon name="clock" size={18} />
          <span>{t('player.screen.warn', { count: screenGuard.warnMinutes })}</span>
        </div>
      )}

      {screenGuard.over && (
        <div className={styles.errorWrap}>
          <div className={`${styles.error} ${styles.screenOver} glass-strong`} role="alert">
            <Icon name="moon" size={36} strokeWidth={1.6} />
            <h2>{t('player.screen.overTitle')}</h2>
            <p>{screenGuard.over}</p>
            <div className={styles.errorActions}>
              <button type="button" className={styles.errorPrimary} onClick={() => void close()}>
                {t('common.close')}
              </button>
            </div>
          </div>
        </div>
      )}

      {p.error && !screenGuard.over && (
        <div className={styles.errorWrap}>
          <div className={`${styles.error} glass-strong`} role="alert">
            <Icon name="warning" size={36} strokeWidth={1.6} />
            <h2>{t('player.video.playFailed')}</h2>
            <p>{p.error}</p>
            {online ? (
              <div className={styles.errorActions}>
                <button type="button" className={styles.errorPrimary} onClick={online.onOtherSource}>
                  {t('player.video.otherSource')}
                </button>
                {p.onlineQuality && otherQuality(p.onlineQuality.options, p.onlineQuality.current) && (
                  <button
                    type="button"
                    className={styles.errorSecondary}
                    onClick={() => {
                      const q = otherQuality(p.onlineQuality!.options, p.onlineQuality!.current)
                      online.onQuality?.(q)
                      p.chooseOnlineQuality(q)
                    }}
                  >
                    {t('player.video.otherQuality')}
                  </button>
                )}
              </div>
            ) : (
              <div className={styles.errorActions}>
                <button type="button" className={styles.errorPrimary} onClick={p.retry}>
                  {t('common.retry')}
                </button>
                <button type="button" className={styles.errorSecondary} onClick={() => void close()}>
                  {t('common.close')}
                </button>
              </div>
            )}
          </div>
        </div>
      )}

      {findingSubs && canFindSubs && (
        <SubtitleSearchSheet
          fileId={fileId}
          container={rootRef.current}
          onClose={() => setFindingSubs(false)}
          onDownloaded={subtitleDownloaded}
        />
      )}
      {!__LIBRARY_EDITION__ && findingVoices && canFindVoices && voiceOverItem && (
        <VoiceOverSheet
          itemId={voiceOverItem.id}
          title={voiceOverItem.title}
          container={rootRef.current}
          onClose={() => setFindingVoices(false)}
        />
      )}
      {overlay?.(rootRef.current, () => holdRef.current?.pos ?? p.absRef.current)}
    </div>
  )
}
