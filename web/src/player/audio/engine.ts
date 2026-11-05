// The audio engine: one module-level player for music and audiobooks.
//
// Its <audio> elements are created here and appended to document.body,
// outside the React tree, so sound survives navigation and even leaving the
// Shell (the video player pauses it instead). Two elements give near-gapless
// changes: 15 s before the end the idle one loads the next track and takes
// over on `ended`. iOS/iPadOS Safari refuses play() on a second element
// without a user gesture (worst with the screen locked), so there a single
// element changes its src on `ended`. Safari on a Mac routed to AirPlay
// does the same: WebKit routes only the element that opened the picker.
//
// Transcoded streams (remote quality, codecs the browser lacks) have no
// duration and nothing seekable: a seek outside `seekable` reloads the track
// with `t=<sec>` and counts time from that offset.
//
// Audiobooks (kind 'audiobook'): one queue item per file of the book. The
// position from the start of the book is saved every 10 s while playing, on
// pause, when the queue is replaced and on pagehide (keepalive). The speed
// applies to audiobooks only (pitch kept); music always plays at 1×. A
// position is queued (offline/store) until the server has it: one reached
// without the server goes out when it is back.
//
// «Выравнивание громкости» (normalize.ts): each element plays its track at
// the track's (or album's) ReplayGain, set when the track is loaded, so a
// change of track never jumps in level mid-sound. Attenuation is the
// element's volume. A boost goes through a Web Audio GainNode (one
// MediaElementSource per element, made once) only in desktop browsers
// without AirPlay: WebKit routes an element, not an audio graph, to an
// AirPlay receiver, and a phone's suspended AudioContext would silence the
// lock screen. There the gain stops at 1 (never louder than the file).
//
// The engine belongs to the profile that started it. Before a sign-out, an
// expired session or another profile signing in is published, it saves the
// book for that profile (under its cookie while it still works, else queued
// for its next sign-in), then stops both elements and empties the queue and
// the system media controls: nothing plays on, shows or is saved under the
// next profile on a shared device.
import { getMyScreenTime, probeStream, recordPlay, saveProgress } from '../../api/client'
import { currentOwner, isSavedMedia, rememberProgress, settleProgress, settlesProgress } from '../../offline/store'
import { session as authSession, type SessionState } from '../../state/session'
import * as session from './mediaSession'
import { t } from '../../i18n'
import { nextIndex, previousIndex, shuffled, uniqueKeys, withStart, type QueueItem, type RepeatMode } from './queue'
import { heldPosition, type Hold } from '../familyMarks'
import { itemGain, loadNormalize, saveNormalize, type NormalizeMode } from './normalize'

export type { QueueItem, RepeatMode } from './queue'

export interface AudioState {
  queue: QueueItem[]
  index: number
  playing: boolean
  positionSec: number
  durationSec: number
  buffering: boolean
  shuffle: boolean
  repeat: RepeatMode
  rate: number
  volume: number
  kind: 'music' | 'audiobook' | null
  bookId?: number
  error?: string
  /** A kid's «Экранное время» stopped the listening: what the kid reads (AudioScreenStop). */
  screenStop?: string
}

export interface PlayOptions {
  kind?: 'music' | 'audiobook'
  bookId?: number
  startSec?: number
  shuffle?: boolean
  /** Audiobooks: the speed to play at (music is always 1×). */
  rate?: number
}

/** Audiobook position saves while playing, every this many seconds. */
export const BOOK_SAVE_SEC = 10
/** ±skips of an audiobook (system media controls, mini player). */
export const BOOK_BACK_SEC = 15
export const BOOK_FORWARD_SEC = 30

/** Seconds before the end at which the next track starts loading. */
export const PRELOAD_SEC = 15
/** «Предыдущий трек» after this many seconds restarts the current one. */
export const RESTART_SEC = 3
/** A listen counts after this many seconds (or half the track). */
export const PLAY_COUNT_SEC = 30
const MAX_ERRORS = 3
/**
 * A stream that fails for a passing reason (the network, a busy or failing
 * server, an idle transcode the server cut) is fetched again from where it
 * was, this many times, before the track counts as unplayable.
 */
export const MAX_RETRIES = 3
const RETRY_MS = [1000, 2000, 4000]
const RETRY_AFTER_MAX_SEC = 10
/** MediaError codes (jsdom has no MediaError). */
const MEDIA_ERR_NETWORK = 2
const MEDIA_ERR_DECODE = 3

export const skipNotice = () => t('player.audio.skipNotice')
export const stopError = () => t('player.audio.stopError')

/** 'single' on iPhone and iPad (iPadOS reports itself as a Mac with touch). */
export function engineMode(): 'dual' | 'single' {
  return navigator.maxTouchPoints > 0 && /Mac|iPhone|iPad/.test(navigator.userAgent) ? 'single' : 'dual'
}

type WirelessElement = HTMLAudioElement & { webkitCurrentPlaybackTargetIsWireless?: boolean }

interface Slot {
  el: HTMLAudioElement
  /** Queue key of the loaded item, null when empty. */
  key: string | null
  /** Seconds of the track before this stream's zero (transcode `t`). */
  offset: number
  transcoded: boolean
  /** Start position to apply once metadata is known. */
  pendingSeek: number | null
  /** A src change is in progress: its `pause` is not the user's. */
  loading: boolean
  /** «Выравнивание громкости»: the linear gain of the loaded track. */
  gain: number
  /** The element's Web Audio gain, once it needed a boost (normalize.ts). */
  node: GainNode | null
}

