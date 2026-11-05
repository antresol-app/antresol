import { describe, expect, it } from 'vitest'
import type { MediaFile } from '../../api/types'
import { activeCues, audioLabel, subtitleLabel, clock, humanMinutes, langKey, locate, parseVtt, partNumber, playbackParts, resumeSec } from '../timeline'
import { setLanguagePref } from '../../i18n'

const file = (id: number, name: string, durationMs = 0): MediaFile => ({ id, name, size: 1, durationMs, audio: [], subs: [] })

describe('timeline', () => {
  it('formats clock and human durations', () => {
    expect(clock(4360)).toBe('1:12:40')
    expect(clock(65)).toBe('1:05')
    expect(clock(-3)).toBe('0:00')
    expect(humanMinutes(166)).toBe('2 ч 46 мин')
    expect(humanMinutes(120)).toBe('2 ч')
    expect(humanMinutes(45)).toBe('45 мин')
  })

  it('recognises numbered parts', () => {
    expect(partNumber('Москва слезам не верит (1979) - cd2.mkv')).toBe(2)
    expect(partNumber('Film.Part.1.1080p.mkv')).toBe(1)
    expect(partNumber('Брат 2 (2000) BDRip 1080p.mkv')).toBe(0)
    expect(partNumber('Интерстеллар.2014.2160p.mkv')).toBe(0)
  })

  it('plays parts in order but only one of several versions', () => {
    const parts = [file(2, 'X - cd2.mkv', 60_000), file(1, 'X - cd1.mkv', 50_000)]
    expect(playbackParts(parts).map((f) => f.id)).toEqual([1, 2])
    const versions = [file(5, 'X.1080p.mkv'), file(6, 'X.2160p.mkv')]
    expect(playbackParts(versions).map((f) => f.id)).toEqual([5])
    expect(playbackParts(versions, 6).map((f) => f.id)).toEqual([6])
  })

  it('maps a whole-movie position to a part', () => {
    const parts = [file(1, 'a cd1', 50_000), file(2, 'a cd2', 60_000)]
    expect(locate(parts, 10)).toEqual({ part: 0, offset: 10 })
    expect(locate(parts, 70)).toEqual({ part: 1, offset: 20 })
  })

  it('resumes only unfinished progress', () => {
    expect(resumeSec(0.5, 1000)).toBe(500)
    expect(resumeSec(0.95, 1000)).toBe(0)
    expect(resumeSec(0.001, 1000)).toBe(0)
  })

  it('labels audio tracks in Russian', () => {
    expect(audioLabel({ index: 1, codec: 'ac3', lang: 'rus', title: 'Дубляж', channels: 6, default: true }, 0)).toEqual({
      title: 'Русский',
      detail: 'Дубляж · 5.1',
    })
    expect(audioLabel({ index: 2, codec: 'aac', lang: '', title: '', channels: 2, default: false }, 1).title).toBe('Дорожка 2')
  })

  it('does not repeat the language as the track title', () => {
    const sub = { index: 1000, codec: 'subrip', lang: 'ru', title: 'ru', forced: false, default: false, external: true, burnIn: false }
    expect(subtitleLabel(sub, 0)).toEqual({ title: 'Русский', detail: '' })
    expect(subtitleLabel({ ...sub, title: 'Russian', forced: true }, 0)).toEqual({ title: 'Русский', detail: 'Только надписи' })
    expect(subtitleLabel({ ...sub, title: 'Полные' }, 0).detail).toBe('Полные')
  })

  it('parses WebVTT and finds active cues', () => {
    const cues = parseVtt('WEBVTT\n\n1\n00:00:01.000 --> 00:00:03.500\n<i>Привет</i> &amp; пока\n\n00:04.000 --> 00:05.000 align:start\nВторая\n')
    expect(cues).toEqual([
      { start: 1, end: 3.5, text: 'Привет & пока' },
      { start: 4, end: 5, text: 'Вторая' },
    ])
    expect(activeCues(cues, 2).map((c) => c.text)).toEqual(['Привет & пока'])
    expect(activeCues(cues, 3.7)).toEqual([])
  })

  it('speaks English when the interface is English; remembered language keys stay Russian', () => {
    setLanguagePref('en')
    expect(humanMinutes(166)).toBe('2 hr 46 min')
    expect(audioLabel({ index: 1, codec: 'ac3', lang: 'rus', title: 'Russian', channels: 6, default: true }, 0)).toEqual({ title: 'Russian', detail: '5.1' })
    expect(audioLabel({ index: 2, codec: 'aac', lang: '', title: '', channels: 2, default: false }, 1)).toEqual({ title: 'Track 2', detail: 'Stereo' })
    expect(subtitleLabel({ index: 3, codec: 'subrip', lang: 'eng', title: 'Русский', forced: true, default: false } as never, 0)).toEqual({ title: 'English', detail: 'Русский · Forced only' })
    expect(langKey('eng')).toBe('Английский')
  })
})
