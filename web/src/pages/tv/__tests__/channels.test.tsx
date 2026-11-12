import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Channel, ChannelTrack } from '../../../api/channels'
import { rulesSummary } from '../TvManage'
import { ChannelCard } from '../ChannelCard'
import { ChannelRow } from '../ChannelRow'
import {
  applyFilter,
  blockOf,
  filterParam,
  gridMove,
  guideOrigin,
  labelInset,
  mergePrograms,
  minutesLeft,
  parseFilter,
  pickAudioSlot,
  pickSubSlot,
  playingAudio,
  progressOf,
  renditionIndex,
  renditionsToPlay,
  themesOf,
  ticksOf,
  validFilter,
  withFavorite,
  zapTarget,
} from '../model'

const ch = (id: number, number: number, category: string, extra: Partial<Channel> = {}): Channel => ({
  id, number, name: `Канал ${id}`, category, personal: false, auto: false, enabled: true, canEdit: false, ...extra,
})

const at = (hhmm: string) => `2026-11-10T${hhmm}:00Z`
const ms = (hhmm: string) => Date.parse(at(hhmm))

describe('the one numbered list', () => {
  const list = [ch(1, 40, 'Б'), ch(2, 10, 'А', { favorite: true }), ch(3, 5, 'Б'), ch(4, 12, 'А')]

  it('keeps number order under every filter; themes come in the order of their first channel', () => {
    expect(applyFilter(list, { kind: 'all' }).map((c) => c.number)).toEqual([5, 10, 12, 40])
    expect(applyFilter(list, { kind: 'theme', theme: 'А' }).map((c) => c.number)).toEqual([10, 12])
    expect(applyFilter(list, { kind: 'fav' }).map((c) => c.id)).toEqual([2])
    expect(themesOf(list)).toEqual(['Б', 'А'])
  })

  it('reads and writes the filter in the address; a theme that is gone falls back to all', () => {
    expect(parseFilter('fav')).toEqual({ kind: 'fav' })
    expect(parseFilter('c:Кино по жанрам')).toEqual({ kind: 'theme', theme: 'Кино по жанрам' })
    expect(parseFilter(null)).toEqual({ kind: 'all' })
    expect(filterParam({ kind: 'theme', theme: 'А' })).toBe('c:А')
    expect(filterParam({ kind: 'all' })).toBe('')
    expect(validFilter(list, { kind: 'theme', theme: 'В' })).toEqual({ kind: 'all' })
  })

  it('stars a channel without moving it', () => {
    const l = withFavorite(list, 3, true)
    expect(applyFilter(l, { kind: 'all' }).map((c) => c.id)).toEqual([3, 2, 4, 1])
    expect(applyFilter(l, { kind: 'fav' }).map((c) => c.number)).toEqual([5, 10])
  })
})

describe('zapping', () => {
  const list = [ch(1, 3, 'А'), ch(2, 1, 'А'), ch(3, 7, 'Б'), ch(4, 12, 'Б')]

  it('goes by number and wraps round', () => {
    expect(zapTarget(list, 2, 1)?.number).toBe(3)
    expect(zapTarget(list, 4, 1)?.number).toBe(1)
    expect(zapTarget(list, 2, -1)?.number).toBe(12)
  })

  it('stays within the list it was opened from', () => {
    const fav = applyFilter(withFavorite(withFavorite(list, 2, true), 4, true), { kind: 'fav' })
    expect(zapTarget(fav, 2, 1)?.number).toBe(12)
    expect(zapTarget(fav, 4, 1)?.number).toBe(1)
  })

  it('a channel no longer in the list zaps from where its number stands', () => {
    const fav = [ch(2, 1, 'А'), ch(4, 12, 'Б')]
    expect(zapTarget(fav, 3, 1, 7)?.number).toBe(12)
    expect(zapTarget(fav, 3, -1, 7)?.number).toBe(1)
    expect(zapTarget([ch(1, 3, 'А')], 1, 1)).toBeUndefined()
  })
})

describe('time', () => {
  const p = { itemId: 1, title: 'x', start: at('12:00'), end: at('13:00') }
  it('measures a programme and the minutes left', () => {
    expect(progressOf(p, ms('12:15'))).toBeCloseTo(0.25)
    expect(progressOf(p, ms('14:00'))).toBe(1)
    expect(minutesLeft(p, ms('12:35'))).toBe(25)
    expect(minutesLeft(p, Date.parse('2026-11-10T12:59:50Z'))).toBe(1)
  })
})

