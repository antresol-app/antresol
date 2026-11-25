// «Состояние»'s rows: what each answer of the server, each part the page asks,
// is as a row — its state, its short value, and what its sheet says and offers.
import { describe, expect, it } from 'vitest'
import type { ServerCheck } from '../../../api/selfcheck'
import { en } from '../../../i18n/en'
import { ru } from '../../../i18n/ru'
import { t, translate, type TFunction } from '../../../i18n'
import { formatSize } from '../../../state/format'
import type { HelperHealth } from '../../../api/types'
import { buildRows, COMPOSE_UPDATE, GROUPS, tally, tappable, type CheckRow } from '../healthChecks'
import { addon, core, healthy, library, network, readonlyStorage, ready, serverView, storage, transcoding } from './healthFixtures'

const rowsOf = (over = {}, tr: TFunction = t) => buildRows(healthy(over), tr)
const row = (rows: CheckRow[], id: string) => rows.find((r) => r.id === id)
const english: TFunction = (k, p) => translate('en', k, p)

/** One server check among the healthy ones. */
const answer = (c: ServerCheck, tr: TFunction = t) => row(rowsOf({ server: serverView([c]) }, tr), c.id)

describe('a healthy server', () => {
  it('is all in order: every row ok or off, none open, the groups in their order', () => {
    const rows = rowsOf()
    expect(tally(rows)).toEqual({ bad: 0, warn: 0, checking: 0 })
    expect(rows.filter((r) => r.state !== 'ok' && r.state !== 'off')).toEqual([])
    // a row that is fine says only its value
    for (const r of rows.filter((x) => x.state === 'ok' && x.id !== 'tmdb')) expect(tappable(r), r.id).toBe(false)
    const groups = rows.map((r) => r.group)
    expect(groups).toEqual([...groups].sort((a, b) => GROUPS.findIndex((g) => g.id === a) - GROUPS.findIndex((g) => g.id === b)))
    expect(rows.map((r) => r.id)).toEqual(['connection', 'address', 'https', 'folders', 'space', 'data', 'scan', 'ffmpeg', 'gpu', 'intros', 'tmdb', 'internet', 'clock', 'mail', 'server'])
  })

  it('says the short value of each', () => {
    const rows = rowsOf()
    const value = (id: string) => row(rows, id)?.value
    expect(value('connection')).toBe('Из дома')
    expect(value('address')).toBe('media.example.org')
    expect(value('https')).toBe('Без шифрования')
    expect(value('data')).toBe(`${formatSize(38 * 2 ** 30)} свободно`)
    expect(value('space')).toBe(`${formatSize(410 * 2 ** 30)} свободно`)
    expect(value('ffmpeg')).toBe('7.1')
    expect(value('gpu')).toBe('VA-API')
    expect(value('tmdb')).toBe('Ключ задан')
    expect(value('clock')).toBe('Europe/Berlin')
    expect(value('internet')).toBe('Доступен')
    expect(value('folders')).toBe('Запись работает')
    expect(value('server')).toBe('0.8.28')
    expect(row(rows, 'mail')).toMatchObject({ state: 'off', value: 'Не настроена' })
  })

  it('has the same rows in English, with words and no leftover placeholder', () => {
    for (const r of rowsOf({}, english)) {
      for (const text of [r.title, r.value, r.detail, r.fix]) {
        if (text !== undefined) expect(text, r.id).not.toMatch(/^sc\.|\{\w+\}/)
      }
    }
    expect(row(rowsOf({}, english), 'connection')?.value).toBe('At home')
    expect(row(rowsOf({}, english), 'data')?.value).toBe(`${formatSize(38 * 2 ** 30)} free`)
  })
})