const initial = (): AudioState => ({
  queue: [],
  index: -1,
  playing: false,
  positionSec: 0,
  durationSec: 0,
  buffering: false,
  shuffle: false,
  repeat: 'off',
  rate: 1,
  volume: 1,
  kind: null,
})

let state: AudioState = initial()
const listeners = new Set<() => void>()
/** The queue in its original order while shuffle is on. */
let original: QueueItem[] | null = null
let slots: Slot[] = []
let cur = 0
let wantPlay = false
let counted = false
/** Seconds of the current track actually heard (seeks excluded). */
let listened = 0
/** Position at the last timeupdate, to measure what was heard. */
let lastPos = 0
let errors = 0
/** Retries of the current track since it last played. */
let retries = 0
let retryTimer: ReturnType<typeof setTimeout> | null = null
/** Bumped by anything that makes a pending retry or error verdict stale. */
let errorSeq = 0
let ticker: ReturnType<typeof setInterval> | null = null
let ticks = 0
let handlersSet = false
/** Sleep-timer fade (0..1) on top of the volume. */
let fade = 1
/** The last audiobook position sent, to skip repeats. */
let lastBookSave = ''
/** «Выравнивание громкости» on this device. */
let normalizeMode: NormalizeMode = loadNormalize()
/** The audio graph of the boosts (desktop browsers without AirPlay); made on a play gesture. */
let audioCtx: AudioContext | null = null
/** A jump to another profile's place in the book (ms): saved as the position before it until it plays on. */
let bookHold: Hold | null = null
/** The current book was really heard since it started: only then is its position saved (opening is not listening). */
let bookHeard = false
let pageHooks = false
/** The profile whose listening this is (set by play); null before the first. */
let owner: number | null = null
/** The last audiobook save under way, for a sign-out to wait on. */
let lastSave: Promise<unknown> | null = null

function set(patch: Partial<AudioState>) {
  state = { ...state, ...patch }
  for (const fn of listeners) fn()
}

// --- notices (toasts under the mini player) ---

let notice: string | null = null
let noticeTimer: ReturnType<typeof setTimeout> | null = null
const noticeSubs = new Set<() => void>()

function showNotice(text: string | null) {
  notice = text
  if (noticeTimer) clearTimeout(noticeTimer)
  noticeTimer = text ? setTimeout(() => showNotice(null), 4000) : null
  for (const fn of noticeSubs) fn()
}

export const audioNotice = {
  get: () => notice,
  subscribe(fn: () => void) {
    noticeSubs.add(fn)
    return () => {
      noticeSubs.delete(fn)
    }
  },
}

// --- elements ---

const active = () => slots[cur] as Slot | undefined
/**
 * Safari: the active element plays on an AirPlay receiver. WebKit routes
 * only the element that opened the picker, so the dual engine then stays on
 * that one element (gapless is given up while routed).
 */
const wireless = () => Boolean((active()?.el as WirelessElement | undefined)?.webkitCurrentPlaybackTargetIsWireless)
const idle = () => (slots.length > 1 && !wireless() ? (slots[1 - cur] ?? null) : null)
const item = () => state.queue[state.index] as QueueItem | undefined
/** A book never repeats (the music setting stays as it is). */
const repeat = (): RepeatMode => (state.kind === 'audiobook' ? 'off' : state.repeat)

/** Milliseconds from the start of the book (the files before plus the position) and the book's length. */
function bookPosition(): { ms: number; total: number } {
  let before = 0
  let total = 0
  state.queue.forEach((q, i) => {
    if (i < state.index) before += q.durationSec
    total += q.durationSec
  })
  return { ms: Math.round((before + state.positionSec) * 1000), total: Math.round(total * 1000) }
}

/** The listening is still the signed-in profile's (never save or count it for another). */
const ours = () => currentOwner() === owner

/**
 * Sends the audiobook position (not twice the same) and returns the request.
 * `send` false only queues it for the profile, to go out at its next sign-in.
 */
function saveBook(keepalive = false, send = true): Promise<unknown> | null {
  if (state.kind !== 'audiobook' || !state.bookId || state.index < 0 || !bookHeard || !ours()) return null
  const { ms: now, total } = bookPosition()
  if (total <= 0) return null
  const held = heldPosition(bookHold, now, 1000)
  if (held.release) bookHold = null
  const ms = held.pos
  const pos = Math.min(Math.max(ms, 0), total)
  const key = `${state.bookId}|${Math.round(pos / 1000)}`
  if (key === lastBookSave) return lastSave
  lastBookSave = key
  const id = state.bookId
  // queued until the server has it: without the server it goes out later
  const at = rememberProgress({ itemId: id, positionMs: pos, durationMs: total })
  if (!send) return null
  lastSave = saveProgress(id, pos, total, keepalive).then(
    () => settleProgress(id, at),
    (e: unknown) => {
      if (settlesProgress(e)) settleProgress(id, at) // the book is gone; anything else stays queued
      if (lastBookSave === key) lastBookSave = ''
      console.warn('vault: audiobook position of', id, 'not saved', e)
    },
  )
  return lastSave
}

