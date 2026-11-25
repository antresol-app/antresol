// A placeholder where content is on its way: a quiet block in a fill, no
// shimmer. The caller gives it its size and shape (a class with `width`,
// `height` or `aspect-ratio`, `border-radius`); the block is decoration, so
// the list around it carries `aria-busy`.
//   tone  faint (--fill-4, the default: a poster, a row) · soft (--fill-3: a title's bar)
import type { CSSProperties } from 'react'
import styles from './Skeleton.module.css'

export interface SkeletonProps {
  tone?: 'faint' | 'soft'
  className?: string
  style?: CSSProperties
}

export function Skeleton({ tone = 'faint', className, style }: SkeletonProps) {
  return <div aria-hidden className={[styles.skeleton, tone === 'soft' ? styles.soft : '', className].filter(Boolean).join(' ')} style={style} />
}
