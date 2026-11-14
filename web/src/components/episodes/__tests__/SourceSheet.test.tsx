import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { groupSources } from '../../../state/pickSource'
import { SourceSheet } from '../SourceSheet'
import { lib, online } from './fixtures'

const sources = [lib(), online('alpha', 'Voice A', { meta: { providerId: 'alpha', voice: 'Voice A', lang: 'ru', subs: ['en'] } }), online('alpha', 'Voice B', { kind: 'online' })]
const base = { episode: { number: 3, name: 'Third' }, groups: groupSources(sources, 'ru'), current: null, onPick: vi.fn(), onClose: vi.fn() }

describe('SourceSheet', () => {
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
  it('labels a pinned release apart from a tracker torrent', () => {
    const pinned = lib({ kind: 'pinned', ref: 'pin:1', provider: 'Tracker', meta: {} })
    const torrent = lib({ kind: 'torrent', ref: 'https://t/1', provider: 'Tracker', meta: { lang: 'ru' } })
    render(<SourceSheet {...base} groups={groupSources([torrent, pinned], 'ru')} />)
    const rows = screen.getAllByRole('button', { pressed: false })
    expect(rows[0]).toHaveTextContent('Tracker · Закреплённая')
    expect(rows[1]).toHaveTextContent('Tracker · Торрент')
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
