// «Telegram» of the notification settings: the same news in a Telegram chat.
// «Подключить Telegram» makes a one-time code and shows the link that carries
// it (t.me/<bot>?start=<code>); the person opens it and presses «Запустить»,
// and the server, which asks Telegram for the bot's messages, links the chat.
// This sheet only watches for that (no webhook, nothing to type back). Linked:
// the chat's name, a switch for the channel and «Отключить». Not offered to a
// kid's profile (its parent's settings never reach here) or where the server
// has no bot.
import { useEffect, useId, useRef, useState } from 'react'
import { getNotifyPrefs, linkTelegram, unlinkTelegram, errorText, safeHttpUrl } from '../api/client'
import type { NotifyPrefs, TelegramLink } from '../api/types'
import { useT } from '../i18n'
import { Button, ButtonAnchor } from '../ui/Button'
import { Toggle } from '../ui/Toggle'
import { Icon } from './Icon'
import styles from './NotifySheet.module.css'

/** How often the sheet asks whether the chat is linked. */
const WATCH_MS = 2500

export interface NotifyTelegramProps {
  prefs: NotifyPrefs
  /** The profile's own state changed (linked, unlinked): the sheet takes it. */
  onLinked: (p: NotifyPrefs) => void
  /** The channel's switch. */
  onToggle: (on: boolean) => void
  onError: (message: string) => void
  /** How often to look for the linked chat (ms; tests make it short). */
  watchMs?: number
}

export function NotifyTelegram({ prefs, onLinked, onToggle, onError, watchMs = WATCH_MS }: NotifyTelegramProps) {
  const t = useT()
  const id = useId()
  const [link, setLink] = useState<TelegramLink | null>(null)
  const [busy, setBusy] = useState(false)
  const [expired, setExpired] = useState(false)
  const found = useRef(onLinked)
  found.current = onLinked

  // a code is out: look for the chat until it appears or the code runs out
  useEffect(() => {
    if (!link) return
    const until = Date.parse(link.expires)
    let live = true
    const timer = window.setInterval(() => {
      if (Date.now() > until) {
        setLink(null)
        setExpired(true)
        return
      }
      getNotifyPrefs().then(
        (p) => {
          if (!live || !p.telegramLinked) return
          setLink(null)
          found.current(p)
        },
        () => {},
      )
    }, watchMs)
    return () => {
      live = false
      window.clearInterval(timer)
    }
  }, [link, watchMs])

  const connect = async () => {
    setBusy(true)
    setExpired(false)
    onError('')
    try {
      setLink(await linkTelegram())
    } catch (e) {
      onError(errorText(e, t('comp.notify.tg.linkFailed')))
    } finally {
      setBusy(false)
    }
  }

  const disconnect = async () => {
    setBusy(true)
    onError('')
    try {
      setLink(null)
      onLinked(await unlinkTelegram())
    } catch (e) {
      onError(errorText(e, t('comp.notify.tg.unlinkFailed')))
    } finally {
      setBusy(false)
    }
  }

  const linked = Boolean(prefs.telegramLinked)
  // nothing to connect to, and nothing connected: the section stays out of the way
  if (!linked && !prefs.telegramReady) return null
  return (
    <section className={styles.section} aria-labelledby={`${id}-title`}>
      <h3 className={styles.label} id={`${id}-title`}>
        {t('comp.notify.tg.title')}
      </h3>
      {linked ? (
        <>
          <p className={styles.ok} role="status">
            <Icon name="checkCircle" size={16} />{' '}
            {prefs.telegramName ? t('comp.notify.tg.connected', { name: prefs.telegramName }) : t('comp.notify.tg.connectedNoName')}
          </p>
          {!prefs.telegramReady && <p className={styles.warning}>{t('comp.notify.tg.botGone')}</p>}
          <ul className={styles.list}>
            <li className={styles.item}>
              <div className={styles.itemText}>
                <span aria-hidden>{t('comp.notify.tg.on')}</span>
              </div>
              <Toggle label={t('comp.notify.tg.on')} checked={prefs.telegramOn !== false} onChange={onToggle} />
            </li>
          </ul>
          <div className={styles.row}>
            <Button size="small" variant="secondary" disabled={busy} onClick={() => void disconnect()}>
              {t('comp.notify.tg.disconnect')}
            </Button>
          </div>
        </>
      ) : link ? (
        <>
          <p className={styles.hint}>{t('comp.notify.tg.steps')}</p>
          <div className={styles.row}>
            <ButtonAnchor size="small" variant="primary" icon="send" href={safeHttpUrl(link.url)} target="_blank" rel="noopener noreferrer">
              {t('comp.notify.tg.open')}
            </ButtonAnchor>
            <Button size="small" variant="secondary" onClick={() => setLink(null)}>
              {t('comp.notify.tg.cancel')}
            </Button>
          </div>
          <p className={styles.hint}>{t('comp.notify.tg.manual', { bot: link.bot, command: `/start ${link.code}` })}</p>
        </>
      ) : (
        <>
          <p className={styles.hint}>{t('comp.notify.tg.hint', { bot: prefs.telegramBot ?? '' })}</p>
          {expired && <p className={styles.hint}>{t('comp.notify.tg.expired')}</p>}
          <div className={styles.row}>
            <Button size="small" variant="secondary" icon="send" loading={busy} disabled={busy} onClick={() => void connect()}>
              {t('comp.notify.tg.connect')}
            </Button>
          </div>
        </>
      )}
    </section>
  )
}
