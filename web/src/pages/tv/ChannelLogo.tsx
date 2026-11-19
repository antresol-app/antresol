// A channel's logo, small, in a neutral rounded box — where a channel is
// shown (the list row, the details pane, the guide's channel column, the
// live player's header and its channel list, the editor). A channel without
// a logo (or one that fails to load) and a `name`: its initials in the same
// box, so a list's rows are one shape whichever channels have logos (the
// monogram of the design system: a profile's avatar letter, a person's
// initials — here neutral, the box is the channel's place, not a colour of
// its own). Without a name: nothing, or with `reserve` an empty slot of the
// same width, so every name starts at the same place.
import { useState } from 'react'
import { locale } from '../../i18n'
import styles from './ChannelLogo.module.css'

export type ChannelLogoSize = 'xs' | 'sm' | 'md' | 'lg'

/** Words a playlist adds to a channel's name that say nothing of it. */
const NOISE = /^(hd|fhd|uhd|sd|hq|4k|8k|orig|original|\+\d+|\d+p|50fps|h\.?26[45]|hevc)$/i

/** A channel's initials: the first letters of its first two words (one word: its first letter), quality marks left out. */
export function channelInitials(name: string): string {
  const words = name
    .split(/[\s\-–—_|/\\:.,·•()[\]{}«»"'#&!?*]+/)
    .filter((w) => w && !NOISE.test(w))
  const out = words
    .slice(0, 2)
    .map((w) => w.match(/[\p{L}\p{N}]/u)?.[0] ?? '')
    .join('')
  return (out || Array.from(name.trim())[0] || '?').toLocaleUpperCase(locale())
}

export function ChannelLogo({ url: first, fallback, name, size = 'sm', reserve, className }: { url?: string; fallback?: string; name?: string; size?: ChannelLogoSize; reserve?: boolean; className?: string }) {
  const [failed, setFailed] = useState<string[]>([])
  // a logo that does not load gives way to the fallback (an IPTV channel's guide icon), then to the initials
  const url = [first, fallback].find((u) => u && !failed.includes(u))
  const cls = (base: string | undefined) => [base, styles[size], className].filter(Boolean).join(' ')
  if (!url) {
    if (name)
      return (
        <span className={cls(styles.initials)} aria-hidden data-channel-initials>
          {channelInitials(name)}
        </span>
      )
    return reserve ? <span className={cls(styles.slot)} aria-hidden data-logo-slot /> : null
  }
  return (
    <span className={cls(styles.logo)} aria-hidden data-channel-logo>
      <img src={url} alt="" decoding="async" loading="lazy" draggable={false} onError={() => setFailed((f) => [...f, url])} />
    </span>
  )
}