function inSeekable(el: HTMLMediaElement, t: number): boolean {
  const r = el.seekable
  if (!r) return false
  for (let i = 0; i < r.length; i++) if (t >= r.start(i) && t <= r.end(i)) return true
  return false
}

function detectTranscode(s: Slot) {
  const d = s.el.duration
  // a copy saved on the device is a whole file (served with Range): seek inside it
  s.transcoded = !isSavedMedia(s.el.currentSrc || s.el.src) && (d === Infinity || !s.el.seekable || s.el.seekable.length === 0)
}

function durationOf(s: Slot): number {
  const d = s.el.duration
  if (!s.transcoded && Number.isFinite(d) && d > 0) return d
  return item()?.durationSec ?? 0
}

const positionOf = (s: Slot) => s.offset + (Number.isFinite(s.el.currentTime) ? s.el.currentTime : 0)

/**
 * «Выравнивание громкости» works here: not on iPhone or iPad, whose Safari
 * ignores an element's volume (and gets no audio graph: see above).
 */
export function normalizeSupported(): boolean {
  return engineMode() === 'dual'
}

/**
 * Web Audio may carry an element's sound: a desktop browser (no touch: a
 * phone suspends the context with the screen off) without WebKit's AirPlay
 * (it routes elements, not graphs), in the dual engine.
 */
function webAudioAllowed(): boolean {
  if (engineMode() !== 'dual' || navigator.maxTouchPoints > 0) return false
  if ('WebKitPlaybackTargetAvailabilityEvent' in window) return false
  return typeof window.AudioContext === 'function'
}

/** Makes (or wakes) the context on a play gesture, where a browser allows it to run. */
function prepareContext() {
  if (!webAudioAllowed()) return
  try {
    if (!audioCtx) {
      const ctx = new AudioContext()
      audioCtx = ctx
      ctx.addEventListener('statechange', () => audioCtx === ctx && contextChanged(ctx))
    }
    if (audioCtx.state === 'suspended') void audioCtx.resume().catch(() => {})
  } catch {
    audioCtx = null
  }
}

/**
 * The context runs: the boosts it waited for are attached. It stopped
 * (suspended, interrupted): an element routed through it would be silent,
 * so each one is moved to a fresh element of its own (element volume only)
 * where it was; a later run attaches the boost to that again.
 */
function contextChanged(ctx: AudioContext) {
  if (ctx.state === 'running') {
    for (const s of slots) applyLevel(s)
    return
  }
  for (const s of slots) if (s.node) rerouteToElement(s)
}

/** Moves a slot off the audio graph: a new element loads its item where it was. */
function rerouteToElement(s: Slot) {
  const old = s.el
  const it = s.key ? state.queue.find((q) => q.key === s.key) : undefined
  const pos = positionOf(s)
  const sounding = wantPlay && slots[cur] === s
  s.el = makeElement(s, old.dataset.vaultAudio ?? 'a')
  s.node = null
  old.parentElement?.insertBefore(s.el, old)
  try {
    old.pause()
    old.removeAttribute('src')
    old.load()
  } catch {
    /* ignore */
  }
  old.remove()
  markActive()
  if (!it) {
    s.key = null
    return
  }
  load(s, it, slots[cur] === s ? pos : 0)
  if (sounding) playEl(s)
}

/** Routes an element through its own GainNode (once; never undone). */
function attachGain(s: Slot) {
  if (s.node || !audioCtx || audioCtx.state !== 'running' || wireless()) return
  try {
    const src = audioCtx.createMediaElementSource(s.el)
    const node = audioCtx.createGain()
    node.gain.value = 1
    src.connect(node).connect(audioCtx.destination)
    s.node = node
  } catch (e) {
    console.warn('vault: no Web Audio gain', e)
  }
}

/**
 * Applies volume × fade × the track's gain: the GainNode carries the gain
 * when the element has one (a short ramp when `smooth`, for a change while
 * it sounds), else the element's volume (at most 1).
 */
function applyLevel(s: Slot, smooth = false) {
  const vol = state.volume * fade
  if (s.gain > 1 && !s.node) attachGain(s)
  const ctx = audioCtx
  if (s.node && ctx) {
    const g = s.node.gain
    const now = ctx.currentTime
    g.cancelScheduledValues(now)
    if (smooth) {
      g.setValueAtTime(g.value, now)
      g.setTargetAtTime(s.gain, now, 0.05)
    } else g.setValueAtTime(s.gain, now)
    setElVolume(s, vol, false)
  } else setElVolume(s, vol * Math.min(s.gain, 1), smooth)
}

/** Steps of an element-volume ramp (RAMP_STEPS × RAMP_MS ≈ 150 ms). */
const RAMP_STEPS = 10
const RAMP_MS = 15
const ramps = new Map<Slot, ReturnType<typeof setInterval>>()

/** Sets an element's volume, ramped (no click) when `smooth`. */
function setElVolume(s: Slot, target: number, smooth: boolean) {
  const t = ramps.get(s)
  if (t) clearInterval(t)
  ramps.delete(s)
  const el = s.el
  const from = el.volume
  if (!smooth || Math.abs(from - target) < 0.005) {
    el.volume = target
    return
  }
  let step = 0
  ramps.set(
    s,
    setInterval(() => {
      step++
      if (s.el !== el || step >= RAMP_STEPS) {
        if (s.el === el) el.volume = target
        clearInterval(ramps.get(s))
        ramps.delete(s)
        return
      }
      el.volume = from + ((target - from) * step) / RAMP_STEPS
    }, RAMP_MS),
  )
}

