// «Настройки сервера → Почта и уведомления → Бот Telegram»: the bot that sends
// notifications to Telegram, next to the mailbox that sends them by mail.
// One row like the others (the bot's @name, or «Не подключён», as its
// value); the steps and the token field are in the sheet it opens, set up
// once. The token is pasted, checked with Telegram («Проверить» asks getMe and
// shows the bot's @name) and kept by the server; it never comes back.
import { useEffect, useState } from 'react'
import { checkTelegramBot, clearTelegramBot, getTelegramBot, setTelegramToken, errorText } from '../../api/client'
import type { TelegramBot } from '../../api/types'
import { useT } from '../../i18n'
import { Button } from '../../ui/Button'
import { GroupedSection, ListLinkRow } from '../../ui/List'
import { Sheet } from '../../ui/Sheet'
import { TextField } from '../../ui/TextField'
import styles from './Settings.module.css'

export function TelegramSection() {
  const t = useT()
  const [bot, setBot] = useState<TelegramBot | null>(null)
  const [open, setOpen] = useState(false)

  useEffect(() => {
    const ac = new AbortController()
    getTelegramBot(ac.signal).then(setBot, () => {})
    return () => ac.abort()
  }, [])

  const ready = Boolean(bot?.set && bot.username)
  return (
    <GroupedSection>
      <ListLinkRow label={t('settings.tg')} value={ready ? `@${bot!.username}` : t('settings.tg.notReady')} onOpen={() => setOpen(true)} />
      {open && (
        <Sheet
          title={t('settings.tg.title')}
          role="dialog"
          size="wide"
          onCancel={() => setOpen(false)}
          closeButton={false}
          actions={[{ label: t('common.done'), kind: 'primary', run: () => setOpen(false) }]}
        >
          <TelegramFields bot={bot} onBot={setBot} />
        </Sheet>
      )}
    </GroupedSection>
  )
}

function TelegramFields({ bot, onBot }: { bot: TelegramBot | null; onBot: (b: TelegramBot) => void }) {
  const t = useT()
  const [token, setToken] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [guide, setGuide] = useState(!bot?.set)

  // a token typed: it is checked and kept; nothing typed: the stored one is asked again
  const check = async () => {
    setBusy(true)
    setError('')
    try {
      const next = token.trim() ? await setTelegramToken(token.trim()) : await checkTelegramBot()
      setToken('')
      onBot(next)
    } catch (e) {
      setError(errorText(e, t('settings.tg.checkFailed')))
    } finally {
      setBusy(false)
    }
  }
  const remove = async () => {
    setBusy(true)
    setError('')
    try {
      onBot(await clearTelegramBot())
      setGuide(true)
    } catch (e) {
      setError(errorText(e, t('settings.tg.removeFailed')))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className={styles.mailBlock}>
      {guide ? (
        <ol className={styles.mailGuide} aria-label={t('settings.tg.guide')}>
          <li>
            {t('settings.tg.step1')}{' '}
            <a href="https://t.me/BotFather" target="_blank" rel="noopener noreferrer">
              {t('settings.tg.step1Link')}
            </a>
          </li>
          <li>{t('settings.tg.step2')}</li>
          <li>{t('settings.tg.step3')}</li>
        </ol>
      ) : (
        <Button size="small" variant="plain" icon="info" onClick={() => setGuide(true)}>
          {t('settings.tg.guide')}
        </Button>
      )}
      <div className={styles.tgFields}>
        <TextField
          label={t('settings.tg.token')}
          hint={bot?.set ? t('settings.tg.tokenSet') : t('settings.tg.tokenHint')}
          type="password"
          revealable
          autoComplete="new-password"
          spellCheck={false}
          value={token}
          onChange={(e) => setToken(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !busy) void check()
          }}
        />
        <div className={styles.tgActions}>
          <Button size="small" variant="secondary" loading={busy} disabled={busy || (!token.trim() && !bot?.set)} onClick={() => void check()}>
            {t('settings.tg.check')}
          </Button>
          {bot?.set && (
            <Button size="small" variant="destructive" disabled={busy} onClick={() => void remove()}>
              {t('settings.tg.remove')}
            </Button>
          )}
        </div>
        {error ? (
          <p className={styles.mailError} role="alert">
            {error}
          </p>
        ) : bot?.set && bot.username ? (
          <p className={styles.tgOk} role="status">
            {t('settings.tg.botOk', { bot: bot.username })}
          </p>
        ) : null}
        {bot?.set && bot.problem && (
          <p className={styles.mailError} role="alert">
            {t(`settings.tg.problem.${bot.problem}`)}
          </p>
        )}
      </div>
    </div>
  )
}
