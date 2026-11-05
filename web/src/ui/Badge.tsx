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

/** One capsule. `round`: a status rather than a fact. */
export function Badge({ children, tone = 'default', round, title, className }: { children: ReactNode; tone?: BadgeTone; round?: boolean; title?: string; className?: string }) {
  return (
    <span className={[styles.badge, tone !== 'default' ? styles[tone] : '', round ? styles.round : '', className].filter(Boolean).join(' ')} title={title}>
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
  if (!m) return <>{text}</>
  const rest = text.slice(m[0].length)
  return (
    <>
      <Icon name={AUDIO.test(text) ? 'speaker' : 'captions'} size={13} strokeWidth={2} className={styles.icon} />
      <span className="visually-hidden">{rest ? `${m[1]} ` : m[1]}</span>
      {rest}
    </>
  )
}

export function Badges({ items, className }: { items?: string[]; className?: string }) {
  const t = useT()
  if (!items || items.length === 0) return null
  return (
    <ul className={[styles.badges, className].filter(Boolean).join(' ')} aria-label={t('comp.badges')}>
      {items.map((b) => (
        <li key={b} className={styles.badge}>
          <BadgeLabel text={b} />
        </li>
      ))}
    </ul>
  )
}