/** The queue or shuffle changed: the preloaded track's gain is worked out again (it does not sound yet). */
function relevelIdle() {
  if (state.kind !== 'music') return
  for (const s of slots) {
    if (s === slots[cur] || !s.key) continue
    const it = state.queue.find((q) => q.key === s.key)
    s.gain = it ? gainOf(it) : 1
    applyLevel(s)
  }
}

/** The gain of a queue item under the setting (1 for a book or an unknown track). */
function gainOf(it: QueueItem): number {
  if (!normalizeSupported()) return 1
  const i = state.queue.findIndex((q) => q.key === it.key)
  return itemGain(normalizeMode, i >= 0 ? state.queue : [it], Math.max(i, 0), state.shuffle)
}

/**
 * Creates a slot's <audio> element and its listeners. Each listener first
 * checks the element is still the slot's: an element replaced by
 * rerouteToElement fires nothing into the engine.
 */
function makeElement(slot: Slot, name: string): HTMLAudioElement {
  const el = document.createElement('audio')
  el.hidden = true
  el.preload = 'auto'
  el.dataset.vaultAudio = name
  // audiobooks at 1.5× sound like the reader, only faster
  el.preservesPitch = true
  ;(el as HTMLAudioElement & { webkitPreservesPitch?: boolean }).webkitPreservesPitch = true
  const mine = () => slot.el === el
  const isActive = () => mine() && slots[cur] === slot
  el.addEventListener('loadedmetadata', () => {
    if (!mine()) return
    // a new src resets the rate to defaultPlaybackRate: keep the book's speed
    if (el.playbackRate !== state.rate) el.playbackRate = state.rate
    detectTranscode(slot)
    if (isActive()) onMetadata(slot)
  })
  el.addEventListener('durationchange', () => {
    if (!mine()) return
    if (slot.el.readyState >= 1) detectTranscode(slot)
    if (isActive()) set({ durationSec: durationOf(slot) })
  })
  el.addEventListener('timeupdate', () => isActive() && onTime(slot))
  el.addEventListener('ended', () => isActive() && onEnded(slot))
  el.addEventListener('error', () => {
    if (!mine()) return
    if (isActive()) onError()
    else slot.key = null // a failed preload: load it again when its turn comes
  })
  el.addEventListener('playing', () => {
    if (!mine()) return
    slot.loading = false
    // a boost's context fell asleep meanwhile: wake it (or fall back)
    if (slot.node && audioCtx && audioCtx.state !== 'running') void audioCtx.resume().catch(() => {})
    if (!isActive()) return
    errors = 0
    retries = 0
    set({ buffering: false, playing: true, error: undefined })
  })
  el.addEventListener('waiting', () => isActive() && set({ buffering: true }))
  el.addEventListener('canplay', () => isActive() && state.buffering && set({ buffering: false }))
  el.addEventListener('pause', () => {
    // Headphones unplugged, another app took the audio, … — not our own
    // pauses, src changes or the end of a track.
    if (!isActive() || slot.loading || el.ended || !wantPlay) return
    wantPlay = false
    stopTicker()
    session.setPlaying(false)
    set({ playing: false })
    saveBook()
  })
  el.addEventListener('webkitcurrentplaybacktargetiswirelesschanged', () => {
    // routed away: a track preloaded in the other element would play on the Mac
    if (!isActive() || !wireless()) return
    const o = slots.find((s) => s !== slot)
    if (o?.key) release(o)
  })
  return el
}

function ensure() {
  if (slots.length > 0) return
  const n = engineMode() === 'dual' ? 2 : 1
  for (let i = 0; i < n; i++) {
    const slot: Slot = { el: null as unknown as HTMLAudioElement, key: null, offset: 0, transcoded: false, pendingSeek: null, loading: false, gain: 1, node: null }
    slot.el = makeElement(slot, i === 0 ? 'a' : 'b')
    slots.push(slot)
    document.body.appendChild(slot.el)
  }
  cur = 0
  markActive()
  if (!pageHooks) {
    pageHooks = true
    // the tab closes or goes to the background: the request must outlive it
    window.addEventListener('pagehide', () => saveBook(true))
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'hidden') saveBook(true)
    })
  }
}

function markActive() {
  slots.forEach((s, i) => {
    if (i === cur) s.el.setAttribute('data-active', '')
    else s.el.removeAttribute('data-active')
  })
}

function load(s: Slot, it: QueueItem, startSec: number) {
  s.key = it.key
  s.offset = 0
  s.transcoded = false
  s.pendingSeek = startSec > 0 ? startSec : null
  s.loading = true
  // the media load algorithm resets playbackRate to defaultPlaybackRate
  s.el.defaultPlaybackRate = state.rate
  s.el.playbackRate = state.rate
  // the track's level before a sound of it plays
  s.gain = state.kind === 'music' ? gainOf(it) : 1
  applyLevel(s)
  s.el.src = it.src
}

function release(s: Slot) {
  s.key = null
  s.offset = 0
  s.pendingSeek = null
  s.loading = false
  s.el.removeAttribute('src')
  try {
    s.el.load()
  } catch {
    /* ignore */
  }
}

