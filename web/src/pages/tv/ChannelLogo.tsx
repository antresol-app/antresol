// A channel's uploaded logo, small, in a neutral rounded box — where a
// channel is shown (the list row, the details pane, the guide's channel
// column, the live player's header and its channel strip, the editor). No
// logo (or one that fails to load): nothing, the number and the name stand
// alone — never a generated tile. `reserve`: a list where some channel
// has a logo keeps an empty slot of the same width in the rows without one,
// so every name starts at the same place.
import { useState } from 'react'
import styles from './ChannelLogo.module.css'

export type ChannelLogoSize = 'xs' | 'sm' | 'md' | 'lg'

export function ChannelLogo({ url, size = 'sm', reserve, className }: { url?: string; size?: ChannelLogoSize; reserve?: boolean; className?: string }) {
  const [failed, setFailed] = useState<string | null>(null)
  if (!url || failed === url) return reserve ? <span className={[styles.slot, styles[size], className].filter(Boolean).join(' ')} aria-hidden data-logo-slot /> : null
  return (
    <span className={[styles.logo, styles[size], className].filter(Boolean).join(' ')} aria-hidden data-channel-logo>
      <img src={url} alt="" decoding="async" loading="lazy" draggable={false} onError={() => setFailed(url)} />
    </span>
  )
}
