// React view of the audio engine (same pattern as useSession).
import { useSyncExternalStore } from 'react'
import { audio, audioNotice, type AudioState } from './engine'

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

/** The current toast text of the engine, or null. */
export function useAudioNotice(): string | null {
  return useSyncExternalStore(audioNotice.subscribe, audioNotice.get, audioNotice.get)
}
