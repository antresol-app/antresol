// The play queue in «Сейчас играет»: the current track, then «Далее» with
// «Убрать» and «Выше»/«Ниже» buttons (keyboard-friendly reordering; focus
// follows the moved track).
import { Artwork } from '../ui/Artwork'
import { useLayoutEffect, useRef } from 'react'
import { Icon } from '../components/Icon'
import { audio, type QueueItem } from './audio/engine'
import { useAudio } from './audio/useAudio'
import styles from './QueueList.module.css'
import { useT } from '../i18n'

function Cover({ src }: { src: string }) {
  return (
    <span className={styles.cover}>
      <Artwork src={src} width={44} fallback={<Icon name="music" size={18} />} />
    </span>
  )
}

function Text({ it }: { it: QueueItem }) {
  return (
    <span className={styles.text}>
      <span className={styles.title}>{it.title}</span>
      <span className={styles.artist}>{it.artist}</span>
    </span>
  )
}

export interface QueueListProps {
  /** Leaves out «Сейчас играет» (the phone layout shows it above already). */
  hideNow?: boolean
}

export function QueueList({ hideNow }: QueueListProps) {
  const t = useT()
  const s = useAudio()
  const listRef = useRef<HTMLOListElement>(null)
  /** After a move: the button to focus again (`<key>|up` or `<key>|down`). */
  const refocus = useRef<string | null>(null)
  useLayoutEffect(() => {
    const want = refocus.current
    if (!want) return
    refocus.current = null
    const [key, dir] = want.split('|')
    const li = listRef.current?.querySelector<HTMLElement>(`[data-key="${CSS.escape(key!)}"]`)
    const btn = li?.querySelector<HTMLButtonElement>(`[data-dir="${dir}"]`)
    ;(btn && !btn.disabled ? btn : li?.querySelector<HTMLButtonElement>('button[data-dir]:not(:disabled)'))?.focus()
  })
  const move = (key: string, from: number, to: number, dir: 'up' | 'down') => {
    refocus.current = `${key}|${dir}`
    audio.move(from, to)
  }
  const now = s.queue[s.index]
  const first = s.index + 1
  const upcoming = s.queue.slice(first)

  return (
    <div className={styles.root}>
      {now && !hideNow && (
        <section aria-labelledby="queue-now">
          <h2 id="queue-now" className={styles.heading}>
            {t('player.nowPlaying')}
          </h2>
          <div className={`${styles.row} ${styles.now}`}>
            <span className={styles.main}>
              <Cover src={now.cover} />
              <Text it={now} />
            </span>
          </div>
        </section>
      )}
      <section aria-labelledby="queue-next">
        <div className={styles.headRow}>
          <h2 id="queue-next" className={styles.heading}>
            {t('player.upNext')}
          </h2>
          {(s.shuffle || s.repeat !== 'off') && (
            <span className={styles.modes}>
              {s.shuffle && t('player.queue.shuffled')}
              {s.shuffle && s.repeat !== 'off' && ' · '}
              {s.repeat === 'all' && t('player.queue.repeatingAll')}
              {s.repeat === 'one' && t('player.queue.repeatingOne')}
            </span>
          )}
        </div>
        {upcoming.length === 0 ? (
          <p className={styles.empty}>{t('player.queue.empty')}</p>
        ) : (
          <ol ref={listRef} className={styles.list} aria-label={t('player.upNext')}>
            {upcoming.map((it, j) => {
              const i = first + j
              return (
                <li key={it.key} className={styles.row} data-key={it.key}>
                  <button type="button" className={styles.main} onClick={() => audio.jumpTo(i)} aria-label={t('player.queue.playAria', { title: it.title, artist: it.artist })}>
                    <Cover src={it.cover} />
                    <Text it={it} />
                  </button>
                  <span className={styles.tools}>
                    <button type="button" data-dir="up" aria-label={t('player.queue.up', { title: it.title })} disabled={j === 0} onClick={() => move(it.key, i, i - 1, 'up')}>
                      <Icon name="arrowUp" size={16} />
                    </button>
                    <button
                      type="button"
                      data-dir="down"
                      aria-label={t('player.queue.down', { title: it.title })}
                      disabled={j === upcoming.length - 1}
                      onClick={() => move(it.key, i, i + 1, 'down')}
                    >
                      <Icon name="arrowDown" size={16} />
                    </button>
                    <button type="button" aria-label={t('player.queue.remove', { title: it.title })} onClick={() => audio.removeAt(i)}>
                      <Icon name="close" size={16} />
                    </button>
                  </span>
                </li>
              )
            })}
          </ol>
        )}
      </section>
    </div>
  )
}
