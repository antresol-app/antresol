// Admin operations with their sheets: rename, new folder, move and move to
// the trash. When the data is held by a managed download the server asks first
// (needsConfirm + warnings); the second sheet says what will stop and only
// then repeats the request with confirm.
import { useRef, useState } from 'react'
import { errorText } from '../../api/client'
import { mkdir, moveFile, needsConfirm, renameFile, restoreTrash, trashFiles } from '../../api/files'
import type { FileEntry, FileRoot } from '../../api/types'
import { joinPath, objects, parentPath } from './model'
import { FileIcon } from './FileIcon'
import { Glyph } from './glyphs'
import { MovePicker, NameField, Sheet, nameProblem, type MoveTarget } from './Sheet'
import { Thumb } from './Thumb'
import { t, useT } from '../../i18n'

export type Op =
  | { type: 'rename'; entry: FileEntry }
  | { type: 'mkdir' }
  | { type: 'move'; entries: FileEntry[] }
  | { type: 'trash'; entries: FileEntry[] }

export interface Toast {
  text: string
  action?: { label: string; run: () => void }
}

export interface OpDone {
  /** Path to select afterwards (renamed item, new folder). */
  select?: string
  toast?: Toast
}

export interface FileOpsProps {
  op: Op
  root: string
  /** Folder the operation happens in. */
  dir: string
  roots: FileRoot[]
  /** Names already in the folder (default name of a new folder). */
  names: string[]
  onClose: () => void
  onDone: (r: OpDone) => void
  /** Something changed on disk (a partial move): refresh without closing. */
  onChanged: () => void
}

const what = (es: FileEntry[]) => (es.length === 1 ? t('files.quoted', { name: es[0]!.name }) : objects(es.length))
const errText = (e: unknown) => (errorText(e, t('files.op.failed')))

function uniqueName(base: string, names: string[]) {
  const taken = new Set(names.map((n) => n.toLowerCase()))
  if (!taken.has(base.toLowerCase())) return base
  for (let i = 2; ; i++) if (!taken.has(`${base} ${i}`.toLowerCase())) return `${base} ${i}`
}

function Warnings({ warnings }: { warnings: string[] }) {
  const t = useT()
  return (
    <>
      {t('files.op.seedWarning')}
      <ul>
        {warnings.slice(0, 5).map((w) => (
          <li key={w}>{w}</li>
        ))}
        {warnings.length > 5 && <li>{t('files.op.andMore', { n: warnings.length - 5 })}</li>}
      </ul>
    </>
  )
}

