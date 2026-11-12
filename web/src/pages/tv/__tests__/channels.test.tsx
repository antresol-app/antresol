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
  chooseAudioIn,
  chooseSubsIn,
  cleanSession,
  pickAudio,
  pickSubs,
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
  // «Много голосов»: 8 voices and 6 subtitle languages, names as the server makes them, in no fixed order
  const tr = (name: string, lang: string, label = name): ChannelTrack => ({ name, label, lang })
  const audio: ChannelTrack[] = [
    tr('Дубляж', 'ru', 'Русский — Дубляж'),
    tr('LostFilm', 'ru', 'Русский — LostFilm'),
    tr('Кураж-Бамбей', 'ru'),
    tr('LostFilm (очищено)', 'ru'),
    tr('Оригинал (English)', 'en'),
    tr('Français', 'fr'),
    tr('Deutsch', 'de'),
    tr('Комментарий', 'ru'),
  ]
  const subs: ChannelTrack[] = [tr('Русские', 'ru'), tr('Русские 2', 'ru'), tr('English', 'en'), tr('Français', 'fr'), tr('Deutsch', 'de'), tr('Español', 'es')]
  const a = (name: string) => audio.find((x) => x.name === name)!
  const s = (name: string) => subs.find((x) => x.name === name)!
  const prefs = (audioLangs: string[], subMode: 'off' | 'always' | 'foreign', subLangs: string[] = []) => ({ audioLangs, subMode, subLangs }) as never
  const master = { audio: audio.map((x) => ({ name: x.name, lang: x.lang })), subs: subs.map((x) => ({ name: x.name, lang: x.lang })) }

  it('defaults to the first of the profile’s languages present, else Russian, else the first entry', () => {
    expect(pickAudio(audio, prefs(['en', 'ru'], 'off'), {})?.name).toBe('Оригинал (English)')
    expect(pickAudio(audio, prefs(['ja', 'de'], 'off'), {})?.name).toBe('Deutsch')
    expect(pickAudio(audio, prefs(['ja'], 'off'), {})?.name).toBe('Дубляж')
    // the order is the server's: the first Russian is whatever stands first
    expect(pickAudio([a('Deutsch'), a('LostFilm'), a('Дубляж')], prefs([], 'off'), {})?.name).toBe('LostFilm')
    expect(pickAudio([a('Français'), a('Deutsch')], null, {})?.name).toBe('Français')
    expect(pickAudio(undefined, prefs(['en'], 'off'), {})).toBeUndefined()
    // a region tag matches its language
    expect(pickAudio([tr('English (US)', 'en-US'), a('Дубляж')], prefs(['en'], 'off'), {})?.name).toBe('English (US)')
  })

  it('the choice sticks by name; a programme without it falls back to its language', () => {
    const session = chooseAudioIn({}, a('Кураж-Бамбей'))
    expect(session).toEqual({ audioName: 'Кураж-Бамбей', audioLang: 'ru' })
    expect(pickAudio(audio, prefs(['en'], 'off'), session)?.name).toBe('Кураж-Бамбей')
    // the next film has only «Дубляж» and the original: the Russian one, not the profile's English
    expect(pickAudio([a('Оригинал (English)'), a('Дубляж')], prefs(['en'], 'off'), session)?.name).toBe('Дубляж')
    // no track in that language at all: the profile's default
    expect(pickAudio([a('Оригинал (English)'), a('Français')], prefs(['fr'], 'off'), session)?.name).toBe('Français')
  })

  it('subtitles: the profile’s mode, the viewer’s choice by name, then by language', () => {
    expect(pickSubs(subs, prefs(['ru'], 'off'), a('Дубляж'), {})).toBeNull()
    expect(pickSubs(subs, prefs(['ru'], 'always', ['es']), a('Дубляж'), {})?.name).toBe('Español')
    expect(pickSubs(subs, prefs(['ru', 'en'], 'always'), a('Дубляж'), {})?.name).toBe('Русские')
    expect(pickSubs(subs, prefs(['ru'], 'foreign'), a('Дубляж'), {})).toBeNull()
    expect(pickSubs(subs, prefs(['ru'], 'foreign'), a('Français'), {})?.name).toBe('Русские')
    const chosen = chooseSubsIn({}, s('Русские 2'))
    expect(pickSubs(subs, prefs(['ru'], 'off'), a('Дубляж'), chosen)?.name).toBe('Русские 2')
    expect(pickSubs([s('English'), s('Русские')], prefs(['ru'], 'off'), a('Дубляж'), chosen)?.name).toBe('Русские')
    expect(pickSubs([s('English')], prefs(['ru'], 'always'), a('Дубляж'), chosen)).toBeNull()
    const off = chooseSubsIn(chosen, null)
    expect(off).toEqual({ subsName: null })
    expect(pickSubs(subs, prefs(['ru'], 'always'), a('Дубляж'), off)).toBeNull()
  })

  it('plays the chosen rendition while the master has it, even through a programme that lacks it', () => {
    const session = { ...chooseAudioIn({}, a('Кураж-Бамбей')), ...chooseSubsIn({}, s('Deutsch')) }
    // the next film has only «Дубляж» and English subtitles: no switch at the boundary
    const progA = [a('Дубляж')]
    const progS = [s('English')]
    const audioNow = pickAudio(progA, prefs(['ru'], 'off'), session)
    const subsNow = pickSubs(progS, prefs(['ru'], 'off'), audioNow, session)
    expect(renditionsToPlay(audioNow, subsNow, true, master, session)).toEqual({ audio: 'Кураж-Бамбей', subs: 'Deutsch' })
    // zapped to a channel whose master has neither name: the programme's entries, by language
    const other = { audio: [{ name: 'Дубляж', lang: 'ru' }, { name: 'English', lang: 'en' }], subs: [{ name: 'English', lang: 'en' }] }
    expect(renditionsToPlay(audioNow, subsNow, true, other, session)).toEqual({ audio: 'Дубляж', subs: null })
    // no choice: the profile's default; subtitles off
    const def = pickAudio(audio, prefs(['en'], 'off'), {})
    expect(renditionsToPlay(def, null, true, master, {})).toEqual({ audio: 'Оригинал (English)', subs: null })
    // an older server before the player listed anything: nothing touched
    expect(renditionsToPlay(undefined, null, false, { audio: [], subs: [] }, {})).toEqual({ audio: undefined, subs: undefined })
  })

  it('reads a stored session safely (round 3’s slots are dropped)', () => {
    expect(cleanSession({ audio: 'ru2', audioName: 'Русский 2', subs: 'en' })).toEqual({ audioName: 'Русский 2' })
    expect(cleanSession({ subsName: null })).toEqual({ subsName: null })
    expect(cleanSession('x')).toEqual({})
  })

  it('selects the rendition by its HLS NAME', () => {
    expect(renditionIndex(master.audio.map((x) => x.name), 'LostFilm (очищено)')).toBe(3)
    expect(renditionIndex(['Дубляж'], 'English')).toBe(-1)
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

  it('a logo beside the name; a row without one keeps the slot when the list has logos', () => {
    const now = ms('12:35')
    render(
      <MemoryRouter>
        <ul>
          <ChannelRow channel={ch(1, 1, 'Кино', { logoUrl: '/api/channels/1/logo.png?v=2' })} now={now} to="/tv/1" logoSlot onFavorite={() => {}} />
          <ChannelRow channel={ch(2, 2, 'Кино')} now={now} to="/tv/2" logoSlot onFavorite={() => {}} />
          <ChannelRow channel={ch(3, 3, 'Кино')} now={now} to="/tv/3" onFavorite={() => {}} />
        </ul>
      </MemoryRouter>,
    )
    expect(document.querySelector('[data-channel-logo] img')?.getAttribute('src')).toBe('/api/channels/1/logo.png?v=2')
    expect(document.querySelectorAll('[data-logo-slot]')).toHaveLength(1)
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