describe('the server\'s own checks', () => {
  it('a proxy nobody listed: the line to put in .env, not a fault of the page', () => {
    const r = answer({ id: 'connection', state: 'warn', code: 'proxy', params: { peer: '172.18.0.1' } })!
    expect(r).toMatchObject({ state: 'warn', value: 'Чужой прокси', command: 'VAULT_TRUSTED_PROXIES=172.18.0.1' })
    expect(r.detail).toContain('172.18.0.1')
    expect(r.fix).toMatch(/\.env/)
    expect(tappable(r)).toBe(true)
  })

  it('a foreign name offers the line for VAULT_TRUSTED_HOSTS; a proxy without addresses only the setting to turn on', () => {
    expect(answer({ id: 'connection', state: 'warn', code: 'host', params: { host: 'media.example.org' } })?.command).toBe('VAULT_TRUSTED_HOSTS=media.example.org')
    const bare = answer({ id: 'connection', state: 'warn', code: 'bare', params: { peer: '172.18.0.1' } })!
    expect(bare.command).toBeUndefined()
    expect(bare.fix).toMatch(/X-Forwarded-For/)
  })

  it('https the server does not see, and plain http through a public name', () => {
    expect(answer({ id: 'https', state: 'warn', code: 'lost' })).toMatchObject({ state: 'warn', value: 'Не виден', fix: expect.stringContaining('X-Forwarded-Proto') })
    expect(answer({ id: 'https', state: 'warn', code: 'plainPublic', params: { host: 'media.example.org' } })?.detail).toContain('media.example.org')
  })

  it('no public address is not a fault, and opens the page where it is set', () => {
    const r = answer({ id: 'address', state: 'off', code: 'unset' })!
    expect(r).toMatchObject({ state: 'off', value: 'Не задан', action: { kind: 'link', to: '/manage/settings/address' } })
  })

  it('the settings folder: no write, a full disk, open to others (with the command)', () => {
    expect(answer({ id: 'data', state: 'bad', code: 'denied', params: { path: '/config' } })).toMatchObject({ state: 'bad', value: 'Нет записи' })
    const low = answer({ id: 'data', state: 'warn', code: 'low', params: { free: 2 ** 30 } })!
    expect(low).toMatchObject({ state: 'warn', value: `${formatSize(2 ** 30)} свободно` })
    expect(low.detail).toContain(formatSize(2 ** 30))
    expect(answer({ id: 'data', state: 'warn', code: 'open', params: { path: '/config', mode: '0755' } })?.command).toBe('chmod 700 /config')
  })

  it('free space: the figure in the row, the percent and where it goes in the sheet', () => {
    const r = answer({ id: 'space', state: 'warn', code: 'low', params: { free: 14 * 2 ** 30, percent: 88 } })!
    expect(r).toMatchObject({ state: 'warn', value: `${formatSize(14 * 2 ** 30)} свободно`, action: { kind: 'link', to: '/manage/stats' } })
    expect(r.detail).toBe(`Медиадиск заполнен на 88 %, свободно ${formatSize(14 * 2 ** 30)}.`)
    expect(answer({ id: 'space', state: 'unknown', code: 'offline' })).toMatchObject({ state: 'unknown', value: 'Недоступен' })
  })

  it('ffmpeg: missing names the tool, an old one the version', () => {
    expect(answer({ id: 'ffmpeg', state: 'bad', code: 'missing', params: { tool: 'ffprobe' } })).toMatchObject({ state: 'bad', value: 'Не найден', detail: expect.stringContaining('ffprobe') })
    expect(answer({ id: 'ffmpeg', state: 'warn', code: 'old', params: { version: '6.0' } })).toMatchObject({ state: 'warn', value: '6.0' })
    expect(answer({ id: 'ffmpeg', state: 'ok', code: 'ok' })?.value).toBe('Есть')
  })

  it('the TMDB key: none is a fix with the line to write, set is tested by a button, a refused one is bad', () => {
    expect(answer({ id: 'tmdb', state: 'warn', code: 'unset' })).toMatchObject({ state: 'warn', value: 'Нет ключа', command: 'VAULT_TMDB_KEY=YOUR_KEY' })
    expect(answer({ id: 'tmdb', state: 'ok', code: 'set' })).toMatchObject({ state: 'ok', value: 'Ключ задан', action: { kind: 'test', id: 'tmdb', label: 'sc.a.testKey' } })
    expect(answer({ id: 'tmdb', state: 'ok', code: 'accepted', at: '2026-11-25T14:00:00Z' })).toMatchObject({ value: 'Ключ принят', action: { kind: 'test', id: 'tmdb', label: 'sc.a.again' } })
    const bad = answer({ id: 'tmdb', state: 'bad', code: 'rejected' })!
    expect(bad).toMatchObject({ state: 'bad', value: 'Ключ не принят', action: { kind: 'test', id: 'tmdb' } })
    expect(bad.detail).toContain('401')
    expect(answer({ id: 'tmdb', state: 'unknown', code: 'unreachable' })?.state).toBe('unknown')
  })

  it('the clock: skew in minutes, and the zone line from the browser\'s own zone', () => {
    expect(answer({ id: 'clock', state: 'warn', code: 'ahead', params: { minutes: 5 } })).toMatchObject({ state: 'warn', value: 'Сервер спешит', detail: 'Часы сервера спешат на 5 мин.' })
    const utc = answer({ id: 'clock', state: 'warn', code: 'utc', params: { browser: '+1:00' } })!
    expect(utc.command).toBe('TZ=Europe/Berlin')
    expect(utc.detail).toContain('+1:00')
  })

  it('TV sources: the count, or the one that fails with its reason, pointing at the sources', () => {
    expect(answer({ id: 'tv', state: 'ok', code: 'ok', params: { count: 3 } })?.value).toBe('3 источника')
    const r = answer({ id: 'tv', state: 'warn', code: 'failing', params: { name: 'Провайдер', error: 'HTTP 403', count: 1 } })!
    expect(r).toMatchObject({ state: 'warn', value: 'Не обновляется', detail: 'Источник «Провайдер»: HTTP 403.', action: { kind: 'link', to: '/manage/channels/sources' } })
  })

  it('mail: off and quiet until set; set is tested by a button; a refused login points at the settings', () => {
    expect(answer({ id: 'mail', state: 'off', code: 'unset' })).toMatchObject({ state: 'off', value: 'Не настроена', action: { kind: 'link', to: '/manage/settings/kindle' } })
    expect(answer({ id: 'mail', state: 'warn', code: 'unsetKindle' })?.state).toBe('warn')
    expect(answer({ id: 'mail', state: 'ok', code: 'set', params: { host: 'smtp.example.com' } })).toMatchObject({ state: 'ok', value: 'Настроена', action: { kind: 'test', id: 'mail', label: 'sc.a.testMail' } })
    expect(answer({ id: 'mail', state: 'ok', code: 'works', params: { host: 'smtp.example.com' } })).toMatchObject({ value: 'Работает', action: { kind: 'test', id: 'mail', label: 'sc.a.again' } })
    const auth = answer({ id: 'mail', state: 'bad', code: 'auth', params: { host: 'smtp.example.com' } })!
    expect(auth).toMatchObject({ state: 'bad', action: { kind: 'link', to: '/manage/settings/kindle' } })
    expect(auth.fix).toMatch(/Gmail/)
  })

  it('an id or a code a newer server adds is not a crash', () => {
    const rows = rowsOf({ server: serverView([{ id: 'unheard', state: 'bad', code: 'x' }, { id: 'ffmpeg', state: 'warn', code: 'strange' }]) })
    expect(row(rows, 'unheard')).toBeUndefined()
    expect(row(rows, 'ffmpeg')).toMatchObject({ state: 'warn', value: 'Не удалось проверить' })
  })

  it('while it asks, the always-answered checks are «Проверяем»; when it fails, one row says so and offers to ask again', () => {
    const asking = rowsOf({ server: { status: 'loading' } })
    expect(asking.filter((r) => r.state === 'checking').map((r) => r.id)).toEqual(expect.arrayContaining(['connection', 'address', 'https', 'ffmpeg', 'tmdb']))
    expect(tally(asking).checking).toBeGreaterThan(0)
    const failed = rowsOf({ server: { status: 'error', message: 'Нет связи с сервером' } })
    expect(row(failed, 'selfcheck')).toMatchObject({ state: 'unknown', detail: 'Нет связи с сервером', action: { kind: 'recheck' } })
    expect(row(failed, 'connection')).toBeUndefined()
  })
})

