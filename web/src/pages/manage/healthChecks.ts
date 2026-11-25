// «Состояние»: what the page's endpoints say, as one list of checks. A check
// is a row — its name, a state, a short value — and, when something is to be
// done, what is wrong, how to fix it and the one action or command for it
// (the sheet a tap opens). The server's own checks (/api/admin/selfcheck) come
// as codes with figures; the rest is made here from what the page already
// asks (the versions, the addons and their helper services, the GPU, the
// network, the last scan, the folders). Pure: no request, no component.
import { APP_NAME } from '../../brand'
import type { CheckState, SelfCheckView, ServerCheck, TestableCheck } from '../../api/selfcheck'
import type { StorageView } from '../../api/storage'
import type { AddonInfo, CoreUpdate, HelperHealth, LibraryStatus, NetCheck, TranscodingView } from '../../api/types'
import { gpuProbeFailed } from '../../components/storage/GpuHint'
import { hasProblem } from '../../components/storage/StoragePerms'
import type { Key, TFunction } from '../../i18n'
import { ago, formatSize } from '../../state/format'
import { statusLine } from './TranscodingSettings'

/** The command that updates a Compose install (docs/updates.md). */
export const COMPOSE_UPDATE = 'docker compose pull && docker compose up -d'

export type Load<T> = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; value: T }

/** What a row shows: a server's state, or that it is still being asked. */
export type RowState = CheckState | 'checking'

export type GroupId = 'access' | 'disk' | 'video' | 'catalog' | 'extras' | 'versions' | 'helpers'

/** The one thing a sheet offers besides its command. */
export type FixAction =
  | { kind: 'link'; label: Key; to: string }
  | { kind: 'test'; label: Key; id: TestableCheck }
  | { kind: 'update'; label: Key; addon: AddonInfo }
  | { kind: 'recheck'; label: Key }

export interface CheckRow {
  id: string
  group: GroupId
  title: string
  state: RowState
  /** The short value at the row's trailing edge. */
  value: string
  /** The sheet's first line: what is wrong. Absent with `fix`, `command` and `action`: nothing to open. */
  detail?: string
  /** How to fix it, a sentence or two. */
  fix?: string
  /** The command (or the .env line) to copy. */
  command?: string
  action?: FixAction
}

/** A row opens a sheet when there is anything to say or do. */
export const tappable = (r: CheckRow) => r.detail !== undefined || r.fix !== undefined || r.command !== undefined || r.action !== undefined

/** The groups in the order of the page, and each one's rows in theirs. */
export const GROUPS: { id: GroupId; title: Key; rows: string[] }[] = [
  { id: 'access', title: 'sc.g.access', rows: ['selfcheck', 'connection', 'address', 'https'] },
  { id: 'disk', title: 'sc.g.disk', rows: ['folders', 'space', 'data', 'scan'] },
  { id: 'video', title: 'sc.g.video', rows: ['ffmpeg', 'gpu', 'intros'] },
  { id: 'catalog', title: 'sc.g.catalog', rows: ['tmdb', 'internet', 'clock'] },
  { id: 'extras', title: 'sc.g.extras', rows: ['tv', 'mail'] },
  { id: 'versions', title: 'health.versions', rows: ['server'] },
  { id: 'helpers', title: 'health.helpers', rows: [] },
]

const SERVER_GROUP: Record<string, GroupId> = {
  connection: 'access',
  address: 'access',
  https: 'access',
  data: 'disk',
  space: 'disk',
  ffmpeg: 'video',
  tmdb: 'catalog',
  clock: 'catalog',
  tv: 'extras',
  mail: 'extras',
}

/** The checks the server always answers: shown as «Проверяем» until it does. */
const ALWAYS = ['connection', 'address', 'https', 'ffmpeg', 'tmdb']

