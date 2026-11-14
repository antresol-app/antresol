import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ChannelTrack } from '../../../api/channels'
import { LiveTrackMenu } from '../LiveTrackMenu'

const audio: ChannelTrack[] = [
  { name: 'LostFilm', label: 'Русский — LostFilm', lang: 'ru' },
  { name: 'LostFilm (очищено)', label: 'Русский — LostFilm (очищено)', lang: 'ru', cleanedOf: 'LostFilm' },
  { name: 'Оригинал (English)', label: 'Оригинал (English)', lang: 'en' },
]

const open = async (audioName: string, onAudio = vi.fn()) => {
  render(<LiveTrackMenu audio={audio} subtitles={[]} audioName={audioName} subsName={null} onAudio={onAudio} onSubtitles={vi.fn()} />)
  await userEvent.click(screen.getByRole('button', { name: 'Звук и субтитры' }))
  return { menu: await screen.findByRole('menu'), onAudio }
}

describe('live TV: «Очищенный перевод»', () => {
  it('the cleaned rendition is a switch on its voice, not a row', async () => {
    const { menu, onAudio } = await open('LostFilm')
    const group = within(menu).getByRole('group', { name: 'Аудио' })
    expect(within(group).getAllByRole('menuitemradio').map((x) => x.textContent)).toEqual(['Русский — LostFilm', 'Оригинал (English)'])
    await userEvent.click(within(group).getByRole('menuitemcheckbox', { name: /Очищенный перевод/ }))
    expect(onAudio).toHaveBeenCalledWith(audio[1])
  })

  it('on: the voice stays chosen, the switch turns it off', async () => {
    const { menu, onAudio } = await open('LostFilm (очищено)')
    expect(within(menu).getByRole('menuitemradio', { name: 'Русский — LostFilm', checked: true })).toBeInTheDocument()
    await userEvent.click(within(menu).getByRole('menuitemcheckbox', { name: /Очищенный перевод/ }))
    expect(onAudio).toHaveBeenCalledWith(audio[0])
  })

  it('no switch on the original', async () => {
    const { menu } = await open('Оригинал (English)')
    expect(within(menu).queryByRole('menuitemcheckbox')).toBeNull()
  })
})
