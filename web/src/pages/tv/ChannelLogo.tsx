// A channel's tile, as the Apple TV app shows a channel — where a channel
// is shown (the list row, the details pane, the guide's channel column,
// the live player's header and its channel list, the editor). With a logo
// (or, when it fails to load, the guide's icon): the logo fitted whole on a
// quiet tile. Without one and with a `name`: a tile of its own — one of the
// design system's art gradients (the profile avatars'), picked by the name
// so a channel keeps its colour everywhere, with its initials in white. A
// list's rows are one shape whichever channels have logos. Without a name:
// nothing, or with `reserve` an empty slot of the same width, so every name
// starts at the same place.
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

/** The art gradients a generated tile takes (tokens --grad-1…7: the avatars', not the grey one). */
export const TILE_TONES = 7

/** The tone of a channel's generated tile (1…TILE_TONES): the same name, the same colour, on every screen. */
export function channelTone(name: string): number {
  let h = 0
  for (const ch of name.trim().toLocaleLowerCase()) h = (h * 31 + ch.codePointAt(0)!) >>> 0
  return (h % TILE_TONES) + 1
}

export function ChannelLogo({ url: first, fallback, name, size = 'sm', reserve, className }: { url?: string; fallback?: string; name?: string; size?: ChannelLogoSize; reserve?: boolean; className?: string }) {
  const [failed, setFailed] = useState<string[]>([])
  // a logo that does not load gives way to the fallback (an IPTV channel's guide icon), then to the initials
  const url = [first, fallback].find((u) => u && !failed.includes(u))
  const cls = (base: string | undefined) => [base, styles[size], className].filter(Boolean).join(' ')
  if (!url) {
    if (name)
      return (
        <span className={[cls(styles.tile), styles[`tone${channelTone(name)}`]].join(' ')} aria-hidden data-channel-initials data-channel-tone={channelTone(name)}>
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