describe('every code the server can answer has its words, in both languages', () => {
  // [state, code, figures], as internal/api/selfcheck_checks.go answers them
  const answers: Record<string, [string, string, Record<string, string | number>?][]> = {
    connection: [['ok', 'home'], ['ok', 'away'], ['warn', 'proxy', { peer: '172.18.0.1' }], ['warn', 'bare', { peer: '172.18.0.1' }], ['warn', 'host', { host: 'media.example.org' }]],
    https: [['ok', 'secure'], ['ok', 'plainHome'], ['warn', 'lost'], ['warn', 'plainPublic', { host: 'media.example.org' }]],
    address: [['off', 'unset'], ['ok', 'set', { host: 'media.example.org' }], ['warn', 'plain', { host: 'media.example.org' }], ['warn', 'invalid']],
    data: [
      ['ok', 'ok', { path: '/config', free: 2 ** 36 }],
      ['bad', 'denied', { path: '/config' }],
      ['bad', 'readonlyFS', { path: '/config' }],
      ['bad', 'other', { path: '/config' }],
      ['bad', 'full', { free: 2 ** 20 }],
      ['warn', 'low', { free: 2 ** 30 }],
      ['warn', 'open', { path: '/config', mode: '0755' }],
    ],
    space: [['ok', 'ok', { free: 2 ** 40, percent: 40 }], ['warn', 'low', { free: 2 ** 33, percent: 88 }], ['bad', 'full', { free: 2 ** 30, percent: 98 }], ['unknown', 'offline'], ['unknown', 'unreadable']],
    ffmpeg: [['ok', 'ok', { version: '7.1' }], ['warn', 'old', { version: '6.0' }], ['bad', 'missing', { tool: 'ffmpeg' }], ['bad', 'broken', { tool: 'ffprobe' }]],
    tmdb: [['warn', 'unset'], ['ok', 'set'], ['ok', 'accepted'], ['bad', 'rejected'], ['unknown', 'unreachable'], ['warn', 'failed', { status: 500 }]],
    clock: [['ok', 'ok', { zone: 'UTC' }], ['warn', 'ahead', { minutes: 5 }], ['warn', 'behind', { minutes: 5 }], ['warn', 'utc', { browser: '+1:00' }]],
    tv: [['ok', 'ok', { count: 2 }], ['warn', 'failing', { name: 'A', error: 'HTTP 403', count: 1 }], ['warn', 'pending', { name: 'A', count: 1 }], ['warn', 'stale', { name: 'A', count: 1 }]],
    mail: [
      ['off', 'unset'],
      ['warn', 'unsetKindle'],
      ['ok', 'set', { host: 'smtp.example.com' }],
      ['ok', 'works', { host: 'smtp.example.com' }],
      ['bad', 'auth', { host: 'smtp.example.com' }],
      ['bad', 'noTLS', { host: 'smtp.example.com' }],
      ['bad', 'tls', { host: 'smtp.example.com' }],
      ['bad', 'unreachable', { host: 'smtp.example.com', port: 587 }],
      ['warn', 'failed', { host: 'smtp.example.com' }],
    ],
  }

  for (const [id, list] of Object.entries(answers)) {
    for (const [state, code, params] of list) {
      it(`${id}/${code}`, () => {
        for (const [name, tr, cat] of [['ru', t, ru], ['en', english, en]] as const) {
          const r = answer({ id, state, code, params } as ServerCheck, tr)!
          expect(r, `${id}/${code} in ${name}`).toBeDefined()
          for (const text of [r.title, r.value, r.detail, r.fix]) {
            if (text !== undefined) expect(text, `${id}/${code} in ${name}`).not.toMatch(/^sc\.|\{\w+\}/)
          }
          // a row that is not fine says what is wrong, and what to do
          if (state !== 'ok') expect(r.detail, `${id}/${code} in ${name}: what is wrong`).toBeTruthy()
          expect(`sc.t.${id}` in cat).toBe(true)
        }
      })
    }
  }
})

