// «Сейчас в эфире» on Главная: the channels and what they show, the way
// into «ТВ» from Главная. Nothing when there are no channels.
import { useState } from 'react'
import { getChannels, type Channel } from '../../api/channels'
import { useT } from '../../i18n'
import { usePolling } from '../../state/usePolling'
import { ChannelShelf } from './ChannelShelf'
import { REFRESH_MS, useNow } from './useChannels'

export function OnAirShelf() {
  const t = useT()
  const [channels, setChannels] = useState<Channel[]>([])
  const now = useNow()
  usePolling(async (signal) => {
    // brief: the card shows the title and the times only; the full list costs a query per channel
    const l = await getChannels(signal, false, true)
    setChannels((Array.isArray(l?.channels) ? l.channels : []).filter((c) => c.now).sort((a, b) => a.number - b.number))
  }, REFRESH_MS)
  if (channels.length === 0) return null
  return <ChannelShelf title={t('tv.onAir')} channels={channels.slice(0, 20)} now={now} seeAllTo="/tv" />
}