export interface HealthInput {
  server: Load<SelfCheckView>
  core: Load<CoreUpdate>
  /** null: this server has no addons. */
  addons: Load<AddonInfo[]> | null
  helpers: Load<HelperHealth[]> | null
  hw: Load<TranscodingView>
  storage: Load<StorageView>
  net: Load<NetCheck[]>
  library: Load<LibraryStatus>
  /** The addons whose web part did not load in this browser (id → why). */
  webFailed: ReadonlyMap<string, string>
  /** The browser's time zone («Europe/Berlin»), for the TZ line. */
  zone: string
}

const LINKS = {
  address: '/manage/settings/address',
  space: '/manage/stats',
  processing: '/manage/settings/processing',
  library: '/manage/settings/library',
  addons: '/manage/addons',
  tv: '/manage/channels/sources',
  mail: '/manage/settings/kindle',
}

const link = (label: Key, to: string): FixAction => ({ kind: 'link', label, to })

/** The words a catalog key says, or undefined when it has none (a newer server's code). */
function say(t: TFunction, key: string, params?: Record<string, string | number>): string | undefined {
  const s = t(key as Key, params)
  return s === key ? undefined : s
}

/** A server answer's figures as the words use them (bytes as «410 ГБ»). */
function figures(c: ServerCheck): Record<string, string | number> {
  const p: Record<string, string | number> = { ...c.params }
  if (typeof p.free === 'number') p.free = formatSize(p.free)
  return p
}

const loadingRow = (id: string, group: GroupId, title: string, t: TFunction): CheckRow => ({ id, group, title, state: 'checking', value: t('sc.checking') })

/** A row whose data did not come: the reason in its sheet, and a way to ask again. */
const failedRow = (id: string, group: GroupId, title: string, message: string, t: TFunction): CheckRow => ({
  id,
  group,
  title,
  state: 'unknown',
  value: t('sc.failed'),
  detail: message,
  action: { kind: 'recheck', label: 'sc.a.again' },
})

/** A server check as a row (null: a check this page does not know). */
function serverRow(c: ServerCheck, input: HealthInput, t: TFunction): CheckRow | null {
  const group = SERVER_GROUP[c.id]
  if (!group) return null
  const p = figures(c)
  const key = (kind: 'v' | 'd' | 'f') => `sc.${kind}.${c.id}.${c.code}`
  const settled = c.code === 'ok' || c.code === 'low' || c.code === 'full'
  let value: string | undefined
  switch (c.id) {
    case 'address':
      if (c.code === 'set') value = String(c.params?.host ?? '')
      break
    case 'data':
      if (settled) value = c.params?.free !== undefined ? t('sc.v.data.free', p) : t('sc.v.data.ok')
      break
    case 'space':
      value = settled ? t('sc.v.space.free', p) : t('sc.v.space.unavailable')
      break
    case 'clock':
      if (c.code === 'ok') value = String(c.params?.zone ?? '')
      break
    case 'ffmpeg':
      if (c.code === 'ok' || c.code === 'old') value = c.params?.version ? String(c.params.version) : t('sc.v.ffmpeg.ok')
      break
    case 'tv':
      if (c.code === 'ok') value = t('sc.v.tv.ok', { count: Number(c.params?.count ?? 0) })
      break
  }
  value ??= say(t, key('v'), p) ?? t('sc.failed')
  const row: CheckRow = { id: c.id, group, title: t(`sc.t.${c.id}` as Key), state: c.state, value }
  row.detail = say(t, key('d'), p)
  row.fix = say(t, key('f'), p) ?? (c.id === 'clock' && (c.code === 'ahead' || c.code === 'behind') ? t('sc.f.clock.skew') : undefined)
  const code = `${c.id}.${c.code}`
  switch (code) {
    case 'connection.proxy':
      row.command = `VAULT_TRUSTED_PROXIES=${c.params?.peer}`
      break
    case 'connection.host':
      row.command = `VAULT_TRUSTED_HOSTS=${c.params?.host}`
      break
    case 'data.open':
      row.command = `chmod 700 ${c.params?.path}`
      break
    case 'tmdb.unset':
      row.command = 'VAULT_TMDB_KEY=YOUR_KEY'
      break
    case 'clock.utc':
      row.command = `TZ=${input.zone}`
      break
  }
  if (c.id === 'address' && c.code !== 'set') row.action = link('sc.a.address', LINKS.address)
  else if (c.id === 'space' && (c.code === 'low' || c.code === 'full')) row.action = link('sc.a.space', LINKS.space)
  else if (c.id === 'tv' && c.code !== 'ok') row.action = link('sc.a.tv', LINKS.tv)
  else if (c.id === 'mail' && c.code !== 'works' && c.code !== 'set') row.action = link('sc.a.mail', LINKS.mail)
  else if (c.id === 'tmdb' && c.code !== 'unset') row.action = { kind: 'test', label: c.code === 'set' ? 'sc.a.testKey' : 'sc.a.again', id: 'tmdb' }
  else if (c.id === 'mail') row.action = { kind: 'test', label: c.code === 'set' ? 'sc.a.testMail' : 'sc.a.again', id: 'mail' }
  return row
}

