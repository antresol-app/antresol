import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import { MemoryRouter } from 'react-router'
import { MiniPlayer } from '../MiniPlayer'
import { NowPlaying } from '../NowPlaying'
import { audio, resetAudioForTests, type QueueItem } from '../audio/engine'

const item = (n: number, over: Partial<QueueItem> = {}): QueueItem => ({
  key: `t${n}`,
  trackId: n,
  title: `Трек ${n}`,
  artist: 'Тестовый артист',
  album: 'Первый альбом',
  cover: '/api/images/local/1/poster',
  durationSec: 20,
  src: `/api/tracks/${n}/stream?accept=flac`,
  ...over,
})

function Harness() {
  const [open, setOpen] = useState(false)
  return (
    <MemoryRouter>
      <MiniPlayer onExpand={() => setOpen(true)} />
      <NowPlaying open={open} onClose={() => setOpen(false)} />
    </MemoryRouter>
  )
}

/** Safari's event: `availability` on a plain Event. */
function availability(el: HTMLMediaElement, value: 'available' | 'not-available') {
  const e = new Event('webkitplaybacktargetavailabilitychanged') as Event & { availability?: string }
  e.availability = value
  act(() => {
    el.dispatchEvent(e)
  })
}

/** Makes the page look like Safari (the event class exists). */
const safari = () => vi.stubGlobal('WebKitPlaybackTargetAvailabilityEvent', class {})

beforeEach(() => {
  resetAudioForTests()
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ error: 'Текста нет' }), { status: 404 })))
})
afterEach(() => {
  vi.unstubAllGlobals()
  resetAudioForTests()
  delete (window as unknown as Record<string, unknown>).WebKitPlaybackTargetAvailabilityEvent
})

async function openPlayer() {
  fireEvent.click(screen.getByRole('button', { name: /Открыть плеер/ }))
  await screen.findByRole('dialog', { name: 'Сейчас играет' })
}

describe('AirPlay', () => {
  it('noAirPlayWithoutSafariEvent', async () => {
    render(<Harness />)
    act(() => audio.play([item(1), item(2)], 0))
    await openPlayer()
    availability(audio.element()!, 'available')
    expect(screen.queryByRole('button', { name: 'AirPlay' })).not.toBeInTheDocument()
  })

  it('musicAirPlayWhenAvailable', async () => {
    safari()
    render(<Harness />)
    act(() => audio.play([item(1), item(2)], 0))
    await openPlayer()
    const el = audio.element()!
    const picker = vi.fn()
    ;(el as HTMLAudioElement & { webkitShowPlaybackTargetPicker?: () => void }).webkitShowPlaybackTargetPicker = picker
    availability(el, 'available')
    fireEvent.click(await screen.findByRole('button', { name: 'AirPlay' }))
    expect(picker).toHaveBeenCalledTimes(1)
  })

  it('audiobookAirPlayWhenAvailable', async () => {
    safari()
    render(<Harness />)
    act(() => audio.play([item(1, { title: 'Глава 1' })], 0, { kind: 'audiobook', bookId: 7 }))
    await openPlayer()
    const el = audio.element()!
    const picker = vi.fn()
    ;(el as HTMLAudioElement & { webkitShowPlaybackTargetPicker?: () => void }).webkitShowPlaybackTargetPicker = picker
    availability(el, 'available')
    fireEvent.click(await screen.findByRole('button', { name: 'AirPlay' }))
    expect(picker).toHaveBeenCalledTimes(1)
  })

  it('notAvailableHidesButton', async () => {
    safari()
    render(<Harness />)
    act(() => audio.play([item(1), item(2)], 0))
    await openPlayer()
    const el = audio.element()!
    availability(el, 'available')
    expect(await screen.findByRole('button', { name: 'AirPlay' })).toBeInTheDocument()
    availability(el, 'not-available')
    await waitFor(() => expect(screen.queryByRole('button', { name: 'AirPlay' })).not.toBeInTheDocument())
  })
})
