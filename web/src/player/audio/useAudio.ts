// React view of the audio engine (same pattern as useSession).
import { useSyncExternalStore } from 'react'
import { audio, audioNotice, type AudioState } from './engine'
import type { QueueItem } from './queue'

export function useAudio(): AudioState {
  return useSyncExternalStore(audio.subscribe, audio.get, audio.get)
}

/**
 * One value derived from the engine, re-rendering only when it changes
 * (not on every timeupdate). `select` must return a primitive.
 */
export function useAudioSelect<T extends string | number | boolean | null | undefined>(select: (s: AudioState) => T): T {
  return useSyncExternalStore(audio.subscribe, () => select(audio.get()), () => select(audio.get()))
}

/** The queue's current item: it changes with the track (the object stays the same as the position moves). */
export function useAudioItem(): QueueItem | undefined {
  const item = () => {
    const s = audio.get()
    return s.queue[s.index]
  }
  return useSyncExternalStore(audio.subscribe, item, item)
}

/** The current toast text of the engine, or null. */
export function useAudioNotice(): string | null {
  return useSyncExternalStore(audioNotice.subscribe, audioNotice.get, audioNotice.get)
}
