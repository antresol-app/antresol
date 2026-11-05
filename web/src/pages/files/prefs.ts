// Per-device view preferences (view mode, sort, «Свойства» open). Stored in
// localStorage as a convenience only: everything works without it.
import { useCallback, useState } from 'react'

export function usePref<T extends string | boolean>(key: string, initial: T, valid: (v: unknown) => v is T): [T, (v: T) => void] {
  const [value, setValue] = useState<T>(() => {
    try {
      const raw = localStorage.getItem(`vault.files.${key}`)
      if (raw !== null) {
        const v: unknown = JSON.parse(raw)
        if (valid(v)) return v
      }
    } catch {
      /* private mode or blocked storage */
    }
    return initial
  })
  const set = useCallback(
    (v: T) => {
      setValue(v)
      try {
        localStorage.setItem(`vault.files.${key}`, JSON.stringify(v))
      } catch {
        /* ignore */
      }
    },
    [key],
  )
  return [value, set]
}

export const oneOf =
  <T extends string>(...values: T[]) =>
  (v: unknown): v is T =>
    typeof v === 'string' && (values as string[]).includes(v)
export const isBool = (v: unknown): v is boolean => typeof v === 'boolean'
