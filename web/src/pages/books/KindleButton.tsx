// «Отправить на Kindle»: the button itself tells how it went. Sent, it turns
// white and says so, then settles on «На Kindle»; pressed again, it asks before
// sending the same book twice. A failure goes to the page's error line.
import { useEffect, useRef, useState } from 'react'
import { sendToKindle, errorText } from '../../api/client'
import { Button } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { MenuItem, Menu } from '../../ui/Menu'
import { useT } from '../../i18n'
import styles from './BookDetail.module.css'

/** How long «Отправлено» stays before «На Kindle». */
const SENT_MS = 2500

export function KindleButton({ bookId, sent, compact, onError }: { bookId: number; sent: boolean; compact: boolean; onError: (message: string) => void }) {
  const t = useT()
  const [wasSent, setWasSent] = useState(sent)
  const [sending, setSending] = useState(false)
  const [justSent, setJustSent] = useState(false)
  const timer = useRef<number | undefined>(undefined)
  useEffect(() => () => window.clearTimeout(timer.current), [])
  useEffect(() => {
    if (sent) setWasSent(true)
  }, [sent])

  const send = () => {
    setSending(true)
    setJustSent(false)
    onError('')
    sendToKindle(bookId)
      .then(() => {
        setWasSent(true)
        setJustSent(true)
        window.clearTimeout(timer.current)
        timer.current = window.setTimeout(() => setJustSent(false), SENT_MS)
      })
      .catch((e: unknown) => onError(errorText(e, t('books.book.kindleFailed'))))
      .finally(() => setSending(false))
  }

  if (!wasSent || sending) {
    const label = t('books.book.sendToKindle')
    return compact ? (
      <IconButton icon="send" label={label} loading={sending} onClick={send} />
    ) : (
      <Button variant="secondary" icon="send" loading={sending} onClick={send}>
        {label}
      </Button>
    )
  }

  const label = justSent ? t('books.book.kindleSent') : t('books.book.onKindle')
  const done = `${styles.kindleDone} ${justSent ? styles.kindleJustSent : ''}`
  return (
    <>
      <Menu
        label={t('books.book.kindleAgainTitle')}
        placement={compact ? 'bottom-end' : 'bottom-start'}
        trigger={(p) =>
          compact ? (
            <IconButton {...p} icon="check" label={label} className={done} />
          ) : (
            <Button {...p} variant="primary" icon="check" className={done}>
              <span key={label} className={styles.kindleLabel}>
                {label}
              </span>
            </Button>
          )
        }
      >
        {(close) => (
          <>
            <p className={styles.kindleAsk}>{t('books.book.kindleAgainTitle')}</p>
            <MenuItem
              label={t('books.book.kindleAgain')}
              onSelect={() => {
                close(false)
                send()
              }}
            />
            <MenuItem label={t('common.cancel')} onSelect={() => close()} />
          </>
        )}
      </Menu>
      {/* a screen reader hears what the eye sees change */}
      <span className="visually-hidden" role="status">
        {justSent ? label : ''}
      </span>
    </>
  )
}
