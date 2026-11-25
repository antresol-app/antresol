// The one badge: a small capsule with a fact or a status. `Badge` is one;
// `Badges` the facts worth a chip for a kind of media («FLAC 24/96», «4K»,
// «RU · EPUB»), computed by the server (Card.badges). Audio and subtitle
// languages show an icon in place of the word («Звук RU EN» → 🔈 RU EN); the
// word stays for screen readers.
import type { ReactNode } from 'react'
import { useT } from '../i18n'
import { Icon } from '../components/Icon'
import styles from './Badge.module.css'

export type BadgeTone = 'default' | 'strong' | 'outline' | 'warning' | 'accent' | 'success' | 'destructive'

export interface BadgeProps {
  children: ReactNode
  tone?: BadgeTone
  /** A status rather than a fact: a capsule. */
  round?: boolean
  /** `status`: a status that stands where a small button stood («Ждёт ответа» in place of «Попросить»): its height, its type, a glyph before the word. */
  size?: 'default' | 'status'
  title?: string
  className?: string
}

/** One capsule. */
export function Badge({ children, tone = 'default', round, size = 'default', title, className }: BadgeProps) {
  return (
    <span className={[styles.badge, tone !== 'default' ? styles[tone] : '', round ? styles.round : '', size === 'status' ? styles.status : '', className].filter(Boolean).join(' ')} title={title}>
      {children}
    </span>
  )
}

// The server's words (both languages) and the release table's «Субтитры: …».
const AUDIO = /^(Звук|Audio)(?::\s*|\s+|$)/
const SUBS = /^(Субтитры|Subtitles)(?::\s*|\s+|$)/

/** A badge's text, with the audio or subtitles word turned into an icon. */
export function BadgeLabel({ text }: { text: string }) {
  const m = AUDIO.exec(text) ?? SUBS.exec(text)
  if (!m) return <span className={styles.text}>{text}</span>
  const rest = text.slice(m[0].length)
  return (
    <>
      <Icon name={AUDIO.test(text) ? 'speaker' : 'captions'} size={13} strokeWidth={2} className={styles.icon} />
      <span className="visually-hidden">{rest ? `${m[1]} ` : m[1]}</span>
      <span className={styles.text}>{rest}</span>
    </>
  )
}

/**
 * A row of badges. `inline`: spans in place of a list, for a row inside
 * a button or a label (where a list may not go); the text still reads.
 */
export function Badges({ items, className, inline }: { items?: string[]; className?: string; inline?: boolean }) {
  const t = useT()
  if (!items || items.length === 0) return null
  const cls = [styles.badges, className].filter(Boolean).join(' ')
  if (inline)
    return (
      <span className={cls}>
        {items.map((b) => (
          <span key={b} className={styles.badge} title={b}>
            <BadgeLabel text={b} />
          </span>
        ))}
      </span>
    )
  return (
    <ul className={cls} aria-label={t('comp.badges')}>
      {items.map((b) => (
        <li key={b} className={styles.badge} title={b}>
          <BadgeLabel text={b} />
        </li>
      ))}
    </ul>
  )
}