describe('the guide grid', () => {
  it('starts an hour back on a half hour', () => {
    expect(guideOrigin(Date.parse('2026-11-10T21:47:13Z'))).toBe(ms('20:30'))
    expect(guideOrigin(ms('21:00'))).toBe(ms('20:00'))
  })

  it('draws a programme as wide as it lasts, cut at the window, past dimmed and on air lit', () => {
    const origin = ms('20:00')
    const end = ms('23:00')
    const now = ms('21:10')
    expect(blockOf({ start: at('21:00'), end: at('22:30') }, origin, end, 6, now)).toEqual({ left: 360, width: 540, past: false, live: true, cut: false })
    expect(blockOf({ start: at('19:30'), end: at('20:30') }, origin, end, 6, now)).toEqual({ left: 0, width: 180, past: true, live: false, cut: true })
    expect(blockOf({ start: at('22:45'), end: at('23:30') }, origin, end, 6, now)?.width).toBe(90)
    expect(blockOf({ start: at('18:00'), end: at('19:00') }, origin, end, 6, now)).toBeNull()
  })

  it('keeps a label in sight: a block begun left of the visible time shows its title at the visible edge', () => {
    // in view: no shift
    expect(labelInset(400, 300, 100)).toBe(0)
    expect(labelInset(400, 300, 400)).toBe(0)
    // began 120 px before the visible edge: the label moves 120 px
    expect(labelInset(400, 300, 520)).toBe(120)
    // but stops where 64 px of it still fit in the block
    expect(labelInset(400, 300, 690)).toBe(236)
    // a block narrower than a label never moves its label
    expect(labelInset(400, 50, 430)).toBe(0)
  })

  it('ticks every half hour, the hours marked', () => {
    const tz = new Date(ms('20:00')).getMinutes() === 0
    const ticks = ticksOf(ms('20:00'), ms('21:30'), 4)
    expect(ticks.map((t) => t.x)).toEqual([0, 120, 240])
    if (tz) expect(ticks.map((t) => t.hour)).toEqual([true, false, true])
  })

  it('merges the windows it pages through, one copy each', () => {
    const a = [{ id: '1-1', itemId: 1, title: 'a', start: at('20:00'), end: at('21:00') }]
    const b = [
      { id: '1-1', itemId: 1, title: 'a', start: at('20:00'), end: at('21:00') },
      { id: '1-2', itemId: 2, title: 'b', start: at('21:00'), end: at('22:00') },
    ]
    expect(mergePrograms(a, b).map((p) => p.id)).toEqual(['1-1', '1-2'])
  })

  it('moves with the arrows: along the row, and up/down at the same time', () => {
    const rows = [
      [{ start: at('20:00'), end: at('21:00') }, { start: at('21:00'), end: at('23:00') }],
      [],
      [{ start: at('20:00'), end: at('20:30') }, { start: at('20:30'), end: at('21:30') }, { start: at('21:30'), end: at('22:00') }],
    ]
    expect(gridMove(rows, 0, 0, 'right', ms('20:00'))).toEqual([0, 1])
    expect(gridMove(rows, 0, 0, 'left', ms('20:00'))).toBeNull()
    // the empty row is skipped; 21:40 is in the third programme below
    expect(gridMove(rows, 0, 1, 'down', ms('21:40'))).toEqual([2, 2])
    expect(gridMove(rows, 2, 1, 'up', ms('20:45'))).toEqual([0, 0])
    expect(gridMove(rows, 2, 0, 'down', ms('20:00'))).toBeNull()
  })
})

describe('audio and subtitles', () => {
  const audio: ChannelTrack[] = [
    { slot: 'ru', name: 'Русский', label: 'Русский — LostFilm', lang: 'ru' },
    { slot: 'ru2', name: 'Русский 2', label: 'Русский — Кубик', lang: 'ru' },
    { slot: 'en', name: 'English', label: 'English', lang: 'en' },
    { slot: 'orig', name: 'Оригинал', label: 'Оригинал (fr)', lang: 'fr' },
  ]
  const subs: ChannelTrack[] = [
    { slot: 'ru', name: 'Русские', label: 'Русские', lang: 'ru' },
    { slot: 'en', name: 'English', label: 'English', lang: 'en' },
  ]
  const prefs = (audioLangs: string[], subMode: 'off' | 'always' | 'foreign', subLangs: string[] = []) => ({ audioLangs, subMode, subLangs }) as never

  it('picks the first of the profile’s languages the programme has, else «ru», else the first', () => {
    expect(pickAudioSlot(audio, prefs(['en', 'ru'], 'off'), {})).toBe('en')
    expect(pickAudioSlot(audio, prefs(['de'], 'off'), {})).toBe('ru')
    expect(pickAudioSlot([audio[2]!], prefs(['de'], 'off'), {})).toBe('en')
    expect(pickAudioSlot(audio, prefs(['fr'], 'off'), {})).toBe('orig')
    expect(pickAudioSlot(undefined, prefs(['en'], 'off'), {})).toBeUndefined()
  })

  it('subtitles by the profile’s mode', () => {
    expect(pickSubSlot(subs, prefs(['ru'], 'off'), audio[0], {})).toBeNull()
    expect(pickSubSlot(subs, prefs(['ru'], 'always', ['en']), audio[0], {})).toBe('en')
    expect(pickSubSlot(subs, prefs(['ru', 'en'], 'always'), audio[0], {})).toBe('ru')
    // foreign: only when the voice is not one of the profile's languages
    expect(pickSubSlot(subs, prefs(['ru'], 'foreign'), audio[0], {})).toBeNull()
    expect(pickSubSlot(subs, prefs(['ru'], 'foreign'), audio[3], {})).toBe('ru')
  })

  it('the viewer’s choice wins and sticks; a slot the programme lacks plays its «ru»', () => {
    expect(pickAudioSlot(audio, prefs(['en'], 'off'), { audio: 'ru2' })).toBe('ru2')
    expect(pickSubSlot(subs, prefs(['ru'], 'always'), audio[0], { subs: null })).toBeNull()
    expect(pickSubSlot(subs, prefs(['ru'], 'off'), audio[0], { subs: 'en' })).toBe('en')
    expect(playingAudio([audio[0]!, audio[2]!], 'ru2')?.slot).toBe('ru')
  })

  it('stays on the chosen slot when a programme lacks it (no switch at the boundary)', () => {
    const session = { audio: 'ru2', audioName: 'Русский 2', subs: 'ru', subsName: 'Русские' }
    // a programme with only English and the original
    const a = [audio[2]!, audio[3]!]
    const s = [subs[1]!]
    expect(renditionsToPlay(a, s, 'ru2', 'ru', session)).toEqual({ audio: 'Русский 2', subs: 'Русские' })
    expect(renditionsToPlay(audio, subs, 'ru2', 'ru', session)).toEqual({ audio: 'Русский 2', subs: 'Русские' })
    // no choice: the profile's default, subtitles off
    expect(renditionsToPlay(audio, subs, 'en', null, {})).toEqual({ audio: 'English', subs: null })
    // an older server: nothing known, nothing touched
    expect(renditionsToPlay(undefined, undefined, undefined, null, {})).toEqual({ audio: undefined, subs: undefined })
  })

  it('selects the rendition by its HLS NAME', () => {
    expect(renditionIndex(['Русский', 'Русский 2', 'English'], 'Русский 2')).toBe(1)
    expect(renditionIndex(['Русский'], 'English')).toBe(-1)
  })
})