function serverRows(input: HealthInput, t: TFunction): CheckRow[] {
  const s = input.server
  if (s.status === 'error') return [failedRow('selfcheck', 'access', t('sc.t.selfcheck'), s.message, t)]
  if (s.status === 'loading') return ALWAYS.map((id) => loadingRow(id, SERVER_GROUP[id]!, t(`sc.t.${id}` as Key), t))
  return (s.value.checks ?? []).flatMap((c) => serverRow(c, input, t) ?? [])
}

/** The library's folders: each the server can read and write, or the fix it words. */
function foldersRow(storage: Load<StorageView>, t: TFunction): CheckRow {
  const title = t('sc.t.folders')
  if (storage.status === 'loading') return loadingRow('folders', 'disk', title, t)
  if (storage.status === 'error') return failedRow('folders', 'disk', title, storage.message, t)
  const v = storage.value
  const row: CheckRow = { id: 'folders', group: 'disk', title, state: 'ok', value: t('sc.v.folders.ok') }
  if (v.root.status === 'missing' || v.root.status === 'notdir') {
    return { ...row, state: 'bad', value: t('sc.v.folders.missing'), detail: t('storage.rootMissing', { root: v.root.path }) }
  }
  const bad = [
    { d: v.root, path: v.root.path },
    ...v.folders.map((f) => ({ d: f, path: f.abs })),
    { d: v.transcode, path: v.transcode.path },
  ].filter((x) => hasProblem(x.d))
  if (bad.length === 0) return row
  const fix = v.fix
  const readonly = bad.some((x) => x.d.why === 'readonlyFS')
  const out: CheckRow = { ...row, state: 'bad', value: t('sc.v.folders.bad'), detail: t('sc.d.folders.bad', { list: bad.map((x) => x.path).join(', ') }) }
  // who the server runs as and who owns the folder: what «that user» below means
  const who = fix ? t(fix.owner ? 'storage.fix.who' : 'storage.fix.whoNoOwner', { uid: fix.uid, gid: fix.gid, owner: fix.owner ?? '' }) : ''
  if (fix?.chown) {
    out.fix = `${who} ${t(fix.docker ? 'storage.fix.chown' : 'storage.fix.chownHost')}`
    out.command = fix.chown
  } else if (fix?.composeUser) {
    out.fix = `${who} ${t('storage.fix.user')}`
    out.command = fix.composeUser
  } else if (readonly) {
    out.fix = t('storage.fix.readonly')
    out.command = 'docker compose up -d'
  } else if (fix?.mount?.length) {
    out.fix = t('storage.fix.mount')
    out.command = fix.mount.join('\n')
  } else {
    out.fix = t('storage.fix.other')
  }
  return out
}

