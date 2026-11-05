// Finder-like file icons drawn as SVG: a blue two-tone folder (with an
// embossed glyph for special folders) and a paper document with a folded
// corner, a coloured kind glyph and the extension label.
import type { FileEntry } from '../../api/types'
import { extBadge } from './model'
import { glyphPaths, type GlyphName } from './glyphs'

interface KindStyle {
  glyph: GlyphName
  color: string
}

const KINDS: Record<string, KindStyle> = {
  video: { glyph: 'play', color: '#7d5cf0' },
  audio: { glyph: 'music', color: '#f2385a' },
  image: { glyph: 'eye', color: '#2fb35a' },
  subtitle: { glyph: 'listView', color: '#6e6e76' },
  text: { glyph: 'listView', color: '#6e6e76' },
  book: { glyph: 'book', color: '#e5484d' },
  comic: { glyph: 'book', color: '#ff8a00' },
  archive: { glyph: 'disk', color: '#8e8e93' },
  ...(__LIBRARY_EDITION__ ? {} : { torrent: { glyph: 'arrowDownCircle' as const, color: '#28a745' } }),
  other: { glyph: 'listView', color: '#8e8e93' },
}

export interface FileIconProps {
  entry: Pick<FileEntry, 'name' | 'isDir' | 'kind'>
  size: number
  /** Glyph embossed on a folder (sidebar roots, the trash). */
  emblem?: GlyphName
  className?: string
}

function Folder({ size, emblem, className }: { size: number; emblem?: GlyphName; className?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" aria-hidden className={className} focusable="false">
      <path d="M4 17a5 5 0 0 1 5-5h13.4a4 4 0 0 1 2.9 1.2l2.6 2.8H55a5 5 0 0 1 5 5v26a5 5 0 0 1-5 5H9a5 5 0 0 1-5-5z" fill="#1f86e0" />
      <path d="M4 24.5a4 4 0 0 1 4-4h48a4 4 0 0 1 4 4V47a5 5 0 0 1-5 5H9a5 5 0 0 1-5-5z" fill="#48a9f6" />
      <path d="M8 21h48a3.5 3.5 0 0 1 3.5 3.3" fill="none" stroke="rgba(255,255,255,0.35)" strokeWidth="0.8" />
      {emblem && (
        <svg x="21" y="25.5" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="#1d74c4" strokeWidth="2.1" strokeLinecap="round" strokeLinejoin="round">
          <g style={{ color: '#1d74c4' }}>{glyphPaths[emblem]}</g>
        </svg>
      )}
    </svg>
  )
}

function Document({ entry, size, className }: { entry: FileIconProps['entry']; size: number; className?: string }) {
  const k = KINDS[entry.kind] ?? KINDS.other!
  const badge = extBadge(entry.name)
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" aria-hidden className={className} focusable="false">
      <path d="M15 4h24.5L52 16.5V57a3 3 0 0 1-3 3H15a3 3 0 0 1-3-3V7a3 3 0 0 1 3-3z" fill="#f5f5f7" stroke="rgba(0,0,0,0.22)" strokeWidth="0.75" />
      <path d="M39.5 4v9.5a3 3 0 0 0 3 3H52" fill="#d9d9de" stroke="rgba(0,0,0,0.18)" strokeWidth="0.75" strokeLinejoin="round" />
      <svg x="21" y="20" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke={k.color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <g style={{ color: k.color }}>{glyphPaths[k.glyph]}</g>
      </svg>
      {badge && (
        <text
          x="32"
          y="53"
          textAnchor="middle"
          fill={k.color}
          fontSize={badge.length > 3 ? 7 : 8}
          fontWeight="700"
          letterSpacing="0.3"
          fontFamily="-apple-system, BlinkMacSystemFont, 'SF Pro Text', Inter, sans-serif"
        >
          {badge}
        </text>
      )}
    </svg>
  )
}

export function FileIcon({ entry, size, emblem, className }: FileIconProps) {
  return entry.isDir ? <Folder size={size} emblem={emblem} className={className} /> : <Document entry={entry} size={size} className={className} />
}

/** Glyph on a folder that is a library item (film / tv), so it reads at a glance. */
export function folderEmblem(e: Pick<FileEntry, 'isDir' | 'item'>): GlyphName | undefined {
  if (!e.isDir || !e.item) return undefined
  return e.item.kind === 'series' ? 'tv' : 'film'
}
