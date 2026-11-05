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

const openMenu = async () => {
  render(<TrackMenu audio={audio} audioIndex={2} subtitles={[]} subIndex={-1} onAudio={vi.fn()} onSubtitle={vi.fn()} />)
  await userEvent.click(screen.getByRole('button', { name: /Аудио и субтитры|Audio & Subtitles/i }))
  return screen.findByRole('menu')
}

describe('a cleaned voice-over in the track menu', () => {
  afterEach(() => act(() => setLanguagePref('ru')))

  it('is «Русский · LostFilm» with the capsule «Очищено», named for VoiceOver', async () => {
    const menu = await openMenu()
    const cleaned = within(menu).getByRole('menuitemradio', { name: 'Русский, LostFilm, очищено от оригинальной речи, 5.1' })
    expect(cleaned).toHaveTextContent('Русский')
    expect(cleaned).toHaveTextContent('LostFilm · 5.1')
    const badge = within(cleaned).getByText('Очищено')
    expect(badge).toHaveAttribute('aria-hidden', 'true')
    expect(badge).toHaveAttribute('title', 'Без оригинальной речи под закадровым переводом · способ: вычитание')
    // the studio's own track stays, without the badge
    const studio = within(menu).getByRole('menuitemradio', { name: /^Русский/, checked: true })
    expect(studio).not.toHaveTextContent('Очищено')
    expect(studio).toHaveTextContent('LostFilm · 5.1')
  })

  it('says “Cleaned” in English', async () => {
    act(() => setLanguagePref('en'))
    const menu = await openMenu()
    const cleaned = within(menu).getByRole('menuitemradio', { name: 'Russian, LostFilm, cleaned of the original speech, 5.1' })
    expect(within(cleaned).getByText('Cleaned')).toBeInTheDocument()
  })
})

describe('the order of the audio menu', () => {
  it('lists a cleaned track right under its studio track', async () => {
    const tracks: Track[] = [
      { index: 1, codec: 'eac3', lang: 'rus', title: 'LostFilm', channels: 6, default: true },
      { index: 2, codec: 'eac3', lang: 'eng', title: '', channels: 6, default: false },
      { ...audio[2]!, cleanedFrom: 1 },
    ]
    render(<TrackMenu audio={tracks} audioIndex={1} subtitles={[]} subIndex={-1} onAudio={vi.fn()} onSubtitle={vi.fn()} />)
    await userEvent.click(screen.getByRole('button', { name: /Аудио и субтитры|Audio & Subtitles/i }))
    const menu = await screen.findByRole('menu')
    const group = within(menu).getByRole('group', { name: 'Аудио' })
    const items = within(group).getAllByRole('menuitemradio')
    expect(items.map((el) => el.getAttribute('aria-label') ?? el.textContent)).toEqual([
      expect.stringMatching(/^Русский/),
      'Русский, LostFilm, очищено от оригинальной речи, 5.1',
      expect.stringMatching(/^Английский/),
    ])
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
