// Line glyphs used only by the file browser (sidebar, toolbar,
// menus). Same 24×24 grid and stroke as components/Icon.
import type { SVGProps } from 'react'

export const glyphPaths = {
  folder: <path d="M3.5 7.5V17a2 2 0 0 0 2 2h13a2 2 0 0 0 2-2V9.5a2 2 0 0 0-2-2h-6.3L10.4 5.6a1.6 1.6 0 0 0-1.1-.5H5.5a2 2 0 0 0-2 2z" />,
  folderPlus: (
    <>
      <path d="M3.5 7.5V17a2 2 0 0 0 2 2h13a2 2 0 0 0 2-2V9.5a2 2 0 0 0-2-2h-6.3L10.4 5.6a1.6 1.6 0 0 0-1.1-.5H5.5a2 2 0 0 0-2 2z" />
      <path d="M12 10.5v6M9 13.5h6" />
    </>
  ),
  trash: (
    <>
      <path d="M4.5 6.5h15M9.5 6.5V5a1.5 1.5 0 0 1 1.5-1.5h2A1.5 1.5 0 0 1 14.5 5v1.5" />
      <path d="M6.5 6.5 7.4 19a1.6 1.6 0 0 0 1.6 1.5h6a1.6 1.6 0 0 0 1.6-1.5l.9-12.5" />
      <path d="M10 10.5v6M14 10.5v6" />
    </>
  ),
  disk: (
    <>
      <rect x="3" y="7" width="18" height="10" rx="2.5" />
      <path d="M7 12h5" />
      <circle cx="16.5" cy="12" r="0.9" fill="currentColor" stroke="none" />
    </>
  ),
  clock: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.5V12l3 2" />
    </>
  ),
  download: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.5v8M8.5 12.5 12 16l3.5-3.5" />
    </>
  ),
  library: (
    <>
      <rect x="3" y="4" width="4.5" height="16" rx="1" />
      <rect x="9.5" y="4" width="4.5" height="16" rx="1" />
      <path d="m15 5.4 3.5-.9 3 15.3-3.5.9z" />
    </>
  ),
  film: (
    <>
      <rect x="3.5" y="4.5" width="17" height="15" rx="2.5" />
      <path d="M7.5 4.5v15M16.5 4.5v15M3.5 9.5h4M3.5 14.5h4M16.5 9.5h4M16.5 14.5h4" />
    </>
  ),
  tv: (
    <>
      <rect x="3" y="5" width="18" height="12" rx="2.5" />
      <path d="M8.5 20.5h7" />
    </>
  ),
  music: (
    <>
      <path d="M9 18V6.5l11-2.5V16" />
      <circle cx="6.5" cy="18" r="2.5" />
      <circle cx="17.5" cy="16" r="2.5" />
    </>
  ),
  book: (
    <>
      <path d="M12 6.5C10.3 5.2 8 4.5 4 4.5v13c4 0 6.3.7 8 2 1.7-1.3 4-2 8-2v-13c-4 0-6.3.7-8 2z" />
      <path d="M12 6.5v13" />
    </>
  ),
  chevronLeft: <path d="M15 5.5 8.5 12l6.5 6.5" />,
  chevronRight: <path d="M9 5.5 15.5 12 9 18.5" />,
  chevronDown: <path d="M5.5 9 12 15.5 18.5 9" />,
  /** Small filled disclosure triangle (list view). */
  disclosure: <path d="M9 6.5v11l7.5-5.5z" fill="currentColor" stroke="none" />,
  gridView: (
    <>
      <rect x="4" y="4" width="6.5" height="6.5" rx="1.6" />
      <rect x="13.5" y="4" width="6.5" height="6.5" rx="1.6" />
      <rect x="4" y="13.5" width="6.5" height="6.5" rx="1.6" />
      <rect x="13.5" y="13.5" width="6.5" height="6.5" rx="1.6" />
    </>
  ),
  listView: (
    <>
      <path d="M9 6.5h11M9 12h11M9 17.5h11" />
      <circle cx="4.8" cy="6.5" r="1.1" fill="currentColor" stroke="none" />
      <circle cx="4.8" cy="12" r="1.1" fill="currentColor" stroke="none" />
      <circle cx="4.8" cy="17.5" r="1.1" fill="currentColor" stroke="none" />
    </>
  ),
  columnView: (
    <>
      <rect x="3" y="5" width="18" height="14" rx="2.5" />
      <path d="M9 5v14M15 5v14" />
    </>
  ),
  sort: (
    <>
      <path d="M7.5 19V5M4 8.5 7.5 5 11 8.5" />
      <path d="M16.5 5v14M13 15.5l3.5 3.5 3.5-3.5" />
    </>
  ),
  info: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 11v5.5" />
      <circle cx="12" cy="7.8" r="0.5" fill="currentColor" />
    </>
  ),
  search: (
    <>
      <circle cx="10.5" cy="10.5" r="6.5" />
      <path d="m15.5 15.5 5 5" />
    </>
  ),
  xmarkCircle: (
    <>
      <circle cx="12" cy="12" r="9" fill="currentColor" stroke="none" />
      <path d="m9 9 6 6M15 9l-6 6" stroke="var(--files-xmark, var(--gray-6))" strokeWidth="2" />
    </>
  ),
  close: <path d="m6.5 6.5 11 11M17.5 6.5l-11 11" />,
  eye: (
    <>
      <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z" />
      <circle cx="12" cy="12" r="3" />
    </>
  ),
  pencil: (
    <>
      <path d="M15.5 5.5 18.5 8.5 9 18l-4 1 1-4z" />
      <path d="m13.5 7.5 3 3" />
    </>
  ),
  move: (
    <>
      <path d="M3.5 7.5V17a2 2 0 0 0 2 2h13a2 2 0 0 0 2-2V9.5a2 2 0 0 0-2-2h-6.3L10.4 5.6a1.6 1.6 0 0 0-1.1-.5H5.5a2 2 0 0 0-2 2z" />
      <path d="M8.5 13.5h7M13 11l2.5 2.5L13 16" />
    </>
  ),
  arrowDownCircle: (
    <>
      <path d="M12 4v11M7.5 10.5 12 15l4.5-4.5" />
      <path d="M5 19.5h14" />
    </>
  ),
  restore: (
    <>
      <path d="M9 14.5 4.5 10 9 5.5" />
      <path d="M4.5 10h10a5 5 0 0 1 0 10H11" />
    </>
  ),
  open: (
    <>
      <path d="M14 4.5h5.5V10M19.5 4.5 11 13" />
      <path d="M18 14v4a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4" />
    </>
  ),
  play: <path d="M7 4.8v14.4a.8.8 0 0 0 1.2.7l11.6-7.2a.8.8 0 0 0 0-1.4L8.2 4.1A.8.8 0 0 0 7 4.8z" fill="currentColor" stroke="none" />,
  ellipsisCircle: (
    <>
      <circle cx="12" cy="12" r="8.8" />
      <circle cx="8" cy="12" r="1.1" fill="currentColor" stroke="none" />
      <circle cx="12" cy="12" r="1.1" fill="currentColor" stroke="none" />
      <circle cx="16" cy="12" r="1.1" fill="currentColor" stroke="none" />
    </>
  ),
  ellipsis: (
    <>
      <circle cx="6" cy="12" r="1.6" fill="currentColor" stroke="none" />
      <circle cx="12" cy="12" r="1.6" fill="currentColor" stroke="none" />
      <circle cx="18" cy="12" r="1.6" fill="currentColor" stroke="none" />
    </>
  ),
  check: <path d="m5 12.5 4.5 4.5L19 7.5" />,
  checkCircle: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="m8.2 12.3 2.6 2.6 5-5.4" />
    </>
  ),
  warning: (
    <>
      <path d="M10.3 4.6 2.9 17.5A2 2 0 0 0 4.6 20.5h14.8a2 2 0 0 0 1.7-3L13.7 4.6a2 2 0 0 0-3.4 0z" />
      <path d="M12 9.5v4.5" />
      <circle cx="12" cy="17" r="0.4" fill="currentColor" />
    </>
  ),
  arrowUp: <path d="M12 19V5M6.5 10.5 12 5l5.5 5.5" />,
} as const

export type GlyphName = keyof typeof glyphPaths

export interface GlyphProps extends Omit<SVGProps<SVGSVGElement>, 'children'> {
  name: GlyphName
  size?: number
  label?: string
}

export function Glyph({ name, size = 18, label, strokeWidth = 1.8, ...rest }: GlyphProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden={label ? undefined : true}
      role={label ? 'img' : undefined}
      aria-label={label}
      focusable="false"
      {...rest}
    >
      {glyphPaths[name]}
    </svg>
  )
}

/** Sidebar glyph of a root (server sends film / tv / music / book / download / clock / disk / library). */
export function rootGlyph(icon: string): GlyphName {
  return (icon in glyphPaths ? icon : 'folder') as GlyphName
}

/** The glyph of a library item's kind (the «в медиатеке» links). */
export function itemGlyph(kind: string): GlyphName {
  switch (kind) {
    case 'series':
    case 'season':
    case 'episode':
      return 'tv'
    case 'album':
    case 'track':
      return 'music'
    case 'book':
    case 'audiobook':
    case 'comic':
    case 'comic_volume':
      return 'book'
    default:
      return 'film'
  }
}
