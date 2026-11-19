// «Скоро выйдет» on Главная: the next episode (or release) of each title the
// profile follows, from today on, soonest first, a cover each; its title
// opens «Календарь». Nothing when nothing is coming (or the ask fails: the
// rest of the page stands without it).
import { useEffect, useState } from 'react'
import { getTrackingCalendar } from '../api/client'
import type { Card } from '../api/types'
import { Shelf } from '../components/Shelf'
import { locale, useT } from '../i18n'
import { calendarCard, episodeLabel, parseYmd, ymd } from './calendarCards'

/** How far ahead the shelf looks (the server answers at most 42 days). */
export const UPCOMING_DAYS = 30
const MAX = 20

export function UpcomingShelf() {
  const t = useT()
  const [cards, setCards] = useState<Card[]>([])

  useEffect(() => {
    const ac = new AbortController()
    const now = new Date()
    const today = ymd(now)
    const until = ymd(new Date(now.getFullYear(), now.getMonth(), now.getDate() + UPCOMING_DAYS))
    const day = new Intl.DateTimeFormat(locale(), {
      day: 'numeric',
      month: 'short',
    })
    getTrackingCalendar(today, until, ac.signal)
      .then((days) => {
        const seen = new Set<string>()
        const out: Card[] = []
        for (const d of days ?? []) {
          const when = d.date === today ? t('tracking.calendar.today') : day.format(parseYmd(d.date))
          for (const it of d.items) {
            const k = `${it.title.kind}-${it.title.tmdbId}`
            if (seen.has(k) || out.length === MAX) continue
            seen.add(k)
            out.push(calendarCard(t, it, `${episodeLabel(t, it)} · ${when}`))
          }
        }
        setCards(out)
      })
      .catch(() => undefined)
    return () => ac.abort()
  }, [t])

  return <Shelf title={t('tracking.upcoming.title')} cards={cards} seeAllTo="/calendar" />
}
