// The coloured tile of a settings list (iOS / macOS System Settings): a
// white glyph on a rounded square of one palette colour. One glyph set of its
// own, drawn for the tile: filled shapes (cut-outs where a detail is needed)
// and, where a symbol is a line (arrows, waves), one stroke weight, so every
// tile of a list reads alike. The app's line icons (components/Icon) are
// drawn for controls and are too thin and uneven on a tile; they never go on
// one. «Управление»'s sections, the addons' tiles and every ListLinkRow use
// this component.
import type { ReactNode } from 'react'
import type { IconName } from '../components/Icon'
import styles from './SettingsIcon.module.css'

/** The tile colours: the palette's (DESIGN.md: palette), named. */
export const SETTINGS_TINTS = ['blue', 'green', 'indigo', 'orange', 'pink', 'purple', 'red', 'teal', 'cyan', 'gray'] as const
export type SettingsTint = (typeof SETTINGS_TINTS)[number]

// the one stroke of the line parts (arrows, waves, the globe's meridians)
const line = { fill: 'none', stroke: 'currentColor', strokeWidth: 2.2, strokeLinecap: 'round', strokeLinejoin: 'round' } as const
const cut = (d: string) => <path d={d} fillRule="evenodd" clipRule="evenodd" />
// the chip's pins: three a side
const PINS = [8.4, 11.25, 14.1]