/** Reloads a transcode starting `t` seconds into the track. */
function reloadAt(s: Slot, it: QueueItem, t: number) {
  s.offset = t
  s.pendingSeek = null
  s.loading = true
  s.el.src = withStart(it.src, t)
  if (wantPlay) playEl(s)
}

function playEl(s: Slot) {
  const p = s.el.play()
  if (p && typeof p.catch === 'function') {
    p.catch((e: unknown) => {
      const name = e instanceof DOMException || e instanceof Error ? e.name : ''
      if (name === 'AbortError') return // a newer src or pause() won
      if (slots[cur] !== s || !wantPlay) return
      if (name === 'NotAllowedError') {
        // autoplay refused (no gesture yet): wait for «Играть»
        finish()
        return
      }
      // The source is unusable. A media error also fires `error`, which
      // handles it; anything else is handled here, never left «playing».
      if (!s.el.error) onError()
    })
  }
}

/** The chapter of an audiobook item at `sec` into it (absolute chapter times). */
function bookChapter(it: QueueItem, sec: number) {
  if (it.bookStartMs === undefined || !it.chapters?.length) return undefined
  const ms = it.bookStartMs + sec * 1000
  let ch = it.chapters[0]
  for (const c of it.chapters) if (c.startMs <= ms + 1) ch = c
  return ch
}

/** The chapter title on the lock screen now ('' for music). */
let shownChapter: string | undefined

/** Lock-screen metadata: a book shows its chapter as the artist line and the author as the album. */
function setSessionMetadata(it: QueueItem) {
  const ch = state.kind === 'audiobook' ? bookChapter(it, state.positionSec) : undefined
  shownChapter = ch?.title
  session.setMetadata(it, ch ? { artist: ch.title, album: it.artist } : undefined)
}

/** Takes the system media controls: metadata and every action handler. */
function claimSession() {
  const it = item()
  if (!it) return
  setSessionMetadata(it)
  const book = state.kind === 'audiobook'
  session.setHandlers(
    {
      play: () => !state.playing && resume(),
      pause: () => audio.pause(),
      previous: () => audio.previous(),
      next: () => audio.next(),
      seek: (t) => audio.seek(t),
      skip: (d) => audio.skip(d),
    },
    // an audiobook gets −15 / +30 s on the lock screen instead of tracks
    book ? { back: BOOK_BACK_SEC, forward: BOOK_FORWARD_SEC, tracks: false } : undefined,
  )
  handlersSet = true
}

function startTicker() {
  if (ticker) return
  ticks = 0
  ticker = setInterval(() => {
    const s = active()
    if (s) session.setPosition(positionOf(s), durationOf(s), state.rate)
    // a book's lock screen follows the chapter (also inside one M4B)
    const it = item()
    if (it && handlersSet && state.kind === 'audiobook' && bookChapter(it, state.positionSec)?.title !== shownChapter) setSessionMetadata(it)
    if (++ticks % BOOK_SAVE_SEC === 0) saveBook()
  }, 1000)
}

function stopTicker() {
  if (ticker) clearInterval(ticker)
  ticker = null
}

function resume() {
  const s = active()
  const it = item()
  if (!s || !it) return
  if (state.error || state.screenStop || s.el.error) {
    // after an error (or a screen-time stop: the server says again) the element is stuck on its failed source: fetch again
    // where it was (a transcode starts right there)
    errors = 0
    retries = 0
    if (s.transcoded && s.key === it.key) reloadAt(s, it, state.positionSec)
    else load(s, it, state.positionSec)
  } else if (s.el.ended || !s.key) {
    // finished queue or a cleared element: start the current track over
    if (s.key !== it.key || s.offset > 0) load(s, it, 0)
    else s.el.currentTime = 0
  }
  wantPlay = true
  lastPos = state.positionSec
  prepareContext()
  set({ playing: true, buffering: true, error: undefined, screenStop: undefined })
  claimSession()
  playEl(s)
  session.setPlaying(true)
  startTicker()
}

/** Makes queue[i] current; `at` is a start position in seconds. */
function start(i: number, autoplay: boolean, at?: number) {
  const it = state.queue[i]
  if (!it) return
  ensure()
  cancelRetry()
  retries = 0
  counted = false
  listened = 0
  let s = active()
  if (!s) return
  const o = idle()
  const from = at ?? it.startSec ?? 0
  if (o && o.key === it.key && from === 0) {
    // the idle element already holds this track: swap
    s.loading = true
    s.el.pause()
    release(s)
    cur = 1 - cur
    s = o
    markActive()
  } else {
    if (o && o.key) release(o)
    else if (!o) for (const x of slots) if (x !== s && x.key) release(x)
    s.el.pause()
    load(s, it, from)
  }
  wantPlay = autoplay
  lastPos = from
  set({ index: i, positionSec: from, durationSec: it.durationSec, buffering: autoplay, playing: autoplay, error: undefined, screenStop: undefined })
  if (autoplay || handlersSet) claimSession()
  if (autoplay) {
    playEl(s)
    session.setPlaying(true)
    startTicker()
  } else session.setPlaying(false)
}

function finish() {
  wantPlay = false
  stopTicker()
  session.setPlaying(false)
  set({ playing: false, buffering: false })
  saveBook()
}

function onMetadata(s: Slot) {
  const it = item()
  if (it && s.pendingSeek !== null) {
    const t = s.pendingSeek
    s.pendingSeek = null
    if (s.transcoded) reloadAt(s, it, t)
    else s.el.currentTime = t
  }
  set({ durationSec: durationOf(s) })
}

