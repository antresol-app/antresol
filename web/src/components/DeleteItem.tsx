// «Удалить» on a library item's page (admins): a red trash button that asks
// first. The sheet says where the files go (the Trash of «Файлы», where they
// can be restored) and how much is freed; a series with several seasons
// asks what to delete — the whole series or one season.
import { useCallback, useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { deleteItem, getDeletePlan, errorText } from '../api/client'
import type { DeletePlan, DeleteResult } from '../api/types'
import { formatSize } from './ActionBar'
import { IconButton, type IconButtonSize } from '../ui/IconButton'
import { episodesCount, seasonLabel } from './EpisodeList'
import { Icon } from './Icon'
import { Sheet } from '../ui/Sheet'
import { showNotice } from '../state/notice'
import { useT } from '../i18n'
import styles from './DeleteItem.module.css'

export interface Deleted {
  /** The item that was deleted: the page's, or the season picked. */
  id: number
  /** The page's item is gone (false: one season of a series that stays). */
  whole: boolean
  result: DeleteResult
}

export interface DeleteItemProps {
  itemId: number
  /** The name in the question until the server's answer comes. */
  title: string
  onDeleted: (d: Deleted) => void
  size?: IconButtonSize
  /** A label under the icon on phones (the item page's row of tiles). */
  tileLabel?: boolean
  className?: string
}

/** The red round trash button; opens the confirmation. */
export function DeleteItem({ itemId, title, onDeleted, size = 'md', tileLabel, className }: DeleteItemProps) {
  const t = useT()
  const [open, setOpen] = useState(false)
  return (
    <>
      <IconButton
        size={size}
        icon="trash"
        label={t('comp.delete.button')}
        tone="destructive"
        className={className}
        tile={tileLabel}
        onClick={() => setOpen(true)}
      />
      {open && <DeleteItemSheet itemId={itemId} title={title} onClose={() => setOpen(false)} onDeleted={onDeleted} />}
    </>
  )
}

/** Leaves the page of a deleted item: back where the admin came from, else to fallback. */
export function useLeaveDeleted() {
  const navigate = useNavigate()
  // the first page of the session has the key "default": nothing to go back to
  const first = useLocation().key === 'default'
  return useCallback(
    (fallback: string) => {
      if (first) navigate(fallback, { replace: true })
      else navigate(-1)
    },
    [navigate, first],
  )
}

type Load = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; plan: DeletePlan }

export function DeleteItemSheet({ itemId, title, onClose, onDeleted }: { itemId: number; title: string; onClose: () => void; onDeleted: (d: Deleted) => void }) {
  const t = useT()
  const [load, setLoad] = useState<Load>({ status: 'loading' })
  // the series (itemId) or one of its seasons
  const [scope, setScope] = useState(itemId)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState('')

  useEffect(() => {
    const ac = new AbortController()
    getDeletePlan(itemId, ac.signal).then(
      (plan) => setLoad({ status: 'ready', plan }),
      (e: unknown) => {
        if (!ac.signal.aborted) setLoad({ status: 'error', message: errorText(e, t('comp.delete.loadFailed')) })
      },
    )
    return () => ac.abort()
  }, [itemId, t])

  const plan = load.status === 'ready' ? load.plan : undefined
  const name = plan?.title || title
  const seasons = plan && plan.kind === 'series' && plan.seasons.length > 1 ? plan.seasons : []
  const season = seasons.find((s) => s.id === scope)
  const size = season ? season.size : (plan?.size ?? 0)
  const nothing = plan !== undefined && plan.files === 0
  // what else happens: for the season picked, not the whole series
  const notes = (season ? season.notes : plan?.notes) ?? []

  const run = async () => {
    setBusy(true)
    setProblem('')
    try {
      const result = await deleteItem(scope)
      // «Файлы» keeps listings and the disk usage: they changed (its own
      // chunk: loaded here only if it is not yet)
      void import('../pages/files/useDir').then((m) => m.invalidateDirs())
      void import('../pages/files/StorageBar').then((m) => m.resetUsageCache())
      const done = t('comp.delete.done', { name: season ? seasonLabel(season) : name })
      if (result.note) showNotice(`${done}. ${result.note}`, 8000)
      else showNotice(done)
      onClose()
      onDeleted({ id: scope, whole: !season || !result.parentShown, result })
    } catch (e) {
      setProblem(errorText(e, t('comp.delete.failed')))
      setBusy(false)
    }
  }

  const message =
    load.status === 'loading' ? (
      t('comp.delete.loading')
    ) : load.status === 'error' ? (
      <span role="alert">{load.message}</span>
    ) : nothing ? (
      t('comp.delete.nothing')
    ) : (
      <>
        <span className={styles.line}>{t('comp.delete.message')}</span>
        {size > 0 && <span className={styles.line}>{t('comp.delete.frees', { size: formatSize(size) })}</span>}
      </>
    )

  return (
    <Sheet
      title={t('comp.delete.title', { name })}
      icon={<Icon name="trash" size={34} strokeWidth={1.6} />}
      message={message}
      size={seasons.length ? 'wide' : 'alert'}
      busy={busy}
      onCancel={() => !busy && onClose()}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: () => !busy && onClose() },
        { label: t('comp.delete.button'), kind: 'destructive', disabled: !plan || nothing, run: () => void run() },
      ]}
    >
      {seasons.length > 0 && (
        <fieldset className={styles.scopes}>
          <legend className={styles.legend}>{t('comp.delete.scope')}</legend>
          {[{ id: itemId, label: t('comp.delete.whole'), detail: formatSize(plan!.size) }, ...seasons.map((s) => ({
            id: s.id,
            label: seasonLabel(s),
            detail: `${episodesCount(s.episodes)} · ${formatSize(s.size)}`,
          }))].map((o) => (
            <label key={o.id} className={styles.scope}>
              <input type="radio" name="delete-scope" value={o.id} checked={scope === o.id} disabled={busy} onChange={() => setScope(o.id)} />
              <span className={styles.scopeLabel}>{o.label}</span>
              <span className={styles.scopeDetail}>{o.detail}</span>
            </label>
          ))}
        </fieldset>
      )}
      {notes.length > 0 && (
        <ul className={styles.notes}>
          {notes.map((n) => (
            <li key={n}>{n}</li>
          ))}
        </ul>
      )}
      {problem && (
        <p className={styles.problem} role="alert">
          {problem}
        </p>
      )}
    </Sheet>
  )
}