const glyphs = {
  /** a profile */
  person: (
    <>
      <circle cx="12" cy="7.8" r="4.3" />
      <path d="M3.8 20c0-4.3 3.7-6.8 8.2-6.8s8.2 2.5 8.2 6.8a1 1 0 0 1-1 1H4.8a1 1 0 0 1-1-1z" />
    </>
  ),
  /** profiles, the family */
  people: (
    <>
      <circle cx="9" cy="8" r="3.7" />
      <path d="M2.3 19.6c0-3.6 3-5.8 6.7-5.8s6.7 2.2 6.7 5.8a1 1 0 0 1-1 1H3.3a1 1 0 0 1-1-1z" />
      <circle cx="16.6" cy="8.6" r="3.1" />
      <path d="M16.9 13.9c3 .3 5.1 2.2 5.1 5.1a1 1 0 0 1-1 1h-3.7c.1-.4.1-.8.1-1.2 0-2-.6-3.6-1.7-4.9z" />
    </>
  ),
  /** requests: a speech bubble with an ellipsis */
  bubble: cut(
    'M12 3.3c-5.1 0-9.2 3.4-9.2 7.7 0 2.4 1.3 4.6 3.4 6-.2 1.4-.9 2.7-2 3.7 2.3 0 4.2-.8 5.5-2 .7.2 1.5.3 2.3.3 5.1 0 9.2-3.4 9.2-7.9S17.1 3.3 12 3.3zM7.9 9.7a1.3 1.3 0 1 0 0 2.6 1.3 1.3 0 0 0 0-2.6zm4.1 0a1.3 1.3 0 1 0 0 2.6 1.3 1.3 0 0 0 0-2.6zm4.1 0a1.3 1.3 0 1 0 0 2.6 1.3 1.3 0 0 0 0-2.6z',
  ),
  /** moving in from another server: a packed box */
  box: (
    <>
      <path d="M12 2.6 19.9 6.8 12 11 4.1 6.8z" />
      <path d="M3.4 8 11.2 12.2v9.2L3.4 17.2z" />
      <path d="M20.6 8 12.8 12.2v9.2l7.8-4.2z" />
    </>
  ),
  /** an import: an arrow into a tray */
  trayDown: (
    <>
      <path d="M3.3 13.4h4.3l1.4 2.6h6l1.4-2.6h4.3v4.8a2.6 2.6 0 0 1-2.6 2.6H5.9a2.6 2.6 0 0 1-2.6-2.6z" />
      <path d="M10.9 3h2.2v7.4l2.5-2.5 1.6 1.6-5.2 5.2-5.2-5.2 1.6-1.6 2.5 2.5z" />
    </>
  ),
  folder: <path d="M2.8 7a2.5 2.5 0 0 1 2.5-2.5h3.9c.6 0 1.2.2 1.6.6l1.5 1.4h6.4A2.5 2.5 0 0 1 21.2 9v8.5a2.5 2.5 0 0 1-2.5 2.5H5.3a2.5 2.5 0 0 1-2.5-2.5z" />,
  /** statistics: three bars */
  chart: (
    <>
      <rect x="3.5" y="12.5" width="4.4" height="8" rx="1.2" />
      <rect x="9.8" y="8" width="4.4" height="12.5" rx="1.2" />
      <rect x="16.1" y="3.5" width="4.4" height="17" rx="1.2" />
    </>
  ),
  /** extensions */
  puzzle: (
    <path d="M9.2 3.2a2.6 2.6 0 0 1 2.6 2.6v.7h4.4c.9 0 1.5.7 1.5 1.5v3.8h.6a2.6 2.6 0 0 1 0 5.2h-.6v3.2c0 .9-.7 1.5-1.5 1.5H4.9c-.9 0-1.5-.7-1.5-1.5v-3.6h.8a2.4 2.4 0 0 0 0-4.8h-.8V8c0-.9.7-1.5 1.5-1.5h1.7v-.7a2.6 2.6 0 0 1 2.6-2.6z" />
  ),
  /** updates: two arrows round a circle */
  updates: (
    <>
      <path {...line} d="M5.2 10.6A7 7 0 0 1 17.2 7.6" />
      <path d="m20.4 4.3-.3 5.8-5.6-1.2z" />
      <path {...line} d="M18.8 13.4A7 7 0 0 1 6.8 16.4" />
      <path d="m3.6 19.7.3-5.8 5.6 1.2z" />
    </>
  ),
  /** the library: a stack of titles */
  stack: (
    <>
      <rect x="6.4" y="2.8" width="11.2" height="1.8" rx=".9" />
      <rect x="4.6" y="5.6" width="14.8" height="2" rx="1" />
      <rect x="2.8" y="8.7" width="18.4" height="12.3" rx="2.4" />
    </>
  ),
  /** watching: a picture with a play mark */
  playRect: cut('M5.4 4.6h13.2a3 3 0 0 1 3 3v8.8a3 3 0 0 1-3 3H5.4a3 3 0 0 1-3-3V7.6a3 3 0 0 1 3-3zm4.4 4.2v6.4c0 .5.5.8.9.5l5.1-3.2a.6.6 0 0 0 0-1l-5.1-3.2a.6.6 0 0 0-.9.5z'),
  /** transcoding: a chip */
  cpu: (
    <>
      {cut('M8 5.8h8A2.2 2.2 0 0 1 18.2 8v8a2.2 2.2 0 0 1-2.2 2.2H8A2.2 2.2 0 0 1 5.8 16V8A2.2 2.2 0 0 1 8 5.8zm2 4.2v4h4v-4z')}
      {PINS.map((p) => (
        <g key={p}>
          <rect x={p} y="2.6" width="1.5" height="2.4" rx=".5" />
          <rect x={p} y="19" width="1.5" height="2.4" rx=".5" />
          <rect x="2.6" y={p} width="2.4" height="1.5" rx=".5" />
          <rect x="19" y={p} width="2.4" height="1.5" rx=".5" />
        </g>
      ))}
    </>
  ),
  /** music: two beamed notes */
  note: <path d="M19.2 3.9c0-.7-.6-1.1-1.2-1l-9 2c-.5.1-.8.5-.8 1v8.4a3.1 3.1 0 1 0 2.1 2.9V9.7l6.8-1.5v4a3.1 3.1 0 1 0 2.1 2.9z" />,
  /** subtitles: a frame with lines of text */
  captions: cut(
    'M5 4.5h14A2.5 2.5 0 0 1 21.5 7v10a2.5 2.5 0 0 1-2.5 2.5H5A2.5 2.5 0 0 1 2.5 17V7A2.5 2.5 0 0 1 5 4.5zm.9 7.2v1.9h6.2v-1.9zm7.8 0v1.9h4.4v-1.9zm-7.8 3.3v1.9h3.9V15zm5.5 0v1.9h6.7V15z',
  ),
  /** mail */
  envelope: (
    <>
      <path d="M4.3 5.1 12 11.1l7.7-6a2.5 2.5 0 0 0-.7-.1H5c-.2 0-.5 0-.7.1z" />
      <path d="M2.5 7.2v9.3A2.5 2.5 0 0 0 5 19h14a2.5 2.5 0 0 0 2.5-2.5V7.2l-8.6 6.7a1.5 1.5 0 0 1-1.8 0z" />
    </>
  ),
  /** the assistant */
  sparkles: (
    <>
      <path d="M10 3.2c.5 4 2.6 6.1 6.6 6.6-4 .5-6.1 2.6-6.6 6.6-.5-4-2.6-6.1-6.6-6.6 4-.5 6.1-2.6 6.6-6.6z" />
      <path d="M17.8 13.2c.3 2.2 1.4 3.3 3.6 3.6-2.2.3-3.3 1.4-3.6 3.6-.3-2.2-1.4-3.3-3.6-3.6 2.2-.3 3.3-1.4 3.6-3.6z" />
      <path d="M18 2.4c.2 1.4.9 2.1 2.3 2.3-1.4.2-2.1.9-2.3 2.3-.2-1.4-.9-2.1-2.3-2.3 1.4-.2 2.1-.9 2.3-2.3z" />
    </>
  ),
  /** TV channels */
  tv: (
    <>
      <rect x="2.4" y="4" width="19.2" height="13.2" rx="2.5" />
      <rect x="7.4" y="18.8" width="9.2" height="1.9" rx=".95" />
    </>
  ),
  /** downloads: an arrow down in a disc */
  arrowDownCircle: cut('M12 2.4a9.6 9.6 0 1 1 0 19.2 9.6 9.6 0 0 1 0-19.2zm-1.1 4.8v6.1l-2.2-2.2-1.6 1.6 4.9 4.9 4.9-4.9-1.6-1.6-2.2 2.2V7.2z'),
  /** sources: a point that sends */
  antenna: (
    <>
      <circle cx="12" cy="12" r="2.4" />
      <path {...line} d="M8.2 8.2a5.4 5.4 0 0 0 0 7.6M15.8 8.2a5.4 5.4 0 0 1 0 7.6M5.2 5.2a9.6 9.6 0 0 0 0 13.6M18.8 5.2a9.6 9.6 0 0 1 0 13.6" />
    </>
  ),
  /** online */
  globe: (
    <g {...line} strokeWidth={1.9}>
      <circle cx="12" cy="12" r="9" />
      <ellipse cx="12" cy="12" rx="4" ry="9" />
      <path d="M3.4 9.2h17.2M3.4 14.8h17.2" />
    </g>
  ),
  /** a section's own settings */
  sliders: (
    <>
      <path {...line} d="M3.5 6.5h17M3.5 12h17M3.5 17.5h17" />
      <circle cx="15.2" cy="6.5" r="2.7" />
      <circle cx="8.4" cy="12" r="2.7" />
      <circle cx="14.4" cy="17.5" r="2.7" />
    </>
  ),
  /** following: an open eye */
  eye: cut('M12 5c5.1 0 8.7 4 10 7-1.3 3-4.9 7-10 7S3.3 15 2 12c1.3-3 4.9-7 10-7zm0 3.2a3.8 3.8 0 1 0 0 7.6 3.8 3.8 0 0 0 0-7.6zm0 2a1.8 1.8 0 1 1 0 3.6 1.8 1.8 0 0 1 0-3.6z'),
  /** notifications */
  bell: (
    <>
      <path d="M12 2.7c.7 0 1.2.5 1.2 1.2v.7a6 6 0 0 1 4.9 5.9v3.8l1.7 2.4c.4.5 0 1.3-.7 1.3H4.9c-.7 0-1.1-.8-.7-1.3l1.7-2.4v-3.8a6 6 0 0 1 4.9-5.9v-.7c0-.7.5-1.2 1.2-1.2z" />
      <path d="M9.4 19.4h5.2a2.6 2.6 0 0 1-5.2 0z" />
    </>
  ),
  /** sound */
  speaker: (
    <>
      <path d="M3.5 9h3.3l4.3-3.6c.5-.4 1.3 0 1.3.6v12c0 .6-.8 1-1.3.6L6.8 15H3.5a1 1 0 0 1-1-1v-4a1 1 0 0 1 1-1z" />
      <path {...line} d="M15.6 9.2a4 4 0 0 1 0 5.6M18.4 6.4a8 8 0 0 1 0 11.2" />
    </>
  ),
  /** a password */
  key: cut('M8.2 4.6a5.4 5.4 0 0 1 5 7.4l7.6 7.6v2.2h-3v-1.9h-1.9V18h-1.9l-2.4-2.4A5.4 5.4 0 1 1 8.2 4.6zM7.4 7.7a1.8 1.8 0 1 0 0 3.6 1.8 1.8 0 0 0 0-3.6z'),
  /** the profile's devices */
  devices: (
    <>
      <rect x="2.4" y="5.2" width="13" height="9.4" rx="1.6" />
      <rect x="1.2" y="15.6" width="15.4" height="1.9" rx=".95" />
      <rect x="17.4" y="8.4" width="5.4" height="11" rx="1.5" />
    </>
  ),
  /** what is saved on this device */
  phoneDown: cut('M8.6 2.4h6.8A2.4 2.4 0 0 1 17.8 4.8v14.4a2.4 2.4 0 0 1-2.4 2.4H8.6a2.4 2.4 0 0 1-2.4-2.4V4.8a2.4 2.4 0 0 1 2.4-2.4zm2.4 4.4v5.3l-1.8-1.8-1.3 1.3L12 15.7l4.1-4.1-1.3-1.3-1.8 1.8V6.8z'),
  /** linked accounts: two chain links */
  link: (
    <>
      <path {...line} d="M10.4 13.6a3.9 3.9 0 0 0 5.5 0l3.3-3.3a3.9 3.9 0 0 0-5.5-5.5l-1.3 1.3" />
      <path {...line} d="M13.6 10.4a3.9 3.9 0 0 0-5.5 0l-3.3 3.3a3.9 3.9 0 0 0 5.5 5.5l1.3-1.3" />
    </>
  ),
  /** about */
  info: cut('M12 2.4a9.6 9.6 0 1 1 0 19.2 9.6 9.6 0 0 1 0-19.2zm0 3.8a1.4 1.4 0 1 0 0 2.8 1.4 1.4 0 0 0 0-2.8zm-1.9 4.1v1.7h1v4.6h-1v1.7h4.4v-1.7h-1.2v-6.3z'),
} satisfies Record<string, ReactNode>