describe('the folders', () => {
  it('writable: only its value', () => {
    expect(row(rowsOf(), 'folders')).toMatchObject({ state: 'ok', value: 'Запись работает' })
    expect(tappable(row(rowsOf(), 'folders')!)).toBe(false)
  })

  it('one the server may not write to: the path, and the chown line to copy', () => {
    const r = row(rowsOf({ storage: ready(readonlyStorage) }), 'folders')!
    expect(r).toMatchObject({ state: 'bad', value: 'Нет записи', command: 'sudo chown -R 1000:1000 /srv/media/movies' })
    expect(r.detail).toContain('/media/movies')
    expect(r.fix).toBe('Сервер работает от пользователя 1000:1000, владелец папки — 1001:1001. Отдайте папки этому пользователю. Выполните на сервере в папке с compose.yml:')
  })

  it('without a chown line it says what else to do: the user, a read-only mount, a missing mount', () => {
    const base = { ...readonlyStorage, fix: { paths: ['/media/movies'], docker: true, uid: 1000, gid: 1000 } }
    const compose = row(rowsOf({ storage: ready({ ...base, fix: { ...base.fix, composeUser: 'user: "1001:1001"' } }) }), 'folders')!
    expect(compose.command).toBe('user: "1001:1001"')
    const ro = { ...base, folders: [{ ...base.folders[0]!, why: 'readonlyFS' as const }] }
    expect(row(rowsOf({ storage: ready(ro) }), 'folders')).toMatchObject({ command: 'docker compose up -d' })
    const mount = row(rowsOf({ storage: ready({ ...base, fix: { ...base.fix, mount: ['/media/movies'] } }) }), 'folders')!
    expect(mount.command).toBe('/media/movies')
    expect(row(rowsOf({ storage: ready(base) }), 'folders')?.fix).toMatch(/место/)
  })

  it('no media folder at all', () => {
    const r = row(rowsOf({ storage: ready({ ...storage, root: { path: '/media', status: 'missing', items: 0 } }) }), 'folders')!
    expect(r).toMatchObject({ state: 'bad', value: 'Нет папки' })
    expect(r.detail).toContain('/media')
  })

  it('a failed or running load is a row of its own state', () => {
    expect(row(rowsOf({ storage: { status: 'loading' } }), 'folders')?.state).toBe('checking')
    expect(row(rowsOf({ storage: { status: 'error', message: 'Не удалось проверить папки' } }), 'folders')).toMatchObject({ state: 'unknown', detail: 'Не удалось проверить папки' })
  })
})