/** The last scan: its failure, or nothing found in folders that have files. */
function scanRow(library: Load<LibraryStatus>, storage: Load<StorageView>, t: TFunction): CheckRow {
  const title = t('sc.t.scan')
  if (library.status === 'loading') return loadingRow('scan', 'disk', title, t)
  if (library.status === 'error') return failedRow('scan', 'disk', title, library.message, t)
  const s = library.value
  const row: CheckRow = { id: 'scan', group: 'disk', title, state: 'ok', value: '' }
  if (s.running) return { ...row, value: t('sc.v.scan.running') }
  if (s.queued) return { ...row, value: t('sc.v.scan.queued') }
  if (s.error) {
    return { ...row, state: 'bad', value: t('sc.v.scan.failed'), detail: t('sc.d.scan.failed', { error: s.error }), fix: t('sc.f.scan.failed'), action: link('sc.a.library', LINKS.library) }
  }
  if (!s.finishedAt) return { ...row, state: 'off', value: t('sc.v.scan.never') }
  const found = s.movies + s.episodes + s.albums + s.tracks + s.books + s.audiobooks + s.comics + s.files
  const filled = storage.status === 'ready' && [storage.value.root, ...storage.value.folders].some((d) => d.status === 'ok' && d.items > 0)
  if (found === 0 && filled) {
    return { ...row, state: 'warn', value: t('sc.v.scan.empty'), detail: t('sc.d.scan.empty'), fix: t('sc.f.scan.empty'), action: link('sc.a.library', LINKS.library) }
  }
  return { ...row, value: ago(s.finishedAt) }
}

/** The GPU: it works, the CPU does the work by choice or for want of a GPU, or the GPU failed its test. */
function gpuRow(hw: Load<TranscodingView>, t: TFunction): CheckRow {
  const title = t('sc.t.gpu')
  if (hw.status === 'loading') return loadingRow('gpu', 'video', title, t)
  if (hw.status === 'error') return failedRow('gpu', 'video', title, hw.message, t)
  const v = hw.value
  const s = v.status
  const row: CheckRow = { id: 'gpu', group: 'video', title, state: 'ok', value: s.mode === 'none' ? t('sc.v.gpu.cpu') : s.name }
  const open = link('sc.a.processing', LINKS.processing)
  if (s.mode === 'none') {
    if (s.requested === 'none') return row
    if (!gpuProbeFailed(s) && v.devices.length === 0) return { ...row, state: 'off', detail: statusLine(v, t), action: open }
    return { ...row, state: 'warn', value: t('sc.v.gpu.problem'), detail: statusLine(v, t), action: open }
  }
  const own = s.probes.find((x) => x.mode === s.mode)
  if (own && !own.ok) return { ...row, state: 'warn', value: t('sc.v.gpu.problem'), detail: statusLine(v, t), action: open }
  return row
}

/** Whether ffmpeg can analyse episodes (chromaprint): a feature, never a fault. */
function introsRow(hw: Load<TranscodingView>, t: TFunction): CheckRow | null {
  if (hw.status !== 'ready' || hw.value.fingerprint === undefined) return null
  const row: CheckRow = { id: 'intros', group: 'video', title: t('sc.t.intros'), state: 'ok', value: t('sc.v.intros.ok') }
  if (hw.value.fingerprint) return row
  return { ...row, state: 'off', value: t('sc.v.intros.missing'), detail: t('settings.tc.fingerprint.missing') }
}

/** The services the server reaches (GitHub, TMDB, MusicBrainz…), as one row. */
function internetRow(net: Load<NetCheck[]>, t: TFunction): CheckRow {
  const title = t('sc.t.internet')
  if (net.status === 'loading') return loadingRow('internet', 'catalog', title, t)
  if (net.status === 'error') return failedRow('internet', 'catalog', title, net.message, t)
  const failed = net.value.filter((n) => !n.ok)
  const row: CheckRow = { id: 'internet', group: 'catalog', title, state: 'ok', value: t('sc.v.internet.ok') }
  if (failed.length === 0) return row
  // «x509: certificate signed by unknown authority»: no root certificates, every HTTPS call fails
  const certs = failed.some((n) => /x509|certificate/i.test(n.error ?? ''))
  const code = certs ? 'certs' : failed.length === net.value.length ? 'none' : 'partial'
  return {
    ...row,
    state: code === 'partial' ? 'warn' : 'bad',
    value: t(`sc.v.internet.${code}` as Key),
    detail: t(`sc.d.internet.${code}` as Key, { names: failed.map((n) => n.name).join(', ') }),
    fix: t(`sc.f.internet.${code}` as Key),
  }
}