export type SettingsGlyph = keyof typeof glyphs

/** Whether `name` is a glyph of the set (an addon's tile may name a line icon). */
export const isSettingsGlyph = (name: string): name is SettingsGlyph => Object.hasOwn(glyphs, name)

/**
 * An addon's tile that names a line icon of the app (the contract before the
 * tile glyphs): the tile glyph of the same meaning, else the puzzle piece.
 */
const FROM_LINE: Partial<Record<IconName, SettingsGlyph>> = {
  download: 'arrowDownCircle',
  plug: 'antenna',
  gear: 'sliders',
  sliders: 'sliders',
  eye: 'eye',
  cloud: 'globe',
  folder: 'folder',
  chart: 'chart',
  people: 'people',
  person: 'person',
  music: 'note',
  tv: 'tv',
  film: 'stack',
  play: 'playRect',
  captions: 'captions',
  send: 'envelope',
  sparkles: 'sparkles',
  info: 'info',
  lock: 'key',
  speaker: 'speaker',
}
export function settingsGlyph(name: string): SettingsGlyph {
  if (isSettingsGlyph(name)) return name
  return FROM_LINE[name as IconName] ?? 'puzzle'
}

/** A tint of the set, else gray (an addon may name anything). */
export const settingsTint = (name: string | undefined): SettingsTint => (SETTINGS_TINTS as readonly string[]).includes(name ?? '') ? (name as SettingsTint) : 'gray'

export function SettingsIcon({ glyph, tint, size = 'md', className }: { glyph: SettingsGlyph; tint: SettingsTint; size?: 'sm' | 'md'; className?: string }) {
  return (
    <span className={[styles.tile, size === 'sm' ? styles.sm : '', className].filter(Boolean).join(' ')} style={{ background: `var(--${tint})` }} aria-hidden>
      <svg viewBox="0 0 24 24" fill="currentColor" focusable="false">
        {glyphs[glyph]}
      </svg>
    </span>
  )
}

/** Every glyph's name (the gallery shows them all). */
export const SETTINGS_GLYPHS = Object.keys(glyphs) as SettingsGlyph[]
