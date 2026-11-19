import { afterEach, describe, expect, it, vi } from 'vitest'
import { canFullscreen, toggleFullscreen, type FsVideo } from '../liveFullscreen'

const setDoc = (o: Record<string, unknown>) => {
  for (const [k, v] of Object.entries(o)) Object.defineProperty(document, k, { value: v, configurable: true, writable: true })
}

describe('live player full screen', () => {
  afterEach(() => setDoc({ fullscreenEnabled: false, webkitFullscreenEnabled: undefined, fullscreenElement: null, webkitFullscreenElement: undefined }))

  it('puts the player element in full screen where the browser can (a desktop)', () => {
    setDoc({ fullscreenEnabled: true, fullscreenElement: null })
    const root = document.createElement('div')
    root.requestFullscreen = vi.fn(() => Promise.resolve())
    const video = document.createElement('video') as FsVideo
    video.webkitEnterFullscreen = vi.fn()
    expect(canFullscreen(video)).toBe(true)
    expect(toggleFullscreen(root, video)).toBe('element')
    expect(root.requestFullscreen).toHaveBeenCalled()
    expect(video.webkitEnterFullscreen).not.toHaveBeenCalled()
  })

  it('falls back to the video’s own full screen on an iPhone’s Safari (no element full screen)', () => {
    setDoc({ fullscreenEnabled: undefined, webkitFullscreenEnabled: false, fullscreenElement: undefined })
    const root = document.createElement('div')
    const video = document.createElement('video') as FsVideo
    video.webkitEnterFullscreen = vi.fn()
    expect(canFullscreen(video)).toBe(true)
    expect(toggleFullscreen(root, video)).toBe('video')
    expect(video.webkitEnterFullscreen).toHaveBeenCalledOnce()
    // in it: the same button leaves it
    video.webkitDisplayingFullscreen = true
    video.webkitExitFullscreen = vi.fn()
    expect(toggleFullscreen(root, video)).toBe('exit')
    expect(video.webkitExitFullscreen).toHaveBeenCalledOnce()
  })

  it('a refused video full screen (no frame yet) is no full screen, not an error', () => {
    setDoc({ fullscreenEnabled: false })
    const video = document.createElement('video') as FsVideo
    video.webkitEnterFullscreen = () => {
      throw new DOMException('InvalidStateError')
    }
    expect(toggleFullscreen(document.createElement('div'), video)).toBeNull()
  })

  it('no full screen at all: no button', () => {
    setDoc({ fullscreenEnabled: false })
    expect(canFullscreen(document.createElement('video'))).toBe(false)
  })

  it('leaves the element full screen', () => {
    const root = document.createElement('div')
    setDoc({ fullscreenEnabled: true, fullscreenElement: root, exitFullscreen: vi.fn(() => Promise.resolve()) })
    expect(toggleFullscreen(root, null)).toBe('exit')
    expect(document.exitFullscreen).toHaveBeenCalled()
  })
})
