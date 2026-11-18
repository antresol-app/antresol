// The players' remembered sound: restored from this device, written on every
// change, applied to the <video>; a browser-forced mute is not remembered.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useRef } from 'react'
import { useVolume } from '../useVolume'

beforeEach(() => localStorage.clear())

/** The hook over a real <video>, as the players use it. */
function player() {
  const video = document.createElement('video')
  const hook = renderHook(() => {
    const ref = useRef<HTMLVideoElement | null>(video)
    return useVolume(ref)
  })
  return { video, ...hook }
}

describe('useVolume', () => {
  it('starts at full volume, un-muted, with nothing remembered', () => {
    const { result, video } = player()
    expect(result.current.volume).toBe(1)
    expect(result.current.muted).toBe(false)
    expect(video.volume).toBe(1)
    expect(video.muted).toBe(false)
  })

  it('restores the remembered level and mute and puts them on the video', () => {
    localStorage.setItem('vault.player.volume', '0.4')
    localStorage.setItem('vault.player.muted', '1')
    const { result, video } = player()
    expect(result.current.volume).toBe(0.4)
    expect(result.current.muted).toBe(true)
    expect(video.volume).toBeCloseTo(0.4)
    expect(video.muted).toBe(true)
  })

  it('ignores nonsense in storage (and a remembered 0: a player must not open silent)', () => {
    localStorage.setItem('vault.player.volume', '0')
    expect(player().result.current.volume).toBe(1)
    localStorage.setItem('vault.player.volume', 'loud')
    expect(player().result.current.volume).toBe(1)
    localStorage.setItem('vault.player.volume', '7')
    expect(player().result.current.volume).toBe(1)
  })

  it('a change is clamped, applied, remembered, and un-mutes', () => {
    localStorage.setItem('vault.player.muted', '1')
    const { result, video } = player()
    act(() => result.current.changeVolume(0.65))
    expect(video.volume).toBeCloseTo(0.65)
    expect(video.muted).toBe(false)
    expect(localStorage.getItem('vault.player.volume')).toBe('0.65')
    expect(localStorage.getItem('vault.player.muted')).toBe('0')
    act(() => result.current.changeVolume(3))
    expect(result.current.volume).toBe(1)
    act(() => result.current.changeVolume(-1))
    expect(result.current.volume).toBe(0)
    expect(localStorage.getItem('vault.player.volume')).toBe('0')
  })

  it('the mute toggles, is applied and remembered', () => {
    const { result, video } = player()
    act(() => result.current.toggleMute())
    expect(video.muted).toBe(true)
    expect(localStorage.getItem('vault.player.muted')).toBe('1')
    act(() => result.current.toggleMute())
    expect(video.muted).toBe(false)
    expect(localStorage.getItem('vault.player.muted')).toBe('0')
  })

  it('a mute the browser forced (no sound before a tap) is for now only', () => {
    const { result, video } = player()
    act(() => result.current.muteForNow())
    expect(video.muted).toBe(true)
    expect(localStorage.getItem('vault.player.muted')).toBeNull()
    // the next start is not muted
    expect(player().result.current.muted).toBe(false)
  })

  it('works without storage (private mode)', () => {
    const get = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    const set = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied')
    })
    const { result, video } = player()
    expect(result.current.volume).toBe(1)
    act(() => result.current.changeVolume(0.3))
    expect(video.volume).toBeCloseTo(0.3)
    get.mockRestore()
    set.mockRestore()
  })
})
