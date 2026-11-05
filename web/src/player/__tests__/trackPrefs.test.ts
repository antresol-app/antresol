import { describe, expect, it } from 'vitest'
import type { PlaybackPrefs, SubTrack, Track } from '../../api/types'
import { pickAudio, pickSubtitle, trackLang, sameTitle, offsetCueTime } from '../trackPrefs'

const a = (index: number, lang: string, title = '', def = false): Track => ({ index, codec: 'aac', lang, title, default: def })
const s = (index: number, lang: string, title = '', forced = false): SubTrack => ({
  index, codec: 'subrip', lang, title, forced, default: false, external: false, burnIn: false, url: `/api/subs/1/${index}.vtt`,
})
const prefs = (p: Partial<PlaybackPrefs> = {}): PlaybackPrefs => ({
  audioLangs: ['ru', 'en'], subMode: 'off', subLangs: [], subSize: 'm', subBackground: 'translucent', custom: true, ...p,
})

// an English film with three Russian voice-overs, the original by default
const dubs = [a(1, 'eng', 'Original', true), a(2, 'rus', 'VSI Moscow'), a(3, 'rus', 'TVShows'), a(4, 'rus', 'Кубик в кубе')]

describe('trackLang', () => {
  it('reads the tag, else the title', () => {
    expect(trackLang({ lang: 'rus', title: 'VSI Moscow' })).toBe('ru')
    expect(trackLang({ lang: 'English', title: '' })).toBe('en')
    expect(trackLang({ lang: 'und', title: 'Кубик в кубе' })).toBe('ru')
    expect(trackLang({ lang: '', title: 'English 5.1' })).toBe('en')
    expect(trackLang({ lang: 'und', title: 'Commentary' })).toBe('')
  })
})

describe('sameTitle', () => {
  it('ignores case, ё and punctuation, and a channel suffix', () => {
    expect(sameTitle('Кубик в кубе', 'кубик в Кубе')).toBe(true)
    expect(sameTitle('Кубик в кубе', 'MVO Кубик в Кубе (5.1)')).toBe(true)
    expect(sameTitle('TVShows', 'TV Shows')).toBe(true)
    expect(sameTitle('Ёлки', 'елки')).toBe(true)
    expect(sameTitle('TVShows', 'VSI Moscow')).toBe(false)
    expect(sameTitle('', '')).toBe(false)
  })
})

describe('pickAudio', () => {
  it('takes the series voice by its title, wherever the next file lists it', () => {
    const next = [a(1, 'eng', '', true), a(2, 'rus', 'Кубик в Кубе'), a(3, 'rus', 'TVShows')]
    expect(pickAudio(next, { series: { lang: 'rus', title: 'Кубик в кубе' }, prefs: prefs() })).toBe(2)
  })
  it('falls back to the series language when that voice is gone', () => {
    const next = [a(1, 'eng', '', true), a(2, 'rus', 'LostFilm')]
    expect(pickAudio(next, { series: { lang: 'rus', title: 'Кубик в кубе' }, prefs: prefs({ audioLangs: ['en'] }) })).toBe(2)
  })
  it('then the profile languages, best first: a Russian dub over the English default', () => {
    expect(pickAudio(dubs, { prefs: prefs() })).toBe(2)
    expect(pickAudio(dubs, { prefs: prefs({ audioLangs: ['en', 'ru'] }) })).toBeUndefined() // the default already
  })
  it('prefers the file default within a language', () => {
    const t = [a(1, 'rus', 'TVShows'), a(2, 'rus', 'Дубляж', true), a(3, 'eng')]
    expect(pickAudio(t, { prefs: prefs() })).toBeUndefined()
    const t2 = [a(1, 'eng', '', true), a(2, 'rus', 'TVShows'), a(3, 'rus', 'Дубляж')]
    expect(pickAudio(t2, { prefs: prefs() })).toBe(2)
  })
  it('keeps the file default when no language matches', () => {
    expect(pickAudio([a(1, 'jpn', '', true), a(2, 'kor')], { prefs: prefs() })).toBeUndefined()
    expect(pickAudio([], { prefs: prefs() })).toBeUndefined()
  })
  it('without the profile’s own defaults, this device’s last language comes first', () => {
    const p = prefs({ custom: false })
    expect(pickAudio(dubs, { prefs: p, device: 'Английский' })).toBeUndefined()
    expect(pickAudio([a(1, 'rus', '', true), a(2, 'eng')], { prefs: p, device: 'Английский' })).toBe(2)
    // the profile's own defaults win over the device
    expect(pickAudio([a(1, 'rus', '', true), a(2, 'eng')], { prefs: prefs(), device: 'Английский' })).toBeUndefined()
  })
})