function onTime(s: Slot) {
  const pos = positionOf(s)
  const dur = durationOf(s)
  const it = item()
  // Only time actually heard counts: a jump (seek, reload) adds nothing.
  const delta = pos - lastPos
  lastPos = pos
  if (wantPlay && delta > 0 && delta < 2) {
    listened += delta
    bookHeard = true
  }
  const need = dur > 0 ? Math.min(PLAY_COUNT_SEC, dur / 2) : PLAY_COUNT_SEC
  if (it && !counted && state.kind === 'music' && it.trackId && listened >= need && ours()) {
    counted = true
    const id = it.trackId
    recordPlay(id).catch((e: unknown) => console.warn('vault: play of track', id, 'not recorded', e))
  }
  // Near-gapless: the idle element loads the next track in time.
  const o = idle()
  if (o && dur > 0 && dur - pos <= PRELOAD_SEC) {
    const ni = nextIndex(state.queue.length, state.index, repeat(), true)
    const next = ni === null || ni === state.index ? undefined : state.queue[ni]
    if (next && !next.startSec && o.key !== next.key) {
      load(o, next, 0)
      o.el.preload = 'auto'
    }
  }
  if (pos !== state.positionSec || dur !== state.durationSec) set({ positionSec: pos, durationSec: dur })
}

function onEnded(s: Slot) {
  const ni = nextIndex(state.queue.length, state.index, repeat(), true)
  if (ni === null) {
    set({ positionSec: durationOf(s) })
    finish()
    return
  }
  if (ni === state.index) {
    const it = item()!
    counted = false
    listened = 0
    lastPos = 0
    if (s.offset > 0) load(s, it, 0)
    else s.el.currentTime = 0
    playEl(s)
    set({ positionSec: 0 })
    return
  }
  start(ni, true)
}

function cancelRetry() {
  if (retryTimer) clearTimeout(retryTimer)
  retryTimer = null
  errorSeq++
}

/** Fetches the current item again at `t` (a transcode starts there) and plays it if wanted. */
function reloadHere(s: Slot, it: QueueItem, t: number) {
  if (s.transcoded) {
    reloadAt(s, it, t)
    return
  }
  load(s, it, t)
  if (wantPlay) playEl(s)
}

/** Why the stream failed: a passing reason is worth fetching again (with the server's wait). */
async function verdict(s: Slot, code: number): Promise<{ transient: boolean; waitMs?: number; forbidden?: boolean }> {
  if (code === MEDIA_ERR_NETWORK) return { transient: true }
  const { status, retryAfterSec } = await probeStream(s.el.currentSrc || s.el.src)
  const transient = status === 0 || status === 408 || status === 429 || status >= 500
  return { transient, waitMs: retryAfterSec ? Math.min(retryAfterSec, RETRY_AFTER_MAX_SEC) * 1000 : undefined, forbidden: status === 403 }
}

/**
 * A refused stream of a kid: is it «Экранное время»? Then the listening
 * stops where it is (no skipping through the queue) with the kid's message.
 */
async function screenStopped(): Promise<boolean> {
  if (!authSession.get().user?.isKid) return false
  const st = await getMyScreenTime().catch(() => null)
  const c = st?.limited ? (state.kind === 'audiobook' || state.kind === 'music' ? st.audio : null) : null
  if (!c || c.allowed) return false
  active()?.el.pause()
  finish()
  set({ screenStop: c.message || t('player.screen.overFallback') })
  return true
}

function onError() {
  const s = active()
  const it = item()
  if (!s || !it) return
  cancelRetry()
  // Paused: never move on unasked. The item and its place stay; «Играть»
  // fetches it again there (resume sees the element's error).
  if (!wantPlay) {
    if (state.buffering) set({ buffering: false })
    return
  }
  const code = s.el.error?.code
  // no media error (a rejected play()), an undecodable file, or retried enough: unplayable
  if (code === undefined || code === MEDIA_ERR_DECODE || retries >= MAX_RETRIES) return skipFailed()
  const seq = errorSeq
  const stale = () => seq !== errorSeq || active() !== s || item()?.key !== it.key
  void verdict(s, code).then(async (v) => {
    if (stale() || !wantPlay) return
    if (v.forbidden && (await screenStopped())) return
    if (stale() || !wantPlay) return
    if (!v.transient) return skipFailed()
    retries++
    set({ buffering: true })
    retryTimer = setTimeout(() => {
      retryTimer = null
      if (stale() || !wantPlay) return
      reloadHere(s, it, state.positionSec)
    }, v.waitMs ?? RETRY_MS[Math.min(retries, RETRY_MS.length) - 1])
  })
}

/** The current track cannot be played: the next one, or a stop after a few in a row. */
function skipFailed() {
  errors++
  const s = active()
  if (errors >= MAX_ERRORS) {
    s?.el.pause()
    finish()
    set({ error: stopError() })
    showNotice(stopError())
    return
  }
  const ni = nextIndex(state.queue.length, state.index, repeat() === 'one' ? 'all' : repeat(), false)
  if (ni === null || ni === state.index) {
    finish()
    set({ error: stopError() })
    showNotice(stopError())
    return
  }
  showNotice(skipNotice())
  start(ni, true)
}

