// The video players' sound: the level and the mute, remembered on this device
// (one pair of keys, so the film player and the live channel player start at
// the level the other left it) and applied to the <video>. The film and the
// live player both take it from here; VolumeControl is its face.
import { useCallback, useEffect, useState, type RefObject } from 'react'

const VOLUME_KEY = 'vault.player.volume'
const MUTED_KEY = 'vault.player.muted'

function read(k: string): string | null {
  try {
    return localStorage.getItem(k)
  } catch {
    return null
  }
}
function write(k: string, v: string) {
  try {
    localStorage.setItem(k, v)
  } catch {
    /* private mode */
  }
}

export function useVolume(video: RefObject<HTMLVideoElement | null>) {
  const [volume, setVolume] = useState(() => {
    const v = Number(read(VOLUME_KEY))
    // 0 is never restored: a player that opens silent would look broken
    return Number.isFinite(v) && v > 0 && v <= 1 ? v : 1
  })
  const [muted, setMuted] = useState(() => read(MUTED_KEY) === '1')
  useEffect(() => {
    const v = video.current
    if (!v) return
    v.volume = volume
    v.muted = muted
  }, [video, volume, muted])
  /** A level 0–1 (clamped); moving it up from silence un-mutes. */
  const changeVolume = useCallback((val: number) => {
    const x = Math.min(Math.max(val, 0), 1)
    setVolume(x)
    write(VOLUME_KEY, String(x))
    if (x > 0) {
      setMuted(false)
      write(MUTED_KEY, '0')
    }
  }, [])
  const toggleMute = useCallback(() => {
    setMuted((m) => {
      write(MUTED_KEY, m ? '0' : '1')
      return !m
    })
  }, [])
  /** The browser would not start the picture with sound: mute it for this start only, not for next time. */
  const muteForNow = useCallback(() => setMuted(true), [])
  return { volume, muted, changeVolume, toggleMute, muteForNow }
}
