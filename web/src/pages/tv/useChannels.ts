// «ТВ»: the channel list as the pages keep it (read again every half
// minute and on library changes), the profile's stars (optimistic: the
// star turns at once, comes back if the server refuses), and the clock the
// progress bars read.
import { useCallback, useEffect, useRef, useState } from 'react'
import { errorText } from '../../api/client'
import { getChannels, setChannelFavorite, type ChannelList } from '../../api/channels'
import { locale, useT } from '../../i18n'
import { useLoad } from '../../state/useLoad'
import { withFavorite } from './model'

/** How often what is on air is read again. */
export const REFRESH_MS = 30_000

/** «Now» for the progress bars, ticking. */
export function useNow(every = REFRESH_MS / 3) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), every)
    return () => window.clearInterval(id)
  }, [every])
  return now
}

/** 21:40 in the interface's language, to the nearest minute (schedule
 * times carry seconds: 21:39:50 is shown as 21:40, where the guide draws it). */
export function clock(t: string | number): string {
  const ms = Math.round(new Date(t).getTime() / 60_000) * 60_000
  return new Date(ms).toLocaleTimeString(locale(), { hour: '2-digit', minute: '2-digit' })
}

/** The channel list with stars that turn at once. */
export function useChannelList() {
  const t = useT()
  const { state, load } = useLoad((signal) => getChannels(signal), [], {
    message: (e) => errorText(e, t('common.loadFailed')),
    events: ['library.updated'],
  })
  useEffect(() => {
    const id = window.setInterval(() => load(true), REFRESH_MS)
    return () => window.clearInterval(id)
  }, [load])
  // stars set here and not yet confirmed: they win over a list read meanwhile
  const [stars, setStars] = useState<Record<number, boolean>>({})
  const [favError, setFavError] = useState<string | null>(null)
  const pending = useRef(0)
  const toggleFavorite = useCallback(
    (id: number, on: boolean) => {
      setStars((s) => ({ ...s, [id]: on }))
      setFavError(null)
      pending.current++
      setChannelFavorite(id, on).then(
        () => {
          pending.current--
          load(true)
        },
        (e: unknown) => {
          pending.current--
          setStars((s) => ({ ...s, [id]: !on }))
          setFavError(errorText(e, t('tv.favFailed')))
        },
      )
    },
    [load, t],
  )
  const raw: ChannelList | null = state.status === 'ready' ? state.data : null
  // once the server has the list with the star, the local override is moot
  useEffect(() => {
    if (!raw || pending.current > 0) return
    setStars((s) => (Object.keys(s).length ? {} : s))
  }, [raw])
  let data = raw
  if (raw && Object.keys(stars).length) {
    let channels = raw.channels
    for (const [id, on] of Object.entries(stars)) channels = withFavorite(channels, Number(id), on)
    data = { ...raw, channels }
  }
  return { state, data, load, toggleFavorite, favError, clearFavError: () => setFavError(null) }
}