describe('pickSubtitle', () => {
  const subs = [s(3, 'eng', 'Forced', true), s(4, 'rus', 'Forced', true), s(5, 'eng', 'Full'), s(6, 'rus', 'Полные'), s(7, 'rus', 'SDH')]
  const ru = a(2, 'rus', 'VSI Moscow')
  const en = a(1, 'eng', '', true)

  it('off: only the forced track of the audio language', () => {
    expect(pickSubtitle(subs, ru, { prefs: prefs() })).toBe(4)
    expect(pickSubtitle(subs, en, { prefs: prefs() })).toBe(3)
    expect(pickSubtitle([s(5, 'eng')], ru, { prefs: prefs() })).toBe(-1)
  })
  it('foreign: subtitles when the audio is in none of the profile languages', () => {
    const p = prefs({ audioLangs: ['ru'], subMode: 'foreign', subLangs: ['ru', 'en'] })
    expect(pickSubtitle(subs, en, { prefs: p })).toBe(6)
    expect(pickSubtitle(subs, ru, { prefs: p })).toBe(4) // forced only
  })
  it('always: the first subtitle language, a full track over a forced one', () => {
    expect(pickSubtitle(subs, ru, { prefs: prefs({ subMode: 'always', subLangs: ['en'] }) })).toBe(5)
    expect(pickSubtitle(subs, ru, { prefs: prefs({ subMode: 'always', subLangs: [] }) })).toBe(6) // the audio languages
    expect(pickSubtitle([s(3, 'eng', '', true)], ru, { prefs: prefs({ subMode: 'always', subLangs: ['en'] }) })).toBe(3)
  })
  it('the series choice comes first: by title, then by language; off stays off but for forced', () => {
    const p = prefs({ subMode: 'always', subLangs: ['en'] })
    expect(pickSubtitle(subs, ru, { prefs: p, series: { lang: 'rus', title: 'SDH' } })).toBe(7)
    expect(pickSubtitle([s(5, 'eng'), s(8, 'rus', 'Другие')], ru, { prefs: p, series: { lang: 'rus', title: 'SDH' } })).toBe(8)
    expect(pickSubtitle(subs, ru, { prefs: p, seriesOff: true })).toBe(4)
  })
  it('image tracks and tracks without a url are never picked', () => {
    const img: SubTrack = { ...s(9, 'rus', 'PGS'), burnIn: true, url: undefined }
    expect(pickSubtitle([img], ru, { prefs: prefs({ subMode: 'always' }) })).toBe(-1)
  })
  it('without the profile’s own defaults: this device’s last language, else forced', () => {
    const p = prefs({ custom: false })
    expect(pickSubtitle(subs, ru, { prefs: p, device: 'Английский' })).toBe(5)
    expect(pickSubtitle(subs, ru, { prefs: p, device: '' })).toBe(4)
  })
})

describe('offsetCueTime', () => {
  it('a later offset shows a cue later', () => {
    expect(offsetCueTime(10, 500)).toBe(9.5)
    expect(offsetCueTime(10, -250)).toBe(10.25)
  })
})

describe('review fixes', () => {
  it('an exact title wins over one that only contains it', () => {
    const t = [a(1, 'eng', '', true), a(2, 'rus', 'Кубик в кубе (Director’s Cut)'), a(3, 'rus', 'Кубик в кубе')]
    expect(pickAudio(t, { series: { lang: 'rus', title: 'Кубик в кубе' }, prefs: prefs() })).toBe(3)
  })
  it('codec-only titles name no voice', () => {
    expect(sameTitle('AC3 5.1', 'AC3 5.1')).toBe(false)
    expect(sameTitle('DTS', 'DTS-HD MA')).toBe(false)
    expect(sameTitle('Stereo', 'Stereo')).toBe(false)
    // the remembered «AC3 5.1» English track: by language only (the first English one)
    const t = [a(1, 'rus', 'AC3 5.1', true), a(2, 'eng', 'Commentary'), a(3, 'eng', 'AC3 5.1')]
    expect(pickAudio(t, { series: { lang: 'eng', title: 'AC3 5.1' }, prefs: prefs() })).toBe(2)
  })
  it('two-letter words in a title are not language tags', () => {
    expect(trackLang({ lang: 'und', title: 'It' })).toBe('')
    expect(trackLang({ lang: '', title: 'Be Kind Rewind' })).toBe('')
    expect(trackLang({ lang: '', title: 'Hi-Fi' })).toBe('')
  })
  it('a Cyrillic title with Ukrainian letters is Ukrainian', () => {
    expect(trackLang({ lang: 'und', title: 'Українська озвучка' })).toBe('uk')
    expect(trackLang({ lang: 'und', title: 'Їжак' })).toBe('uk')
    expect(trackLang({ lang: 'und', title: 'Кубик в кубе' })).toBe('ru')
  })
})