export const audio = {
  get: (): AudioState => state,

  /** Hides the «Экранное время» stop (the listening stays stopped). */
  dismissScreenStop() {
    if (state.screenStop) set({ screenStop: undefined, error: t('player.screen.overFallback') })
  },

  subscribe(fn: () => void): () => void {
    listeners.add(fn)
    return () => {
      listeners.delete(fn)
    }
  },

  /** Replaces the queue and starts items[start]. Shuffle stays as it was unless given. */
  play(items: QueueItem[], startAt = 0, opts: PlayOptions = {}) {
    if (items.length === 0) return
    ensure()
    prepareContext()
    saveBook() // the book that played until now
    lastBookSave = ''
    bookHold = null
    bookHeard = false
    owner = currentOwner()
    const kind = opts.kind ?? 'music'
    const rate = kind === 'audiobook' ? (opts.rate ?? state.rate) : 1
    if (rate !== state.rate) audio.setRate(rate)
    const list = uniqueKeys([], items)
    const i = Math.min(Math.max(startAt, 0), list.length - 1)
    const shuffle = opts.shuffle ?? state.shuffle
    errors = 0
    if (shuffle) {
      original = list
      set({ queue: shuffled(list, i), shuffle: true, kind, bookId: opts.bookId })
      start(0, true, opts.startSec)
    } else {
      original = null
      set({ queue: list, shuffle: false, kind, bookId: opts.bookId })
      start(i, true, opts.startSec)
    }
  },

  toggle() {
    if (!item()) return
    if (state.playing) audio.pause()
    else resume()
  },

  pause() {
    wantPlay = false
    cancelRetry()
    active()?.el.pause()
    stopTicker()
    session.setPlaying(false)
    if (state.playing || state.buffering) set({ playing: false, buffering: false })
    saveBook()
  },

  /**
   * Pauses and hands the system media controls (media keys, lock screen,
   * headphones) back, for a video or another player on the page. The next
   * play or resume claims them again.
   */
  yieldSession() {
    audio.pause()
    if (!handlersSet) return
    session.clear()
    handlersSet = false
  },

  next() {
    if (!item()) return
    const ni = nextIndex(state.queue.length, state.index, repeat(), false)
    if (ni === null) {
      audio.pause()
      audio.seek(0)
      return
    }
    start(ni, state.playing)
  },

  previous() {
    if (!item()) return
    if (state.positionSec > RESTART_SEC) {
      audio.seek(0)
      return
    }
    const pi = previousIndex(state.queue.length, state.index, repeat())
    if (pi === null) audio.seek(0)
    else start(pi, state.playing)
  },

  seek(sec: number) {
    const s = slots[cur]
    const it = item()
    if (!s || !it || !Number.isFinite(sec)) return
    const dur = state.durationSec
    const t = Math.max(0, dur > 0 ? Math.min(sec, dur) : sec)
    const local = t - s.offset
    if (local >= 0 && inSeekable(s.el, local)) s.el.currentTime = local
    else if (s.transcoded) reloadAt(s, it, t)
    else {
      // Direct file: the browser starts there. Before the metadata it is not
      // yet known whether this is a transcode: decide in onMetadata.
      if (s.el.readyState === 0) s.pendingSeek = t
      s.el.currentTime = local
    }
    if (s.el.ended && wantPlay) playEl(s)
    lastPos = t
    set({ positionSec: t })
    session.setPosition(t, durationOf(s), state.rate)
  },

  skip(deltaSec: number) {
    if (state.kind !== 'audiobook' || state.queue.length < 2) {
      audio.seek(state.positionSec + deltaSec)
      return
    }
    // a book skips across its files: from the end of one into the next
    const { ms, total } = bookPosition()
    let t = Math.min(Math.max(ms / 1000 + deltaSec, 0), total / 1000)
    let i = 0
    while (i < state.queue.length - 1 && t >= state.queue[i]!.durationSec && state.queue[i]!.durationSec > 0) {
      t -= state.queue[i]!.durationSec
      i++
    }
    audio.jumpTo(i, t)
  },

  setShuffle(on: boolean) {
    if (on === state.shuffle) return
    if (on) {
      original = state.queue
      set({ queue: shuffled(state.queue, state.index), index: state.index < 0 ? -1 : 0, shuffle: true })
      relevelIdle()
      return
    }
    const key = item()?.key
    const queue = original ?? state.queue
    original = null
    const index = key ? Math.max(0, queue.findIndex((q) => q.key === key)) : state.index
    set({ queue, index, shuffle: false })
    relevelIdle()
  },

  cycleRepeat() {
    const next: Record<RepeatMode, RepeatMode> = { off: 'all', all: 'one', one: 'off' }
    set({ repeat: next[state.repeat] })
  },

  setRate(r: number) {
    const rate = Math.min(Math.max(r, 0.5), 3)
    for (const s of slots) {
      s.el.defaultPlaybackRate = rate
      s.el.playbackRate = rate
    }
    set({ rate })
  },

  setVolume(v: number) {
    const volume = Math.min(Math.max(v, 0), 1)
    set({ volume })
    for (const s of slots) applyLevel(s)
  },

  /** The sleep timer's fade-out (0..1), applied over the volume; not part of the state. */
  setFade(f: number) {
    fade = Math.min(Math.max(Number.isFinite(f) ? f : 1, 0), 1)
    for (const s of slots) applyLevel(s)
  },

  /** «Выравнивание громкости» on this device. */
  normalize: (): NormalizeMode => normalizeMode,

  /** Changes «Выравнивание громкости»: remembered, and the loaded tracks follow at once (smoothly). */
  setNormalize(mode: NormalizeMode) {
    normalizeMode = mode
    saveNormalize(mode)
    if (mode !== 'off') prepareContext()
    for (const s of slots) {
      const it = s.key ? state.queue.find((q) => q.key === s.key) : undefined
      s.gain = it && state.kind === 'music' ? gainOf(it) : 1
      applyLevel(s, true)
    }
  },

  /** Inserts items right after the current one. */
  playNext(items: QueueItem[]) {
    if (items.length === 0) return
    if (!item()) return audio.play(items, 0)
    const add = uniqueKeys(state.queue, items)
    const q = state.queue
    if (original) {
      const at = original.findIndex((x) => x.key === item()!.key)
      original = [...original.slice(0, at + 1), ...add, ...original.slice(at + 1)]
    }
    set({ queue: [...q.slice(0, state.index + 1), ...add, ...q.slice(state.index + 1)] })
    relevelIdle()
  },

  addToQueue(items: QueueItem[]) {
    if (items.length === 0) return
    if (!item()) return audio.play(items, 0)
    const add = uniqueKeys(state.queue, items)
    if (original) original = [...original, ...add]
    set({ queue: [...state.queue, ...add] })
    relevelIdle()
  },

  removeAt(i: number) {
    if (i < 0 || i >= state.queue.length) return
    const key = state.queue[i]!.key
    const queue = state.queue.filter((_, j) => j !== i)
    if (original) original = original.filter((x) => x.key !== key)
    if (queue.length === 0) return audio.stop()
    if (i < state.index) {
      set({ queue, index: state.index - 1 })
      relevelIdle()
    } else if (i > state.index) {
      set({ queue })
      relevelIdle()
    } else {
      const playing = state.playing
      set({ queue })
      start(Math.min(i, queue.length - 1), playing)
    }
  },

  /**
   * Makes queue[i] current (a tap in the queue): one load, play state kept.
   * `at` starts that many seconds into it (an audiobook chapter); on the
   * current item it is a seek.
   */
  jumpTo(i: number, at?: number) {
    if (i < 0 || i >= state.queue.length) return
    if (i === state.index) {
      if (at !== undefined) audio.seek(at)
      return
    }
    errors = 0
    start(i, state.playing, at)
  },

  /**
   * Moves queue[from] to position `to` in one update; the current track
   * stays current. Under shuffle only the shuffled order changes: the
   * original order (restored by shuffle off) is kept.
   */
  move(from: number, to: number) {
    const n = state.queue.length
    if (from === to || from < 0 || to < 0 || from >= n || to >= n) return
    const key = item()?.key
    const queue = [...state.queue]
    const [it] = queue.splice(from, 1)
    queue.splice(to, 0, it!)
    set({ queue, index: key ? queue.findIndex((q) => q.key === key) : state.index })
    relevelIdle()
  },

  /** The <audio> element playing now (AirPlay picker, availability events); null before the first play. */
  element(): HTMLAudioElement | null {
    return active()?.el ?? null
  },

  /**
   * «Семья»: a jump to `fromMs` of the book (another profile's place) keeps
   * `posMs` as this profile's position until it plays on there; null drops it.
   */
  holdBook(hold: Hold | null) {
    bookHold = hold
  },

  /** The «Семья» hold of the book, if any (this profile's place it keeps). */
  bookHold(): Hold | null {
    return bookHold
  },

  /** Empties the queue: the mini player disappears. */
  stop() {
    saveBook()
    teardown()
  },
}