describe('the rows', () => {
  it('says what a channel plays', () => {
    expect(rulesSummary({ kinds: ['movie'], genres: ['комедия'], yearFrom: 1990, maxRating: '12+' })).toBe('Фильмы · комедия · 1990-е · до 12+ · Перемешать')
    expect(rulesSummary({ titles: [1, 2, 3], order: 'ordered' })).toBe('3 названия · По порядку')
  })

  it('a row: number, still, name, what is on, a thin bar — and the star turns it', () => {
    const now = ms('12:35')
    const c = ch(4, 7, 'Кино', {
      now: { itemId: 9, title: 'Фильм', subtitle: 'S1 E2', start: at('12:00'), end: at('13:00') },
      next: { itemId: 10, title: 'Другой', start: at('13:00'), end: at('14:00') },
    })
    const onFavorite = vi.fn()
    render(
      <MemoryRouter>
        <ul>
          <ChannelRow channel={c} now={now} to="/tv/4?f=fav" last onFavorite={onFavorite} />
        </ul>
      </MemoryRouter>,
    )
    expect(screen.getByRole('link').getAttribute('href')).toBe('/tv/4?f=fav')
    expect(screen.getByText('7')).toBeTruthy()
    expect(screen.getByText('Канал 4')).toBeTruthy()
    expect(screen.getByText('Фильм · S1 E2')).toBeTruthy()
    // a TV row: no «осталось…» and no «Далее…» (they are in the pane, the guide and the player)
    expect(screen.queryByText(/осталось|Далее|до \d/)).toBeNull()
    expect(document.querySelectorAll('[aria-hidden="true"][data-tone]').length).toBe(1)
    expect(screen.getByText('Последний')).toBeTruthy()
    // no gradient tile: no channel name drawn on the picture
    expect(document.querySelector('[style*="grad"]')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'В избранное' }))
    expect(onFavorite).toHaveBeenCalledWith(true)
  })

  it('a home card shows the programme still and its progress, never a logo tile', () => {
    const now = ms('12:30')
    const c = ch(4, 12, 'Кино', { now: { itemId: 9, title: 'Фильм', subtitle: 'S1 E2', start: at('12:00'), end: at('13:00') } })
    render(
      <MemoryRouter>
        <ChannelCard channel={c} now={now} />
      </MemoryRouter>,
    )
    expect(screen.getByRole('link').getAttribute('href')).toBe('/tv/4')
    expect(screen.getByText('12 · Канал 4')).toBeTruthy()
    expect(screen.getByText('Фильм · S1 E2')).toBeTruthy()
    expect(screen.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('50')
    expect(document.querySelector('[style*="grad"]')).toBeNull()
  })
})

describe('clock', () => {
  it('rounds schedule times with seconds to the nearest minute', async () => {
    const { clock } = await import('../useChannels')
    const local = (h: number, m: number, s: number) => new Date(2026, 10, 10, h, m, s).getTime()
    expect(clock(local(21, 39, 50))).toBe(clock(local(21, 40, 0)))
    expect(clock(local(21, 40, 29))).toBe(clock(local(21, 40, 0)))
  })
})
