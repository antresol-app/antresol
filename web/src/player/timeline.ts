// Pure helpers for the player and the item page: time formatting, multi-part
// movies (cd1/cd2), resume positions, track labels and WebVTT parsing.
import type { MediaFile, SubTrack, Track } from '../api/types'
import { t, translate, type Key } from '../i18n'

/** 4365 → "1:12:45", 65 → "1:05". */
export function clock(sec: number): string {
  const s = Math.max(0, Math.floor(Number.isFinite(sec) ? sec : 0))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const ss = String(s % 60).padStart(2, '0')
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`
}

/** 166 → "2 ч 46 мин" / "2 hr 46 min", 45 → "45 мин". */
export function humanMinutes(min: number): string {
  const m = Math.round(min)
  if (m <= 0) return ''
  const h = Math.floor(m / 60)
  const r = m % 60
  if (h === 0) return t('player.time.min', { m: r })
  return r === 0 ? t('player.time.hr', { h }) : t('player.time.hrMin', { h, m: r })
}

const PART_RE = /(?:^|[\s._\-[(])(?:cd|dvd|disc|disk|part|pt|часть|диск)[\s._-]*(\d{1,2})(?=$|[\s._\-\])])/i

/** Part number from a file name ("Фильм - cd2.mkv" → 2), or 0. */
export function partNumber(name: string): number {
  const stem = name.replace(/\.[^.]+$/, '')
  const m = PART_RE.exec(stem)
  return m ? Number(m[1]) : 0
}

/**
 * The files of a movie to play one after another. When every file is a
 * numbered part (cd1, cd2 …) they are all played in order; otherwise the
 * files are alternative versions and only the chosen one plays.
 */
export function playbackParts(files: MediaFile[], chosenId?: number): MediaFile[] {
  if (files.length === 0) return []
  const numbered = files.map((f) => ({ f, n: partNumber(f.name) }))
  const distinct = new Set(numbered.map((x) => x.n))
  if (files.length > 1 && numbered.every((x) => x.n > 0) && distinct.size === files.length) {
    return numbered.sort((a, b) => a.n - b.n).map((x) => x.f)
  }
  const chosen = files.find((f) => f.id === chosenId)
  return [chosen ?? files[0]!]
}

/** True when the files are parts of one movie, not alternative versions. */
export const isMultiPart = (files: MediaFile[]) => playbackParts(files).length > 1

export const durationSec = (f: { durationMs?: number }) => (f.durationMs ?? 0) / 1000

/** Where part i starts on the whole movie's timeline. */
export function partStarts(parts: { durationMs?: number }[]): number[] {
  const out: number[] = []
  let acc = 0
  for (const p of parts) {
    out.push(acc)
    acc += durationSec(p)
  }
  return out
}

/** Maps a position on the whole timeline to (part, offset inside it). */
export function locate(parts: { durationMs?: number }[], absSec: number): { part: number; offset: number } {
  const starts = partStarts(parts)
  for (let i = parts.length - 1; i >= 0; i--) {
    const st = starts[i]!
    if (absSec >= st && (i === parts.length - 1 || durationSec(parts[i]!) > 0)) {
      return { part: i, offset: Math.max(0, absSec - st) }
    }
  }
  return { part: 0, offset: Math.max(0, absSec) }
}

/** Matches the server's «watched» threshold (progress.finishedPercent). */
export const FINISHED = 0.92

/** Resume point in seconds for a 0..1 progress, or 0 to start from the beginning. */
export function resumeSec(progress: number, totalSec: number): number {
  if (!(progress > 0) || progress >= FINISHED || !(totalSec > 0)) return 0
  const pos = progress * totalSec
  return pos < 10 ? 0 : Math.floor(pos)
}

// Language codes and spellings in track tags → the canonical code.
const ALIASES: Record<string, string> = {
  ru: 'ru', rus: 'ru', russian: 'ru', русский: 'ru',
  en: 'en', eng: 'en', english: 'en', английский: 'en',
  uk: 'uk', ukr: 'uk', ukrainian: 'uk', украинский: 'uk',
  de: 'de', ger: 'de', deu: 'de', german: 'de',
  fr: 'fr', fre: 'fr', fra: 'fr', french: 'fr',
  es: 'es', spa: 'es', spanish: 'es',
  it: 'it', ita: 'it', italian: 'it',
  ja: 'ja', jpn: 'ja', japanese: 'ja',
  zh: 'zh', chi: 'zh', zho: 'zh', chinese: 'zh',
  ko: 'ko', kor: 'ko', korean: 'ko',
  pl: 'pl', pol: 'pl',
  pt: 'pt', por: 'pt',
  tr: 'tr', tur: 'tr',
  be: 'be', bel: 'be',
  kk: 'kk', kaz: 'kk',
  he: 'he', heb: 'he',
  ar: 'ar', ara: 'ar',
  sv: 'sv', swe: 'sv',
  fi: 'fi', fin: 'fi',
  nl: 'nl', dut: 'nl', nld: 'nl',
  cs: 'cs', cze: 'cs', ces: 'cs',
  hi: 'hi', hin: 'hi',
}

// The name of each canonical code in the catalog.
const NAMES: Record<string, Key> = {
  ru: 'common.lang.ru', en: 'common.lang.en', uk: 'common.lang.uk', de: 'common.lang.de', fr: 'common.lang.fr',
  es: 'common.lang.es', it: 'common.lang.it', ja: 'common.lang.ja', zh: 'common.lang.zh', ko: 'common.lang.ko',
  pl: 'common.lang.pl', pt: 'common.lang.pt',
  tr: 'player.lang.tr', be: 'player.lang.be', kk: 'player.lang.kk', he: 'player.lang.he', ar: 'player.lang.ar',
  sv: 'player.lang.sv', fi: 'player.lang.fi', nl: 'player.lang.nl', cs: 'player.lang.cs', hi: 'player.lang.hi',
}

/** The canonical code of a language tag or name ("rus", "Russian" → "ru"), '' when unknown. */
export const langCode = (lang: string): string => ALIASES[lang.trim().toLowerCase()] ?? ''

export function languageName(lang: string): string {
  const k = lang.trim().toLowerCase()
  if (!k || k === 'und') return ''
  const code = ALIASES[k]
  if (code) return t(NAMES[code]!)
  return k.length > 3 ? lang.charAt(0).toUpperCase() + lang.slice(1) : k.toUpperCase()
}

/** «RU» for "rus", "ru" or "Russian" (a badge); an unknown short tag as is, uppercased; '' for none. */
export function langBadge(lang: string): string {
  const k = lang.trim().toLowerCase()
  if (!k || k === 'und') return ''
  const code = ALIASES[k]
  return code ? code.toUpperCase() : k.length <= 3 ? k.toUpperCase() : ''
}

/** Normalised language key for remembering a choice ("rus" and "ru" are the same). */
export function langKey(lang: string): string {
  // the Russian name, whatever the interface language: remembered choices stay valid
  const code = langCode(lang)
  if (code) return translate('ru', NAMES[code]!)
  const n = languageName(lang)
  return n || lang.toLowerCase()
}

/** The track title when it adds something beyond the language ("ru", "Russian" do not). */
function extraName(name: string, lang: string, langName: string): string {
  const n = name.trim()
  if (!n || !langName) return n && !langName ? '' : n
  const l = n.toLowerCase()
  if (l === lang.toLowerCase() || l === langName.toLowerCase() || languageName(n) === langName) return ''
  const code = langCode(lang)
  if (code && langCode(n) === code) return ''
  return n
}

function channelLabel(ch?: number): string {
  if (!ch) return ''
  if (ch === 1) return t('player.track.mono')
  if (ch === 2) return t('player.track.stereo')
  if (ch === 6) return '5.1'
  if (ch === 8) return '7.1'
  return t('player.track.channels', { n: ch })
}

/** The audio menu's order: each cleaned track (kind "cleaned") right under
 *  the track it was made from (cleanedFrom), the rest as the file lists
 *  them; i is the track's position in the list (its label's fallback). */
export function audioMenuOrder(audio: Track[]): { tr: Track; i: number }[] {
  const all = audio.map((tr, i) => ({ tr, i }))
  const placed = (x: { tr: Track }) => x.tr.kind === 'cleaned' && audio.some((s) => s.index === x.tr.cleanedFrom && s.kind !== 'cleaned')
  const out: { tr: Track; i: number }[] = []
  for (const x of all) {
    if (placed(x)) continue
    out.push(x)
    if (x.tr.kind !== 'cleaned') out.push(...all.filter((c) => placed(c) && c.tr.cleanedFrom === x.tr.index))
  }
  return out
}

/** "Русский", with the track title and channel layout as detail. */
export function audioLabel(tr: Track, i: number): { title: string; detail: string; cleaned?: { badge: string; hint: string; aria: string } } {
  const lang = languageName(tr.lang)
  const name = tr.title.trim()
  const title = lang || name || t('player.track.audioN', { n: i + 1 })
  const extra = extraName(name, tr.lang, lang)
  const detail = [extra, channelLabel(tr.channels)].filter(Boolean).join(' · ')
  if (tr.kind !== 'cleaned') return { title, detail }
  // «Очищено»: a capsule beside the label; read out as words, the method in its tooltip
  const what = extra ? t('player.track.cleanedAria', { name: extra }) : t('player.track.cleanedAriaNoName')
  const method = tr.method === 'ml' ? t('player.track.method.ml') : t('player.track.method.subtraction')
  return {
    title,
    detail,
    cleaned: {
      badge: t('player.track.cleaned'),
      hint: t('player.track.cleanedHint', { method }),
      aria: [title, what, channelLabel(tr.channels)].filter(Boolean).join(', '),
    },
  }
}

export function subtitleLabel(s: SubTrack, i: number): { title: string; detail: string } {
  const lang = languageName(s.lang)
  const name = (s.title ?? '').trim() // an older online answer named it «label»
  const title = lang || name || t('player.track.subtitlesN', { n: i + 1 })
  const parts = [extraName(name, s.lang, lang)]
  if (s.forced) parts.push(t('player.track.forcedOnly'))
  if (s.burnIn) parts.push(t('player.track.notSupported'))
  return { title, detail: parts.filter(Boolean).join(' · ') }
}

export interface Cue {
  start: number
  end: number
  text: string
}

function vttTime(s: string): number {
  const m = /^(?:(\d+):)?(\d{1,2}):(\d{2})[.,](\d{1,3})$/.exec(s.trim())
  if (!m) return NaN
  return Number(m[1] ?? 0) * 3600 + Number(m[2]) * 60 + Number(m[3]) + Number(m[4]!.padEnd(3, '0')) / 1000
}

const ENTITIES: Record<string, string> = { '&amp;': '&', '&lt;': '<', '&gt;': '>', '&nbsp;': ' ', '&quot;': '"', '&#39;': "'" }

/** Minimal WebVTT parser: cue timings and plain text (markup removed). */
export function parseVtt(src: string): Cue[] {
  const out: Cue[] = []
  const blocks = src.replace(/\r\n?/g, '\n').split(/\n{2,}/)
  for (const b of blocks) {
    const lines = b.split('\n')
    const ti = lines.findIndex((l) => l.includes('-->'))
    if (ti < 0) continue
    const [a, rest] = lines[ti]!.split('-->')
    const start = vttTime(a ?? '')
    const end = vttTime((rest ?? '').trim().split(/\s+/)[0] ?? '')
    if (!Number.isFinite(start) || !Number.isFinite(end)) continue
    const text = lines
      .slice(ti + 1)
      .join('\n')
      .replace(/<[^>]*>/g, '')
      .replace(/&(amp|lt|gt|nbsp|quot|#39);/g, (e) => ENTITIES[e] ?? e)
      .trim()
    if (text) out.push({ start, end, text })
  }
  return out.sort((x, y) => x.start - y.start)
}

/** Cues visible at time t (binary search over sorted cues; overlaps allowed). */
export function activeCues(cues: Cue[], t: number): Cue[] {
  let lo = 0
  let hi = cues.length
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (cues[mid]!.start <= t) lo = mid + 1
    else hi = mid
  }
  const out: Cue[] = []
  for (let i = lo - 1; i >= 0 && i >= lo - 20; i--) {
    const c = cues[i]!
    if (c.end > t) out.unshift(c)
  }
  return out
}