describe('the last scan', () => {
  it('says when, not a sentence', () => {
    expect(row(rowsOf(), 'scan')).toMatchObject({ state: 'ok', value: 'только что' })
  })

  it('running, queued, never run', () => {
    expect(row(rowsOf({ library: ready({ ...library, running: true }) }), 'scan')?.value).toBe('Идёт')
    expect(row(rowsOf({ library: ready({ ...library, queued: true }) }), 'scan')?.value).toBe('В очереди')
    expect(row(rowsOf({ library: ready({ ...library, finishedAt: undefined }) }), 'scan')).toMatchObject({ state: 'off', value: 'Ещё не было' })
  })

  it('a failed scan is broken, and its reason is in the sheet', () => {
    const r = row(rowsOf({ library: ready({ ...library, error: 'нет доступа' }) }), 'scan')!
    expect(r).toMatchObject({ state: 'bad', value: 'Не удалось', detail: 'Последнее сканирование не удалось: нет доступа', action: { kind: 'link', to: '/manage/settings/library' } })
  })

  it('nothing found while the folders have files is worth fixing; with empty folders it is not', () => {
    const none = { ...library, movies: 0, episodes: 0 }
    expect(row(rowsOf({ library: ready(none) }), 'scan')).toMatchObject({ state: 'warn', value: 'Не найдено' })
    const empty = { ...storage, root: { ...storage.root, items: 0 }, folders: [{ ...storage.folders[0]!, items: 0 }] }
    expect(row(rowsOf({ library: ready(none), storage: ready(empty) }), 'scan')?.state).toBe('ok')
  })
})