/** The server itself: its version, and the command that brings the newer one. */
function serverVersionRow(core: Load<CoreUpdate>, t: TFunction): CheckRow {
  if (core.status === 'loading') return loadingRow('server', 'versions', APP_NAME, t)
  if (core.status === 'error') return failedRow('server', 'versions', APP_NAME, core.message, t)
  const c = core.value
  const versioned = /^\d+\.\d+\.\d+/.test(c.version)
  const row: CheckRow = { id: 'server', group: 'versions', title: APP_NAME, state: 'ok', value: c.commit && !versioned ? `${c.version} (${c.commit.slice(0, 7)})` : c.version }
  if (c.checked && c.newer) {
    const latest = c.latest.replace(/^v/, '')
    return {
      ...row,
      state: 'warn',
      value: t('sc.v.server.newer', { version: latest }),
      detail: t('sc.d.server.newer', { version: latest }),
      fix: c.docker ? t('sc.f.server.update') : t('updates.howBinary'),
      command: c.docker ? COMPOSE_UPDATE : undefined,
    }
  }
  if (!c.checked) return { ...row, state: 'unknown', value: t('sc.v.server.unchecked'), detail: t('updates.checkFailed'), action: { kind: 'recheck', label: 'sc.a.again' } }
  return row
}

/** One addon: it runs, and is as new as it can be. */
function addonRow(a: AddonInfo, webFailed: ReadonlyMap<string, string>, t: TFunction): CheckRow {
  const row: CheckRow = { id: `addon:${a.id}`, group: 'versions', title: a.name, state: 'ok', value: a.version }
  const stopped = link('sc.a.addons', LINKS.addons)
  const newCore = { fix: t('sc.f.addon.needsCore'), command: COMPOSE_UPDATE }
  if (a.status === 'crashed') return { ...row, state: 'bad', value: t('sc.v.addon.crashed'), detail: t('addons.status.crashed'), fix: t('sc.f.addon.stopped'), action: stopped }
  if (a.status === 'incompatible') {
    return { ...row, state: 'bad', value: a.minCore ? t('sc.v.addon.needsCore') : t('sc.v.addon.incompatible'), detail: a.minCore ? t('addons.needsCore', { version: a.minCore }) : t('addons.status.incompatible'), ...newCore }
  }
  if (a.pinFailed) return { ...row, state: 'warn', value: t('sc.v.addon.pinFailed'), detail: t('sc.d.addon.pinFailed'), action: stopped }
  if (a.status === 'stopped') return { ...row, state: 'off', value: t('addons.status.stopped') }
  if (a.status === 'starting') return { ...row, state: 'checking', value: t('addons.status.starting') }
  if (webFailed.has(a.id)) return { ...row, state: 'warn', value: t('sc.v.addon.webFailed'), detail: t('health.webFailed', { error: webFailed.get(a.id) ?? '' }), action: stopped }
  if (a.update && !a.pinned) {
    return { ...row, state: 'warn', value: t('sc.v.addon.update', { version: a.update }), detail: t('addons.versionUpdate', { version: a.version, update: a.update }), action: { kind: 'update', label: 'addons.update', addon: a } }
  }
  if (a.updateState === 'needsCore') return { ...row, state: 'warn', value: t('sc.v.addon.needsCore'), detail: a.updateError, ...newCore }
  if (a.updateState === 'failed') {
    return { ...row, state: 'unknown', value: t('sc.v.addon.unchecked'), detail: a.updateError ? t('updates.addonCheckFailed', { reason: a.updateError }) : t('updates.checkFailed'), action: { kind: 'recheck', label: 'sc.a.again' } }
  }
  if (a.updateState === 'checking') return { ...row, state: 'checking', value: t('updates.addonChecking') }
  return row
}

/** A helper service nobody gave an address (an optional one): not set up, never a fault. */
const unset = (h: HelperHealth) => h.source === 'none' && !h.address

