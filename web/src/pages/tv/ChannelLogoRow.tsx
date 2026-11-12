// «Логотип» in the channel editor: the preview, «Загрузить…» / «Заменить…»
// (PNG, JPEG or WebP up to 2 MB, checked here before it is sent; the
// server's refusal is shown in the row) and «Убрать». A saved channel
// uploads at once; a new one keeps the file until it is created.
import { useEffect, useRef, useState } from 'react'
import { errorText } from '../../api/client'
import { deleteChannelLogo, LOGO_MAX_BYTES, LOGO_TYPES, uploadChannelLogo } from '../../api/channels'
import { Icon } from '../../components/Icon'
import { useT } from '../../i18n'
import { Button } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { ListRow } from '../../ui/List'
import { ChannelLogo } from './ChannelLogo'
import styles from './Tv.module.css'

/** Why a file cannot be a logo ('' : it can). */
export function logoProblem(f: File): 'type' | 'size' | '' {
  if (!LOGO_TYPES.includes(f.type)) return 'type'
  if (f.size > LOGO_MAX_BYTES) return 'size'
  return ''
}

/** A saved channel's logo address after an upload: the server's, else a fresh one of its own. */
export const freshLogoUrl = (id: number, answer: { logoUrl?: string } | undefined | null) => answer?.logoUrl ?? `/api/channels/${id}/logo.png?v=${Date.now()}`

export interface ChannelLogoRowProps {
  /** The saved channel's id; null: a new channel (the file waits for «Создать»). */
  channelId: number | null
  /** The logo now (a saved one's address). */
  url: string | undefined
  /** A saved channel's logo changed on the server. */
  onChanged: (url: string | undefined) => void
  /** A new channel's file to upload once it is created. */
  pending: File | null
  onPending: (f: File | null) => void
}

export function ChannelLogoRow({ channelId, url, onChanged, pending, onPending }: ChannelLogoRowProps) {
  const t = useT()
  const input = useRef<HTMLInputElement>(null)
  const [busy, setBusy] = useState<'upload' | 'remove' | null>(null)
  const [error, setError] = useState('')
  const [said, setSaid] = useState('')
  const [preview, setPreview] = useState<string | undefined>(undefined)
  useEffect(() => {
    if (!pending) {
      setPreview(undefined)
      return
    }
    const u = URL.createObjectURL(pending)
    setPreview(u)
    return () => URL.revokeObjectURL(u)
  }, [pending])
  const shown = pending ? preview : url

  const pick = async (f: File | undefined) => {
    if (!f) return
    const bad = logoProblem(f)
    if (bad) {
      setError(bad === 'type' ? t('tv.logo.badType') : t('tv.logo.tooBig'))
      return
    }
    setError('')
    if (channelId === null) {
      onPending(f)
      return
    }
    setBusy('upload')
    try {
      const answer = await uploadChannelLogo(channelId, f)
      onChanged(freshLogoUrl(channelId, answer))
      setSaid(t('tv.logo.uploaded'))
    } catch (e) {
      setError(errorText(e, t('tv.logo.failed')))
    } finally {
      setBusy(null)
    }
  }

  const remove = async () => {
    setError('')
    if (channelId === null) {
      onPending(null)
      return
    }
    setBusy('remove')
    try {
      await deleteChannelLogo(channelId)
      onChanged(undefined)
      setSaid(t('tv.logo.removed'))
    } catch (e) {
      setError(errorText(e, t('common.genericError')))
    } finally {
      setBusy(null)
    }
  }

  return (
    <ListRow
      label={t('tv.logo.title')}
      stack
      hint={error ? <span className={styles.formErrorInline} role="alert">{error}</span> : t('tv.logo.hint')}
      status={said || undefined}
      leading={
        shown ? (
          <ChannelLogo url={shown} size="lg" />
        ) : (
          <span className={styles.logoWell} aria-hidden>
            <Icon name="tv" size={20} strokeWidth={1.5} />
          </span>
        )
      }
    >
      <input
        ref={input}
        type="file"
        accept={LOGO_TYPES.join(',')}
        className="visually-hidden"
        tabIndex={-1}
        aria-hidden
        data-testid="logo-file"
        onChange={(e) => {
          void pick(e.target.files?.[0])
          e.target.value = ''
        }}
      />
      {shown && (
        <IconButton
          variant="plain"
          size="sm"
          tone="destructive"
          icon="trash"
          label={t('tv.logo.remove')}
          loading={busy === 'remove'}
          disabled={busy !== null}
          onClick={() => void remove()}
        />
      )}
      <Button size="small" variant="secondary" loading={busy === 'upload'} disabled={busy !== null} onClick={() => input.current?.click()}>
        {shown ? t('tv.logo.replace') : t('tv.logo.upload')}
      </Button>
    </ListRow>
  )
}
