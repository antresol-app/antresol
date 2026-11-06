// A lazily loaded chunk whose pages draw at once when it is already in.
// React.lazy suspends on its first render even when the import has long
// finished, and React reveals a suspended boundary only after its 300 ms
// fallback throttle: a section rendered inside another lazy page of the
// same chunk («Управление → Настройки») would wait that out every time.
import { lazy, useState, type ComponentType } from 'react'
import { reloadForNewBuild } from './staleBuild'

export interface Chunk<M> {
  /** Loads the chunk (the import is cached by the browser). */
  load: () => Promise<M>
  /** The module once it has loaded. */
  loaded: () => M | undefined
}

export function chunk<M>(load: () => Promise<M>): Chunk<M> {
  let mod: M | undefined
  return {
    load: () =>
      load().then((m) => {
        // an import that "succeeded" with nothing (a cancelled preload error) is a failure
        if (m === undefined || m === null) throw new Error('chunk: no module')
        mod = m
        return m
      }),
    loaded: () => mod,
  }
}

/** A component of the chunk: React.lazy while the chunk loads, the component itself once it is in. */
export function lazyFrom<M>(c: Chunk<M>, pick: (m: M) => ComponentType): ComponentType {
  const Lazy = lazy(() =>
    c.load().then(
      (m) => ({ default: pick(m) }),
      (e: unknown) => {
        // a chunk of a build the server no longer has: reload onto the new one (once);
        // while it reloads nothing renders, else ChunkBoundary says it failed
        if (reloadForNewBuild()) return new Promise<never>(() => {})
        throw e
      },
    ),
  )
  return function FromChunk() {
    // chosen once per mount: switching from the lazy wrapper to the component would remount the page
    const [Loaded] = useState<ComponentType | undefined>(() => {
      const m = c.loaded()
      return m && pick(m)
    })
    return Loaded ? <Loaded /> : <Lazy />
  }
}
