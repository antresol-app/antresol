// «Отдельные истории»: the profile's managed accounts, a history of its own
// for a shared device («Гостиная»). Created by hand here or by the switch
// «Отдельная история на этом устройстве» in «Устройства»; renamed, moved
// into the profile («Перенести») or deleted with their history. An admin
// sees every profile's (with its owner) and may delete any; renaming and
// moving are the owner's, and so is an account's own app password: a TV's
// Infuse or music app signs in with the profile's name and it, and writes
// that history.
import { useState } from 'react'
import { createManagedAccount, deleteAnyManagedAccount, deleteManagedAccount, errorText, mergeManagedAccount, renameManagedAccount } from '../api/client'
import type { ManagedAccount } from '../api/types'
import { t as tr, useT } from '../i18n'
import { session } from '../state/session'
import { AppPasswordSheet } from './AppPasswordSheet'
import { IconButton } from '../ui/IconButton'
import { Menu, MenuItem } from '../ui/Menu'
import { InlineForm } from '../ui/InlineForm'
import { GroupedSection, ListRow } from '../ui/List'
import { Sheet } from '../ui/Sheet'
import { TextField } from '../ui/TextField'
import styles from './DevicesSheet.module.css'

export interface ManagedAccountsSectionProps {
  items: ManagedAccount[]
  /** The signed-in profile: an account with another owner (an admin's view) is only deleted here. */
  me: number
  /** The list changed on the server: load it (and the devices) again. */
  onChanged: () => void
}

type Ask = {
  kind: 'merge' | 'delete' | 'rename'
  account: ManagedAccount
} | null

const errText = (e: unknown) => errorText(e, tr('comp.serverUnreachable'))

export function ManagedAccountsSection({ items, me, onChanged }: ManagedAccountsSectionProps) {
  const t = useT()
  const [name, setName] = useState('')
  const [rename, setRename] = useState('')
  const [ask, setAsk] = useState<Ask>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [password, setPassword] = useState<ManagedAccount | null>(null)
  const user = session.get().user

  const run = (p: Promise<unknown>, after?: () => void) => {
    setBusy(true)
    setError('')
    p.then(() => {
      after?.()
      onChanged()
    })
      .catch((e: unknown) => setError(errText(e)))
      .finally(() => setBusy(false))
  }

  const mine = (a: ManagedAccount) => !a.owner || a.owner.id === me
  const devicesOf = (a: ManagedAccount) =>
    a.devices.length > 0
      ? a.devices
          .map((d) => d.name || d.model)
          .filter(Boolean)
          .join(', ')
      : t('comp.managed.unused')

  const create = () => {
    if (!name.trim()) return
    run(createManagedAccount(name.trim()), () => setName(''))
  }

  const confirm = () => {
    if (!ask) return
    const { kind, account } = ask
    setAsk(null)
    if (kind === 'merge') run(mergeManagedAccount(account.id))
    else if (kind === 'delete') run(mine(account) ? deleteManagedAccount(account.id) : deleteAnyManagedAccount(account.id))
    else if (rename.trim()) run(renameManagedAccount(account.id, rename.trim()))
  }

  return (
    <>
      <GroupedSection id="managed-accounts" title={t('comp.managed.title')} footer={t('comp.managed.footer')}>
        {items.map((a) => (
          <ListRow key={a.id} label={a.name} hint={mine(a) ? devicesOf(a) : `${a.owner?.name ?? ''} · ${devicesOf(a)}`}>
            {/* one «⋯» per row, destructive last (DESIGN.md: a list of items
                with actions): four buttons do not fit a phone's row */}
            <Menu
              label={t('comp.managed.actionsAria', { name: a.name })}
              placement="bottom-end"
              trigger={(p) => <IconButton {...p} size="xs" icon="ellipsis" disabled={busy} label={t('comp.managed.actionsAria', { name: a.name })} />}
            >
              {(close) => (
                <>
                  {mine(a) && (
                    <>
                      <MenuItem
                        label={t('comp.managed.rename')}
                        onSelect={() => {
                          close(false)
                          setRename(a.name)
                          setAsk({ kind: 'rename', account: a })
                        }}
                      />
                      <MenuItem
                        label={t('comp.managed.appPassword')}
                        onSelect={() => {
                          close(false)
                          setPassword(a)
                        }}
                      />
                      <MenuItem
                        label={t('comp.managed.merge')}
                        ariaLabel={t('comp.managed.mergeAria', { name: a.name })}
                        onSelect={() => {
                          close(false)
                          setAsk({ kind: 'merge', account: a })
                        }}
                      />
                    </>
                  )}
                  <MenuItem
                    label={t('comp.managed.delete')}
                    ariaLabel={t('comp.managed.deleteAria', { name: a.name })}
                    destructive
                    onSelect={() => {
                      close(false)
                      setAsk({ kind: 'delete', account: a })
                    }}
                  />
                </>
              )}
            </Menu>
          </ListRow>
        ))}
        {/* the field's button says it: the row's name is for the screen reader only */}
        <ListRow wide label={<span className="visually-hidden">{t('comp.managed.create')}</span>}>
          <InlineForm
            field={{
              label: t('comp.managed.name'),
              placeholder: t('comp.managed.namePlaceholder'),
              value: name,
              maxLength: 64,
              onChange: (e) => setName(e.target.value),
            }}
            submit={t('comp.managed.create')}
            onSubmit={create}
            loading={busy}
            disabled={!name.trim()}
          />
        </ListRow>
      </GroupedSection>
      {error && (
        <p className={styles.error} role="alert">
          {error}
        </p>
      )}

      {password && (
        <AppPasswordSheet
          userName={user?.name ?? ''}
          passwordless={user ? !user.hasPassword : false}
          managed={{ id: password.id, name: password.name }}
          onClose={() => setPassword(null)}
        />
      )}
      {ask?.kind === 'merge' && (
        <Sheet
          title={t('comp.managed.mergeTitle', { name: ask.account.name })}
          message={t('comp.managed.mergeText')}
          onCancel={() => setAsk(null)}
          actions={[
            {
              label: t('common.cancelAction'),
              kind: 'cancel',
              run: () => setAsk(null),
            },
            {
              label: t('comp.devices.offMerge'),
              kind: 'primary',
              run: confirm,
            },
          ]}
        />
      )}
      {ask?.kind === 'delete' && (
        <Sheet
          title={t('comp.managed.deleteTitle', { name: ask.account.name })}
          message={t('comp.managed.deleteText')}
          onCancel={() => setAsk(null)}
          actions={[
            {
              label: t('common.cancelAction'),
              kind: 'cancel',
              run: () => setAsk(null),
            },
            {
              label: t('comp.managed.delete'),
              kind: 'destructive',
              run: confirm,
            },
          ]}
        />
      )}
      {ask?.kind === 'rename' && (
        <Sheet
          title={t('comp.managed.renameTitle')}
          role="dialog"
          onCancel={() => setAsk(null)}
          actions={[
            {
              label: t('common.cancelAction'),
              kind: 'cancel',
              run: () => setAsk(null),
            },
            {
              label: t('comp.managed.save'),
              kind: 'primary',
              disabled: !rename.trim(),
              run: confirm,
            },
          ]}
        >
          <TextField label={t('comp.managed.name')} hideLabel value={rename} maxLength={64} onChange={(e) => setRename(e.target.value)} />
        </Sheet>
      )}
    </>
  )
}