describe('the GPU and the intros', () => {
  it('the CPU by choice is fine; no GPU passed is not a fault; a GPU that failed its test is', () => {
    const cpu = (over = {}) => ({ ...transcoding, devices: [], status: { ...transcoding.status, mode: 'none' as const, name: 'software', probes: [], ...over } })
    expect(row(rowsOf({ hw: ready(cpu({ requested: 'none' })) }), 'gpu')).toMatchObject({ state: 'ok', value: 'Процессор' })
    const none = row(rowsOf({ hw: ready(cpu()) }), 'gpu')!
    expect(none).toMatchObject({ state: 'off', value: 'Процессор' })
    expect(tally([none])).toEqual({ bad: 0, warn: 0, checking: 0 })
    const failed = row(rowsOf({ hw: ready({ ...cpu({ problem: { kind: 'permission', mode: 'vaapi', device: '/dev/dri/renderD128', gid: 105 } }), devices: ['/dev/dri/renderD128'] }) }), 'gpu')!
    expect(failed).toMatchObject({ state: 'warn', value: 'Не работает', action: { kind: 'link', to: '/manage/settings/processing' } })
    expect(failed.detail).toContain('group_add')
    const own = row(rowsOf({ hw: ready({ ...transcoding, status: { ...transcoding.status, probes: [{ mode: 'vaapi', ok: false }] } }) }), 'gpu')!
    expect(own.state).toBe('warn')
  })

  it('ffmpeg without chromaprint is off, never a fault', () => {
    expect(row(rowsOf(), 'intros')).toMatchObject({ state: 'ok', value: 'Работает' })
    const r = row(rowsOf({ hw: ready({ ...transcoding, fingerprint: false }) }), 'intros')!
    expect(r).toMatchObject({ state: 'off', value: 'Нет chromaprint' })
    expect(rowsOf({ hw: ready({ ...transcoding, fingerprint: undefined }) }).some((x) => x.id === 'intros')).toBe(false)
  })
})

describe('the internet', () => {
  const down = (...names: string[]) => network.map((n) => (names.includes(n.name) ? { ...n, ok: false, error: 'context deadline exceeded' } : n))

  it('all reached; some not (named); none; no root certificates', () => {
    expect(row(rowsOf(), 'internet')).toMatchObject({ state: 'ok', value: 'Доступен' })
    const some = row(rowsOf({ net: ready(down('MusicBrainz')) }), 'internet')!
    expect(some).toMatchObject({ state: 'warn', value: 'Не всё доступно', detail: 'Сервер не достучался до: MusicBrainz.' })
    expect(row(rowsOf({ net: ready(down('GitHub', 'MusicBrainz')) }), 'internet')).toMatchObject({ state: 'bad', value: 'Нет связи' })
    const certs = network.map((n) => ({ ...n, ok: false, error: 'tls: failed to verify certificate: x509: certificate signed by unknown authority' }))
    const c = row(rowsOf({ net: ready(certs) }), 'internet')!
    expect(c).toMatchObject({ state: 'bad', value: 'Нет сертификатов' })
    expect(c.fix).toMatch(/ca-certificates/)
  })
})