/** Silences both elements, drops their sources and empties the queue and the system controls. */
function teardown() {
  wantPlay = false
  cancelRetry()
  retries = 0
  for (const s of slots) {
    s.el.pause()
    release(s)
  }
  stopTicker()
  original = null
  counted = false
  errors = 0
  handlersSet = false
  bookHold = null
  session.clear()
  const { shuffle, repeat, rate, volume } = state
  state = { ...initial(), shuffle, repeat, rate, volume }
  for (const fn of listeners) fn()
}

/** The session moves away from the profile this listening belongs to. */
function leaves(next: SessionState): boolean {
  if (next.user) return next.user.id !== owner
  // offline the device's owner listens on to what is saved here
  return next.status !== 'offline'
}

authSession.beforeChange((next, _prev, canSend) => {
  if (state.queue.length === 0 || !leaves(next)) return
  // still the outgoing profile's state: its position is saved (or queued) for it
  const saved = saveBook(false, canSend)
  lastBookSave = ''
  bookHeard = false
  showNotice(null)
  fade = 1
  teardown()
  return saved ?? undefined
})

/** Tests only: drops the elements and every bit of state. */
export function resetAudioForTests() {
  if (slots.length) audio.stop()
  for (const s of slots) s.el.remove()
  slots = []
  cur = 0
  fade = 1
  normalizeMode = loadNormalize()
  audioCtx = null
  for (const t of ramps.values()) clearInterval(t)
  ramps.clear()
  lastBookSave = ''
  lastSave = null
  bookHeard = false
  bookHold = null
  owner = null
  state = initial()
  showNotice(null)
  for (const fn of listeners) fn()
}
