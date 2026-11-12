// «Сейчас в эфире» on Главная: the channels and what they show, the way
// into «ТВ» from Главная. Nothing when there are no channels.
import { useEffect, useState } from 'react'
import { getChannels, type Channel } from '../../api/channels'
import { useT } from '../../i18n'
import { ChannelShelf } from './ChannelShelf'
import { REFRESH_MS, useNow } from './useChannels'

export function OnAirShelf() {
  const t = useT()
  const [channels, setChannels] = useState<Channel[]>([])
  const now = useNow()
  useEffect(() => {
    let ac = new AbortController()
    const load = () => {
      ac.abort()
      ac = new AbortController()
      getChannels(ac.signal).then((l) => setChannels((Array.isArray(l?.channels) ? l.channels : []).filter((c) => c.now).sort((a, b) => a.number - b.number)), () => undefined)
    }
    load()
    const id = window.setInterval(load, REFRESH_MS)
    return () => {
      window.clearInterval(id)
      ac.abort()
    }
  }, [])
  if (channels.length === 0) return null
  return <ChannelShelf title={t('tv.onAir')} channels={channels.slice(0, 20)} now={now} seeAllTo="/tv" />
}