describe('the versions', () => {
  it('the server: its version; a newer one is the command to update; a failed check says so', () => {
    expect(row(rowsOf(), 'server')).toMatchObject({ state: 'ok', value: '0.8.28', title: 'Antresol' })
    const newer = row(rowsOf({ core: ready({ ...core, latest: 'v0.8.29', newer: true }) }), 'server')!
    expect(newer).toMatchObject({ state: 'warn', value: 'Доступна 0.8.29', command: COMPOSE_UPDATE })
    const binary = row(rowsOf({ core: ready({ ...core, latest: 'v0.8.29', newer: true, docker: false }) }), 'server')!
    expect(binary.command).toBeUndefined()
    expect(row(rowsOf({ core: ready({ ...core, checked: false, latest: '' }) }), 'server')).toMatchObject({ state: 'unknown', action: { kind: 'recheck' } })
    expect(row(rowsOf({ core: ready({ ...core, version: 'dev', commit: '0123456789abcdef' }) }), 'server')?.value).toBe('dev (0123456)')
  })

  const withAddons = (list: ReturnType<typeof addon>[], webFailed = new Map<string, string>()) => rowsOf({ addons: ready(list), helpers: ready([]), webFailed })

  it('each addon is a row after the server, its version when it runs', () => {
    const rows = withAddons([addon({ id: 'a', name: 'Первое' }), addon({ id: 'b', name: 'Второе', version: '0.2.0' })])
    const ids = rows.filter((r) => r.group === 'versions').map((r) => r.id)
    expect(ids).toEqual(['server', 'addon:a', 'addon:b'])
    expect(row(rows, 'addon:b')).toMatchObject({ state: 'ok', value: '0.2.0', title: 'Второе' })
  })

  it('crashed, not fitting this server, a newer release, a check that failed, a web part that did not load', () => {
    const rows = withAddons(
      [
        addon({ id: 'crashed', status: 'crashed' }),
        addon({ id: 'old', status: 'incompatible', minCore: '0.8.30' }),
        addon({ id: 'newer', update: '0.2.0', version: '0.1.0' }),
        addon({ id: 'pinned', update: '0.2.0', pinned: true }),
        addon({ id: 'needs', updateState: 'needsCore', updateError: 'Версии 0.5.0 нужен более новый сервер' }),
        addon({ id: 'failed', updateState: 'failed', updateError: 'GitHub не ответил вовремя' }),
        addon({ id: 'web' }),
        addon({ id: 'checking', updateState: 'checking' }),
        addon({ id: 'off', status: 'stopped' }),
      ],
      new Map([['web', 'timeout']]),
    )
    expect(row(rows, 'addon:crashed')).toMatchObject({ state: 'bad', action: { kind: 'link', to: '/manage/addons' } })
    const old = row(rows, 'addon:old')!
    expect(old).toMatchObject({ state: 'bad', value: 'Обновите сервер', command: COMPOSE_UPDATE })
    expect(old.detail).toBe('Нужна версия сервера 0.8.30 или новее — обновите сервер')
    const newer = row(rows, 'addon:newer')!
    expect(newer).toMatchObject({ state: 'warn', value: 'Доступна 0.2.0', detail: 'Версия 0.1.0 → 0.2.0', action: { kind: 'update', label: 'addons.update' } })
    expect(row(rows, 'addon:pinned')?.state).toBe('ok')
    expect(row(rows, 'addon:needs')).toMatchObject({ state: 'warn', detail: 'Версии 0.5.0 нужен более новый сервер', command: COMPOSE_UPDATE })
    expect(row(rows, 'addon:failed')).toMatchObject({ state: 'unknown', value: 'Не проверено', detail: 'Не удалось проверить: GitHub не ответил вовремя', action: { kind: 'recheck' } })
    expect(row(rows, 'addon:web')).toMatchObject({ state: 'warn', detail: 'Веб-часть не загрузилась: timeout' })
    expect(row(rows, 'addon:checking')?.state).toBe('checking')
    expect(row(rows, 'addon:off')).toMatchObject({ state: 'off', value: 'Выключено' })
  })

  it('while the addons load there is one row; when they fail, one row to ask again', () => {
    expect(row(rowsOf({ addons: { status: 'loading' }, helpers: { status: 'loading' } }), 'addons')?.state).toBe('checking')
    expect(row(rowsOf({ addons: { status: 'error', message: 'Не удалось получить расширения' }, helpers: { status: 'loading' } }), 'addons')).toMatchObject({ state: 'unknown', action: { kind: 'recheck' } })
  })
})