export function FileOps({ op, root, dir, roots, names, onClose, onDone, onChanged }: FileOpsProps) {
  const t = useT()
  const [value, setValue] = useState(() => (op.type === 'rename' ? op.entry.name : op.type === 'mkdir' ? uniqueName(t('files.newFolder'), names) : ''))
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [warnings, setWarnings] = useState<string[] | null>(null)
  /** Moves still waiting for confirmation (the others are already done). */
  const [left, setLeft] = useState<FileEntry[] | null>(null)
  const [target, setTarget] = useState<MoveTarget>({ root, path: dir })
  /** Items already moved, over all attempts of this sheet. */
  const moved = useRef<FileEntry[]>([])
  const total = op.type === 'move' ? op.entries.length : 0

  // Cancelling after part of a move: the listing refreshes and the user learns what moved.
  const cancel = () => {
    if (op.type === 'move' && moved.current.length) {
      onDone({ toast: { text: t('files.op.movedPartial', { moved: moved.current.length, total }) } })
    } else onClose()
  }

  const wrap = async (fn: () => Promise<void>) => {
    setBusy(true)
    setError(undefined)
    try {
      await fn()
    } catch (e) {
      setError(errText(e))
    } finally {
      setBusy(false)
    }
  }

  // another build's server warns before touching some files
  const warn = !__LIBRARY_EDITION__ && warnings && (
    <Sheet
      title={t('files.op.stopSeeding')}
      message={<Warnings warnings={warnings} />}
      icon={<Glyph name="warning" size={44} strokeWidth={1.5} style={{ color: 'var(--orange)' }} />}
      busy={busy}
      onCancel={cancel}
      actions={[
        { label: t('common.cancelAction'), kind: 'cancel', run: cancel },
        { label: t('files.op.continue'), kind: 'destructive', run: () => void wrap(() => submit(true)) },
      ]}
    >
      {error && <p style={{ marginTop: 10, color: 'var(--red)', font: 'var(--t-footnote)' }}>{error}</p>}
    </Sheet>
  )

  async function submit(confirm: boolean) {
    switch (op.type) {
      case 'rename': {
        const r = await renameFile(root, op.entry.path, value, confirm)
        if (needsConfirm(r)) return setWarnings(r.warnings)
        onDone({ select: joinPath(parentPath(op.entry.path), value) })
        return
      }
      case 'mkdir': {
        await mkdir(root, dir, value)
        onDone({ select: joinPath(dir, value) })
        return
      }
      case 'move': {
        const pending: string[] = []
        const wait: FileEntry[] = []
        const failed: { e: FileEntry; msg: string }[] = []
        const before = moved.current.length
        try {
          for (const e of left ?? op.entries) {
            try {
              const r = await moveFile(root, e.path, target.root, target.path, confirm)
              if (needsConfirm(r)) {
                pending.push(...r.warnings)
                wait.push(e)
              } else moved.current.push(e)
            } catch (err) {
              failed.push({ e, msg: errText(err) })
            }
          }
        } finally {
          // Whatever happened, the folders on screen must show what really moved.
          if (moved.current.length > before) onChanged()
        }
        if (wait.length && !confirm) {
          setLeft([...wait, ...failed.map((f) => f.e)])
          return setWarnings(pending)
        }
        if (failed.length) {
          setLeft(failed.map((f) => f.e))
          setWarnings(null)
          setError(t('files.op.movedSome', { moved: moved.current.length, total, errors: failed.map((f) => t('files.op.itemError', { name: f.e.name, error: f.msg })).join('; ') }))
          return
        }
        const where = target.path ? target.path.slice(target.path.lastIndexOf('/') + 1) : (roots.find((x) => x.id === target.root)?.title ?? '')
        onDone({ toast: { text: t('files.op.movedTo', { what: what(op.entries), where }) } })
        return
      }
      case 'trash': {
        const r = await trashFiles(
          root,
          op.entries.map((e) => e.path),
          confirm,
        )
        if (r.needsConfirm) return setWarnings(r.warnings)
        const ids = r.trashed.map((x) => x.id)
        onDone({
          toast: {
            text: r.error ?? t('files.op.inTrash', { what: what(op.entries) }),
            action: ids.length
              ? {
                  label: t('files.undo'),
                  run: () =>
                    void Promise.allSettled(ids.map((id) => restoreTrash(id))).then((res) => {
                      const bad = res
                        .map((x, i) => (x.status === 'rejected' ? t('files.op.itemError', { name: r.trashed[i]!.name, error: errText(x.reason) }) : ''))
                        .filter(Boolean)
                      onDone({
                        toast: { text: bad.length ? t('files.op.restoreFailed', { errors: bad.join('; ') }) : t('files.op.restored', { what: what(op.entries) }) },
                      })
                    }),
                }
              : undefined,
          },
        })
      }
    }
  }

  if (warn) return warn

  switch (op.type) {
    case 'rename':
    case 'mkdir': {
      const problem = nameProblem(value)
      const same = op.type === 'rename' && value === op.entry.name
      return (
        <Sheet
          role="dialog"
          title={op.type === 'rename' ? t('common.rename') : t('files.newFolder')}
          icon={op.type === 'rename' ? <Thumb entry={op.entry} size={56} eager /> : <FileIcon entry={{ name: '', isDir: true, kind: 'folder' }} size={56} />}
          busy={busy}
          onCancel={onClose}
          actions={[
            { label: t('common.cancelAction'), kind: 'cancel', run: onClose },
            {
              label: op.type === 'rename' ? t('common.rename') : t('common.create'),
              kind: 'primary',
              disabled: Boolean(problem),
              run: () => (same ? onClose() : void wrap(() => submit(false))),
            },
          ]}
        >
          <NameField label={op.type === 'rename' ? t('files.op.newName') : t('files.op.folderName')} value={value} onChange={(v) => (setValue(v), setError(undefined))} error={error ?? (value ? problem : undefined)} />
        </Sheet>
      )
    }
    case 'move': {
      const here = target.root === root && target.path === dir
      return (
        <Sheet
          role="dialog"
          wide
          title={t('files.op.moveTitle', { what: what(op.entries) })}
          busy={busy}
          onCancel={cancel}
          actions={[
            { label: t('common.cancelAction'), kind: 'cancel', run: cancel },
            { label: t('files.op.move'), kind: 'primary', disabled: here, run: () => void wrap(() => submit(false)) },
          ]}
        >
          <MovePicker
            roots={roots}
            start={{ root, path: dir }}
            disabledPaths={new Set(op.entries.filter((e) => e.isDir).map((e) => e.path))}
            value={target}
            onChange={setTarget}
          />
          {error && <p style={{ marginTop: 10, color: 'var(--red)', font: 'var(--t-footnote)' }} role="alert">{error}</p>}
        </Sheet>
      )
    }
    case 'trash':
      return (
        <Sheet
          title={t('files.op.trashTitle', { what: what(op.entries) })}
          message={t('files.op.trashHint')}
          icon={<Glyph name="trash" size={44} strokeWidth={1.4} style={{ color: 'var(--label-2)' }} />}
          busy={busy}
          onCancel={onClose}
          actions={[
            { label: t('common.cancelAction'), kind: 'cancel', run: onClose },
            { label: t('files.op.toTrash'), kind: 'destructive', run: () => void wrap(() => submit(false)) },
          ]}
        >
          {error && <p style={{ marginTop: 10, color: 'var(--red)', font: 'var(--t-footnote)' }} role="alert">{error}</p>}
        </Sheet>
      )
  }
}
