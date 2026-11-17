import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { registerAddon, resetAddonsForTests } from '../../../addons/registry'
import { session } from '../../../state/session'
import { groupSources } from '../../../state/pickSource'
import { SourceSheet } from '../SourceSheet'
import { lib, online } from './fixtures'

const sources = [lib(), online('alpha', 'Voice A', { meta: { providerId: 'alpha', voice: 'Voice A', lang: 'ru', subs: ['en'] } }), online('alpha', 'Voice B', { kind: 'online' })]
const base = { episode: { number: 3, name: 'Third' }, groups: groupSources(sources, 'ru'), current: null, onPick: vi.fn(), onClose: vi.fn() }

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

describe('SourceSheet', () => {
  beforeEach(() => {
    session.reset()
    resetAddonsForTests()
  })
  afterEach(() => vi.unstubAllGlobals())
  it('is titled by the episode and marks the current pick', () => {
    render(<SourceSheet {...base} current={sources[1]!} />)
    expect(screen.getByRole('dialog', { name: 'Серия 3 · Third' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /alpha/, pressed: true })).toBeInTheDocument()
    expect(screen.getAllByRole('button', { pressed: true })).toHaveLength(1)
  })
  it('shows a subtitles badge', () => {
    render(<SourceSheet {...base} />)
    expect(screen.getByText('Субтитры')).toBeInTheDocument()
  })
  it('offers download only for a pinned source with onDownload', async () => {
    const pinned = lib({ kind: 'pinned', ref: 'pin:1', meta: {} })
    const onDownload = vi.fn()
    const { rerender } = render(<SourceSheet {...base} groups={groupSources([pinned], 'ru')} />)
    expect(screen.queryByRole('button', { name: 'Скачать серию' })).toBeNull()
    rerender(<SourceSheet {...base} groups={groupSources([pinned], 'ru')} onDownload={onDownload} />)
    await userEvent.click(screen.getByRole('button', { name: 'Скачать серию' }))
    expect(onDownload).toHaveBeenCalledWith(pinned)
    rerender(<SourceSheet {...base} onDownload={onDownload} />)
    expect(screen.queryByRole('button', { name: 'Скачать серию' })).toBeNull()
  })
  it('a kind\'s label comes from its addon', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ id: 2, name: 'Test', avatar: 'grad-1', hasPassword: false, isAdmin: false, isKid: false, maxRating: null })))
    await session.login(2)
    registerAddon('fake', {
      episodeActions: [{ kinds: ['alpha', 'beta'], labels: { alpha: 'kind.alpha' }, order: { alpha: 10, beta: 30 }, use: () => null }],
      strings: { ru: { 'kind.alpha': 'Альфа' }, en: { 'kind.alpha': 'Alpha' } },
    })
    const a = lib({ kind: 'alpha', ref: 'a:1', provider: 'Site', meta: {} })
    const b = lib({ kind: 'beta', ref: 'b:1', provider: 'Site', meta: {} })
    const u = lib({ kind: 'gamma', ref: 'g:1', provider: 'Site', meta: {} })
    render(<SourceSheet {...base} groups={groupSources([u, b, a], 'ru', { alpha: 10, beta: 30 })} />)
    const rows = screen.getAllByRole('button', { pressed: false })
    expect(rows[0]).toHaveTextContent('Site · Альфа')
    // no label from the addon, or no addon: the kind's own name
    expect(rows[1]).toHaveTextContent('Site · beta')
    expect(rows[2]).toHaveTextContent('Site · gamma')
  })
  it('a kid gets no addon\'s label', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ id: 3, name: 'Kid', avatar: 'grad-1', hasPassword: false, isAdmin: false, isKid: true, maxRating: null })))
    await session.login(3)
    registerAddon('fake', {
      episodeActions: [{ kinds: ['alpha'], labels: { alpha: 'kind.alpha' }, use: () => null }],
      strings: { ru: { 'kind.alpha': 'Альфа' }, en: { 'kind.alpha': 'Alpha' } },
    })
    render(<SourceSheet {...base} groups={groupSources([lib({ kind: 'alpha', ref: 'a:1', provider: 'Site', meta: {} })], 'ru')} />)
    expect(screen.getByRole('button', { pressed: false })).toHaveTextContent('Site · alpha')
  })
  it('capitalises the language heading', () => {
    render(<SourceSheet {...base} />)
    expect(screen.getByText('Русский')).toBeInTheDocument()
  })
  it('hides other sources without the handler', () => {
    const { rerender } = render(<SourceSheet {...base} />)
    expect(screen.queryByRole('button', { name: 'Другие источники…' })).toBeNull()
    rerender(<SourceSheet {...base} onOther={() => {}} />)
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'Другие источники…' })).toBeInTheDocument()
  })
})