describe('the helper services', () => {
  const helper = (over: Partial<HelperHealth>): HelperHealth => ({ addon: 'one', name: 'solver', address: 'http://solver:8191', ok: true, ...over })
  const rowsWith = (helpers: HelperHealth[]) => rowsOf({ addons: ready([addon({ id: 'one', name: 'Расширение' })]), helpers: ready(helpers) })
  const hrow = (rows: CheckRow[], name: string) => rows.find((r) => r.id === `helper:one:${name}`)

  it('one that answers is a quiet row, named by the manifest\'s title and its addon', () => {
    const rows = rowsWith([helper({ title: 'Решатель' })])
    expect(hrow(rows, 'solver')).toMatchObject({ state: 'ok', value: 'Отвечает', title: 'Решатель · Расширение', group: 'helpers' })
  })

  it('one that does not: broken, with the page\'s words, and for a compose service the memory hint and the command to start it', () => {
    const r = hrow(rowsWith([helper({ ok: false, problem: 'refused' })]), 'solver')!
    expect(r).toMatchObject({ state: 'bad', value: 'Не отвечает', command: 'docker compose up -d solver' })
    expect(r.detail).toMatch(/Не отвечает: по этому адресу ничего не слушает/)
    expect(r.fix).toMatch(/mem_limit/)
    expect(hrow(rowsWith([helper({ ok: false, problem: 'dns' })]), 'solver')?.value).toBe('Не найдена')
    expect(hrow(rowsWith([helper({ ok: false, problem: 'auth' })]), 'solver')?.value).toBe('Не пускает')
  })

  it('an address of the admin\'s: no memory hint (it is not a compose service), only the way to the addons', () => {
    const r = hrow(rowsWith([helper({ ok: false, problem: 'refused', custom: true, address: '', source: 'override' })]), 'solver')!
    expect(r.command).toBeUndefined()
    expect(r.fix).toBeUndefined()
    expect(r.action).toMatchObject({ kind: 'link', to: '/manage/addons' })
  })

  it('the ones nobody set up are one off row, never a fault', () => {
    const rows = rowsWith([helper({ name: 'warp', title: 'Туннель', address: '', source: 'none', ok: false, problem: 'unset' }), helper({ name: 'gate', address: '', source: 'none', ok: false, problem: 'unset' })])
    expect(rows.filter((r) => r.group === 'helpers')).toEqual([expect.objectContaining({ id: 'helpers-unset', state: 'off', value: 'Туннель, gate' })])
    expect(tally(rows).bad).toBe(0)
  })
})

describe('the tally', () => {
  it('counts what is broken and what wants attention; off, unknown and a check under way are neither', () => {
    const rows = rowsOf({
      server: serverView([
        { id: 'connection', state: 'warn', code: 'proxy', params: { peer: '172.18.0.1' } },
        { id: 'tmdb', state: 'bad', code: 'rejected' },
        { id: 'mail', state: 'off', code: 'unset' },
        { id: 'space', state: 'unknown', code: 'offline' },
      ]),
      storage: { status: 'loading' },
    })
    expect(tally(rows)).toEqual({ bad: 1, warn: 1, checking: 1 })
  })
})
