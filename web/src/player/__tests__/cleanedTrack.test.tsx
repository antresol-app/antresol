import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { PlaybackPrefs, Track } from '../../api/types'
import { setLanguagePref } from '../../i18n'
import { TrackMenu } from '../TrackMenu'
import { choiceOf, pickAudio } from '../trackPrefs'

// the original, LostFilm in the file, and LostFilm with the original speech subtracted (voclean)
const audio: Track[] = [
  { index: 1, codec: 'eac3', lang: 'eng', title: '', channels: 6, default: true },
  { index: 2, codec: 'eac3', lang: 'rus', title: 'LostFilm', channels: 6, default: false },
  {
    index: 2000, codec: 'eac3', lang: 'rus', title: 'LostFilm', channels: 6, default: false, external: true, src: 'c1e4',
    studio: 'LostFilm', kind: 'cleaned', method: 'subtraction', cleanedFrom: 2,
  },
]
const prefs = (p: Partial<PlaybackPrefs> = {}): PlaybackPrefs => ({
  audioLangs: ['ru', 'en'], subMode: 'off', subLangs: [], subSize: 'm', subBackground: 'translucent', custom: true, ...p,
})

const openMenu = async (audioIndex = 2, onAudio = vi.fn()) => {
  render(<TrackMenu audio={audio} audioIndex={audioIndex} subtitles={[]} subIndex={-1} onAudio={onAudio} onSubtitle={vi.fn()} />)
  await userEvent.click(screen.getByRole('button', { name: /Аудио и субтитры|Audio & Subtitles/i }))
  return screen.findByRole('menu')
}

describe('«Очищенный перевод» in the track menu', () => {
  afterEach(() => act(() => setLanguagePref('ru')))

  it('the cleaned version is a switch under the list, not a row of its own', async () => {
    const onAudio = vi.fn()
    const menu = await openMenu(2, onAudio)
    const group = within(menu).getByRole('group', { name: 'Аудио' })
    const rows = within(group).getAllByRole('menuitemradio')
    expect(rows.map((el) => el.textContent)).toEqual([expect.stringMatching(/^Английский/), expect.stringMatching(/^Русский.*LostFilm/)])
    expect(within(group).queryByText('Очищено')).toBeNull()
    const sw = within(group).getByRole('menuitemcheckbox', { name: /Очищенный перевод/ })
    expect(sw).toHaveAttribute('aria-checked', 'false')
    expect(sw).toHaveTextContent('Без оригинальной речи под переводом')
    await userEvent.click(sw)
    expect(onAudio).toHaveBeenCalledWith(2000)
  })

  it('on: the studio stays the chosen row; the switch turns it back off', async () => {
    const onAudio = vi.fn()
    const menu = await openMenu(2000, onAudio)
    expect(within(menu).getByRole('menuitemradio', { name: /^Русский/, checked: true })).toHaveTextContent('LostFilm')
    const sw = within(menu).getByRole('menuitemcheckbox', { name: /Очищенный перевод/ })
    expect(sw).toHaveAttribute('aria-checked', 'true')
    await userEvent.click(sw)
    expect(onAudio).toHaveBeenCalledWith(2)
  })

  it('no switch for a voice-over without a cleaned version', async () => {
    const menu = await openMenu(1)
    expect(within(menu).queryByRole('menuitemcheckbox', { name: /Очищенный перевод/ })).toBeNull()
  })

  it('says “Clean voice-over” in English', async () => {
    act(() => setLanguagePref('en'))
    const menu = await openMenu()
    expect(within(menu).getByRole('menuitemcheckbox', { name: /Clean voice-over/ })).toBeInTheDocument()
  })
})

describe('a cleaned track without its source', () => {
  it('stays a row with the «Очищено» capsule', async () => {
    const tracks: Track[] = [audio[0]!, { ...audio[2]!, cleanedFrom: 9 }]
    render(<TrackMenu audio={tracks} audioIndex={1} subtitles={[]} subIndex={-1} onAudio={vi.fn()} onSubtitle={vi.fn()} />)
    await userEvent.click(screen.getByRole('button', { name: /Аудио и субтитры|Audio & Subtitles/i }))
    const menu = await screen.findByRole('menu')
    expect(within(menu).getByRole('menuitemradio', { name: 'Русский, LostFilm, очищено от оригинальной речи, 5.1' })).toBeInTheDocument()
  })
})

describe('pickAudio with «Выбирать очищенную озвучку»', () => {
  it('takes the cleaned version of the track it would have chosen', () => {
    expect(pickAudio(audio, { prefs: prefs() })).toBe(2)
    expect(pickAudio(audio, { prefs: prefs({ preferCleaned: true }) })).toBe(2000)
  })

  it('the series voice «LostFilm» becomes the cleaned LostFilm', () => {
    const series = { lang: 'rus', title: 'LostFilm' }
    expect(pickAudio(audio, { series, prefs: prefs({ audioLangs: ['en'] }) })).toBe(2)
    expect(pickAudio(audio, { series, prefs: prefs({ audioLangs: ['en'], preferCleaned: true }) })).toBe(2000)
  })

  it('a cleaned voice picked by hand is picked again in the next episode, the switch off', () => {
    const series = { lang: 'rus', title: 'LostFilm', kind: 'cleaned' as const }
    expect(pickAudio(audio, { series, prefs: prefs({ audioLangs: ['en'] }) })).toBe(2000)
    // an episode without the cleaned version: the studio's track
    expect(pickAudio(audio.slice(0, 2), { series, prefs: prefs({ audioLangs: ['en'] }) })).toBe(2)
    expect(choiceOf(audio[2]!)).toEqual({ lang: 'rus', title: 'LostFilm', kind: 'cleaned' })
    expect(choiceOf(audio[1]!)).toEqual({ lang: 'rus', title: 'LostFilm' })
  })

  it('a track with no cleaned version stays', () => {
    expect(pickAudio(audio, { prefs: prefs({ audioLangs: ['en'], preferCleaned: true }) })).toBeUndefined()
  })
})