/** Why a helper does not answer, in the page's words (the raw error only when nothing better is known). */
export function helperProblemText(t: TFunction, h: HelperHealth): string {
  switch (h.problem) {
    case 'dns':
      return t('health.helperDns', { name: h.name })
    case 'refused':
      return t('health.helperRefused', { name: h.name })
    case 'timeout':
      return t('health.helperTimeout', { name: h.name })
    case 'status':
      return t('health.helperStatus', { name: h.name })
    case 'auth':
      return t('health.helperAuth', { name: h.name })
    case 'unset':
      return t('health.helperUnset', { name: h.name })
  }
  return t('health.helperDown', { name: h.name, error: h.error ?? '' })
}

/** The helper services of the addons: each answers, or says why not. */
function helperRows(helpers: Load<HelperHealth[]>, addons: Load<AddonInfo[]> | null, t: TFunction): CheckRow[] {
  const title = t('health.helpers')
  if (helpers.status === 'loading') return []
  if (helpers.status === 'error') return [failedRow('helpers', 'helpers', title, helpers.message, t)]
  const rows = helpers.value
    .filter((h) => !unset(h))
    .map((h): CheckRow => {
      const owner = addons?.status === 'ready' ? addons.value.find((a) => a.id === h.addon)?.name : undefined
      const name = h.title || h.name
      const row: CheckRow = { id: `helper:${h.addon}:${h.name}`, group: 'helpers', title: owner ? `${name} · ${owner}` : name, state: 'ok', value: t('sc.v.helper.ok') }
      if (h.ok) return row
      const word = h.problem === 'dns' ? 'dns' : h.problem === 'auth' ? 'auth' : h.problem === 'unset' ? 'unset' : 'down'
      const out: CheckRow = { ...row, state: 'bad', value: t(`sc.v.helper.${word}` as Key), detail: helperProblemText(t, h), action: link('sc.a.addons', LINKS.addons) }
      // the core has no Docker socket: a compose helper that does not answer may have been stopped for lack of memory
      const compose = !h.custom && h.source !== 'settings' && h.source !== 'override'
      if (compose && (h.problem === 'dns' || h.problem === 'refused' || h.problem === 'timeout' || h.problem === 'other' || !h.problem)) {
        out.fix = t('sc.f.helper.memory')
        out.command = `docker compose up -d ${h.name}`
        delete out.action
      }
      return out
    })
  const off = helpers.value.filter(unset)
  if (off.length > 0) {
    rows.push({ id: 'helpers-unset', group: 'helpers', title: t('health.helpersUnset'), state: 'off', value: off.map((h) => h.title || h.name).join(', ') })
  }
  return rows
}

/** Every row of the page, in the order of its groups. */
export function buildRows(input: HealthInput, t: TFunction): CheckRow[] {
  const all = new Map<string, CheckRow>()
  const put = (r: CheckRow | null) => r && all.set(r.id, r)
  for (const r of serverRows(input, t)) put(r)
  put(foldersRow(input.storage, t))
  put(scanRow(input.library, input.storage, t))
  put(gpuRow(input.hw, t))
  put(introsRow(input.hw, t))
  put(internetRow(input.net, t))
  put(serverVersionRow(input.core, t))
  const rows = GROUPS.flatMap((g) => g.rows.flatMap((id) => all.get(id) ?? []))
  if (input.addons) {
    const a = input.addons
    if (a.status === 'loading') rows.push(loadingRow('addons', 'versions', t('addons.title'), t))
    else if (a.status === 'error') rows.push(failedRow('addons', 'versions', t('addons.title'), a.message, t))
    else rows.push(...a.value.map((x) => addonRow(x, input.webFailed, t)))
  }
  if (input.helpers) rows.push(...helperRows(input.helpers, input.addons, t))
  return rows
}

/** How many rows are broken and how many want attention (what is still being asked, off or unknown counts for neither). */
export function tally(rows: CheckRow[]): { bad: number; warn: number; checking: number } {
  return {
    bad: rows.filter((r) => r.state === 'bad').length,
    warn: rows.filter((r) => r.state === 'warn').length,
    checking: rows.filter((r) => r.state === 'checking').length,
  }
}
