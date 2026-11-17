// The pages whose edition branches went to the addons carry no edition
// flag any more: both builds run the same code there, and what only some
// servers have comes from an addon's slots.
import { readdirSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const SRC = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const read = (rel: string) => readFileSync(path.join(SRC, rel), 'utf8')
const FLAG = '__LIBRARY_EDITION__'

const dirFiles = (rel: string, only?: string[]) =>
  readdirSync(path.join(SRC, rel))
    .filter((n) => /\.tsx?$/.test(n) && (!only || only.includes(n)))
    .map((n) => `${rel}/${n}`)

const WHOLE = [
  'app/Shell.tsx',
  'components/TopNav.tsx',
  'components/DiskMeter.tsx',
  'pages/Home.tsx',
  'pages/Browse.tsx',
  ...dirFiles('pages/books'),
  ...dirFiles('pages/music', ['MusicHome.tsx', 'Albums.tsx', 'TrackList.tsx']),
  'pages/Search.tsx',
  'state/libraryScope.ts',
  'state/pendingCards.ts',
  'pages/manage/Stats.tsx',
  'pages/manage/Requests.tsx',
  // 2f: «Переезд» and «Импорт» draw an addon's slots (migrateSources, importSources)
  'pages/manage/Migrate.tsx',
  'pages/manage/Import.tsx',
  'player/usePlayback.ts',
]

describe('edition flag', () => {
  it('is gone from the files whose branches went to the addons', () => {
    expect(WHOLE.filter((f) => read(f).includes(FLAG))).toEqual([])
  })

  it('is not on the pending lines of Card.tsx', () => {
    const lines = read('components/Card.tsx')
      .split('\n')
      .filter((l) => /pendingHref|const pending =/.test(l))
    expect(lines.length).toBeGreaterThan(0)
    expect(lines.filter((l) => l.includes(FLAG))).toEqual([])
    const key = read('components/Card.tsx').match(/export const cardKey =[\s\S]*?\n\n/)?.[0] ?? ''
    const pendingBranch = key.split('\n').filter((l) => l.includes('pending'))
    expect(pendingBranch.filter((l) => l.includes(FLAG))).toEqual([])
  })

  it('leaves NotifySheet no topic of the downloads', () => {
    expect(read('components/NotifySheet.tsx')).not.toMatch(/case 'downloads'|\bdownloads:/)
  })
})
