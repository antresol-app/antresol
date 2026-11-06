// The one <img> for a picture of the library (a cover, a poster, a track's
// art): the server's size steps as a srcset for the width it is shown at,
// lazy unless it is the page's main picture, decoded off the main thread,
// and `fallback` (a glyph on its tile) when there is none or it fails.
import { useState, type ReactNode } from 'react'
import { imageSrcSet, imageUrl, IMAGE_WIDTHS } from '../api/client'

export interface ArtworkProps {
  /** The server's image reference (or a URL). */
  src: string | null | undefined
  /** The widest it is drawn, in CSS px: the srcset runs from it to twice it (Retina). */
  width: number
  /** The `sizes` of its layout when it is not simply `width` px (a fluid cover). */
  sizes?: string
  alt?: string
  /** The page's main picture (a title's cover): loads at once. */
  eager?: boolean
  className?: string
  /** Shown when there is no picture or it fails to load. */
  fallback?: ReactNode
}

const step = (w: number) => IMAGE_WIDTHS.find((s) => s >= w) ?? IMAGE_WIDTHS[IMAGE_WIDTHS.length - 1]

export function Artwork({ src, width, sizes, alt = '', eager, className, fallback = null }: ArtworkProps) {
  const url = imageUrl(src, width)
  const [failed, setFailed] = useState<string | null>(null)
  if (!url || failed === url) return <>{fallback}</>
  return (
    <img
      className={className}
      src={url}
      srcSet={imageSrcSet(src, step(width), step(width * 2))}
      sizes={sizes ?? `${width}px`}
      alt={alt}
      loading={eager ? undefined : 'lazy'}
      decoding="async"
      draggable={false}
      onError={() => setFailed(url)}
    />
  )
}
