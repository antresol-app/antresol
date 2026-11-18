// «Пропускать сам»: the profile's choice (the server keeps it for every
// device, a kid's included: it is a convenience, not a restriction). The
// player's copy is optimistic and goes back when the server refuses; what
// this browser kept before the profile did moves to the profile once.
import { useEffect, useRef, type Dispatch, type SetStateAction } from 'react'
import { setAutoSkip as saveAutoSkip } from '../api/client'
import type { AutoSkipPrefs, PlaybackPrefs } from '../api/types'
import type { AutoSkip } from './SkipControls'

/** What this browser kept before the profile did ('1': it skipped by itself). */
export const LEGACY_AUTO_SKIP = { recap: 'vault.player.autoSkipRecap', intro: 'vault.player.autoSkipIntro' } as const

const NONE: AutoSkip = { recap: false, intro: false, credits: false }
const KINDS = ['recap', 'intro', 'credits'] as const

function local(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}
function forget(key: string) {
  try {
    localStorage.removeItem(key)
  } catch {
    /* private mode */
  }
}

/** The switches the profile has on at the server (it chose). */
const chosen = (a: AutoSkipPrefs) => a.recap || a.intro || a.credits

export function useAutoSkip(prefs: PlaybackPrefs, setPrefs: Dispatch<SetStateAction<PlaybackPrefs>>): [AutoSkip, (v: AutoSkip) => void] {
  const autoSkip = prefs.autoSkip ?? NONE
  const apply = (patch: Partial<AutoSkip>) => setPrefs((x) => ({ ...x, autoSkip: { ...(x.autoSkip ?? NONE), ...patch } }))

  // only the switches that changed go to the server (the other devices' stay)
  const change = (v: AutoSkip) => {
    const patch: Partial<AutoSkip> = {}
    const back: Partial<AutoSkip> = {}
    for (const k of KINDS) {
      if (v[k] !== autoSkip[k]) {
        patch[k] = v[k]
        back[k] = autoSkip[k]
      }
    }
    if (Object.keys(patch).length === 0) return
    apply(patch)
    saveAutoSkip(patch).catch(() => apply(back))
  }

  // Once: the keys of this browser move to the profile, and go only when
  // the server took them. A profile that already has a choice keeps it (the
  // server wins: the keys go without being sent); a failed PUT leaves the
  // keys for the next player.
  const moving = useRef(false)
  const server = prefs.autoSkip
  useEffect(() => {
    if (!server || moving.current) return // an older server keeps no choice: the keys stay
    const mine: Partial<AutoSkip> = {}
    if (local(LEGACY_AUTO_SKIP.recap) === '1') mine.recap = true
    if (local(LEGACY_AUTO_SKIP.intro) === '1') mine.intro = true
    if (Object.keys(mine).length === 0) return
    const clear = () => {
      forget(LEGACY_AUTO_SKIP.recap)
      forget(LEGACY_AUTO_SKIP.intro)
    }
    if (chosen(server)) return clear()
    moving.current = true
    saveAutoSkip(mine)
      .then(() => {
        clear()
        apply(mine)
      })
      .catch(() => {})
      .finally(() => {
        moving.current = false
      })
    // apply only sets state
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server])

  return [autoSkip, change]
}
