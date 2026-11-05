import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { MiniPlayer } from '../MiniPlayer'
import { audio, resetAudioForTests, type QueueItem } from '../audio/engine'

const item: QueueItem = {
  key: 't1',
  trackId: 1,
  title: 'Очень длинное название песни, которое не помещается в капсулу',
  artist: 'Тестовый артист',
  album: 'Первый альбом',
  cover: '/api/images/local/2/poster',
  durationSec: 20,
  src: '/api/tracks/1/stream?accept=flac',
}

beforeEach(() => {
  resetAudioForTests()
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
})
afterEach(() => resetAudioForTests())

describe('MiniPlayer', () => {
  it('hidden with empty queue', () => {
    render(<MiniPlayer />)
    expect(screen.queryByRole('complementary', { name: 'Сейчас играет' })).not.toBeInTheDocument()
  })

  it('shows title and artist', () => {
    render(<MiniPlayer />)
    act(() => audio.play([item], 0))
    const bar = screen.getByRole('complementary', { name: 'Сейчас играет' })
    expect(bar).toHaveTextContent(item.title)
    expect(bar).toHaveTextContent('Тестовый артист')
    expect(screen.getByRole('button', { name: 'Предыдущий трек' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Следующий трек' })).toBeInTheDocument()
  })

  it('play/pause button toggles aria-label Пауза/Играть', () => {
    render(<MiniPlayer />)
    act(() => audio.play([item], 0))
    const btn = screen.getByRole('button', { name: 'Пауза' })
    fireEvent.click(btn)
    expect(audio.get().playing).toBe(false)
    expect(screen.getByRole('button', { name: 'Играть' })).toBe(btn)
    fireEvent.click(btn)
    expect(screen.getByRole('button', { name: 'Пауза' })).toBe(btn)
  })
})
