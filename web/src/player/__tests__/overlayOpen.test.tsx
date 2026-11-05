import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import type { MediaFile } from '../../api/types'

vi.mock('hls.js', () => {
  class FakeHls {
    static Events = { ERROR: 'error' }
    static ErrorTypes = { NETWORK_ERROR: 'networkError' }
    static isSupported = () => true
    on() {}
    loadSource() {}
    attachMedia() {}
    destroy() {}
  }
  return { default: FakeHls }
})

import { VideoPlayer } from '../VideoPlayer'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

function player(overlayOpen: boolean) {
  const start = vi.fn(async () => ({ mode: 'direct', url: '/api/stream/5', startSec: 0, durationMs: 3_600_000, audioIndex: 0, audio: [], subtitles: [] }))
  render(
    <VideoPlayer
      itemId={9}
      title="Дюна"
      parts={[{ id: 5, name: 'dune.mkv', durationMs: 3_600_000 } as MediaFile]}
      startSec={0}
      onClose={() => {}}
      start={start as never}
      overlayOpen={overlayOpen}
    />,
  )
}

/** Plays, then a touch tap on the picture, which hides the controls. */
function playAndTapAway() {
  const root = screen.getByTestId('player')
  act(() => {
    fireEvent.play(root.querySelector('video')!)
  })
  const surface = root.querySelector('.surface')!
  fireEvent.pointerDown(surface, { pointerType: 'touch' })
  fireEvent.click(surface)
  return root
}

// A sheet over the player (the online picker) keeps the controls, and so
// the cursor, visible: the idle state would hide the cursor over the sheet.
describe('VideoPlayer: an overlay keeps the controls', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => json({})))
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('hides them while playing without an overlay', () => {
    player(false)
    expect(playAndTapAway()).toHaveClass('hidden')
  })

  it('keeps them while an overlay is open', () => {
    player(true)
    expect(playAndTapAway()).toHaveClass('shown')
  })
})
