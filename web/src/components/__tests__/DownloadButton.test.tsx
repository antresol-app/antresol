import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { createRef } from 'react'
import { MemoryRouter, Route, Routes } from 'react-router'
import userEvent from '@testing-library/user-event'
import { DownloadButton } from '../DownloadButton'
import { percent } from '../../ui/StatusRing'
import type { TriggerProps } from '../../ui/Menu'

describe('percent', () => {
  it('is 0 for an unknown size', () => expect(percent(1, 0)).toBe(0))
  it('rounds down, never reaching 100 early', () => expect(percent(999, 1000)).toBe(99))
  it('clamps to 0..100', () => {
    expect(percent(5, 1)).toBe(100)
    expect(percent(-5, 10)).toBe(0)
  })
})

describe('DownloadButton', () => {
  it('idle: a plain download button without a ring', () => {
    render(<DownloadButton state={{ kind: 'idle' }} />)
    expect(screen.getByRole('button', { name: 'Загрузить' })).toBeInTheDocument()
    expect(screen.queryByRole('progressbar')).toBeNull()
  })

  it('href: a download link styled as the same round button', () => {
    render(<DownloadButton state={{ kind: 'idle' }} href="/api/download/5" />)
    const link = screen.getByRole('link', { name: 'Загрузить' })
    expect(link).toHaveAttribute('href', '/api/download/5')
    expect(link).toHaveAttribute('download')
    expect(link.className).toBe(render(<DownloadButton state={{ kind: 'idle' }} />).getByRole('button').className)
    expect(screen.queryByRole('progressbar')).toBeNull()
  })

  it('to: a router link to a page, never a download', async () => {
    render(
      <MemoryRouter initialEntries={['/search']}>
        <Routes>
          <Route path="/search" element={<DownloadButton state={{ kind: 'idle' }} size="small" to="/title/series/83880" />} />
          <Route path="/title/series/83880" element={<h1>Страница сериала</h1>} />
        </Routes>
      </MemoryRouter>,
    )
    const link = screen.getByRole('link', { name: 'Загрузить' })
    expect(link).toHaveAttribute('href', '/title/series/83880')
    expect(link).not.toHaveAttribute('download')
    await userEvent.click(link)
    expect(await screen.findByRole('heading', { name: 'Страница сериала' })).toBeInTheDocument()
  })

  it('idle: the label prop overrides the name', () => {
    render(<DownloadButton state={{ kind: 'idle' }} label="Выбрать релиз" />)
    expect(screen.getByRole('button', { name: 'Выбрать релиз' })).toBeInTheDocument()
  })

  it('starting: an indeterminate ring', () => {
    render(<DownloadButton state={{ kind: 'starting' }} />)
    expect(screen.getByRole('button', { name: 'Загружается' })).toBeInTheDocument()
    const bar = screen.getByRole('progressbar')
    expect(bar).not.toHaveAttribute('aria-valuenow')
  })

  it('active: a determinate ring with the percent', () => {
    render(<DownloadButton state={{ kind: 'active', done: 42, size: 100 }} />)
    expect(screen.getByRole('button', { name: 'Загружается, 42 %' })).toBeInTheDocument()
    const bar = screen.getByRole('progressbar')
    expect(bar).toHaveAttribute('aria-valuenow', '42')
    expect(bar).toHaveAttribute('aria-valuemin', '0')
    expect(bar).toHaveAttribute('aria-valuemax', '100')
  })

  it('the progressbar has its own name and a value text, distinct from the button', () => {
    render(<DownloadButton state={{ kind: 'active', done: 42, size: 100 }} />)
    const bar = screen.getByRole('progressbar', { name: 'Прогресс загрузки' })
    expect(bar).toHaveAttribute('aria-valuetext', '42 %')
    expect(screen.getByRole('button')).toHaveAccessibleName('Загружается, 42 %')
    expect(bar).not.toHaveAccessibleName('Загружается, 42 %')
  })

  it('starting: the progressbar has no value text', () => {
    render(<DownloadButton state={{ kind: 'starting' }} />)
    expect(screen.getByRole('progressbar')).not.toHaveAttribute('aria-valuetext')
  })

  it('paused: keeps the value, says so in the name', () => {
    render(<DownloadButton state={{ kind: 'paused', done: 42, size: 100 }} />)
    expect(screen.getByRole('button', { name: 'Пауза, 42 %' })).toBeInTheDocument()
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '42')
  })

  it('done: a check mark, no ring', () => {
    render(<DownloadButton state={{ kind: 'done' }} />)
    const b = screen.getByRole('button', { name: 'Загружено' })
    expect(b.querySelector('svg path[d="m5 12.5 4.5 4.5L19 7.5"]')).not.toBeNull()
    expect(screen.queryByRole('progressbar')).toBeNull()
  })

  it('done: a click starts nothing, the check is not greyed out', () => {
    const onClick = vi.fn()
    render(<DownloadButton state={{ kind: 'done' }} onClick={onClick} />)
    const b = screen.getByRole('button', { name: 'Загружено' })
    fireEvent.click(b)
    expect(onClick).not.toHaveBeenCalled()
    expect(b).toHaveAttribute('aria-disabled', 'true')
    expect(b).not.toBeDisabled() // :disabled would grey it out
  })

  it('calls onClick and respects disabled', () => {
    const onClick = vi.fn()
    const { rerender } = render(<DownloadButton state={{ kind: 'idle' }} onClick={onClick} />)
    fireEvent.click(screen.getByRole('button'))
    expect(onClick).toHaveBeenCalledTimes(1)
    rerender(<DownloadButton state={{ kind: 'idle' }} onClick={onClick} disabled />)
    expect(screen.getByRole('button')).toBeDisabled()
  })

  it('works as a Popover trigger: spreads triggerProps and forwards the ref', () => {
    const ref = createRef<HTMLButtonElement>()
    const onClick = vi.fn()
    const tp: TriggerProps = { ref, 'aria-haspopup': 'menu', 'aria-expanded': true, 'aria-controls': 'm1', onClick }
    render(<DownloadButton state={{ kind: 'idle' }} triggerProps={tp} />)
    const b = screen.getByRole('button')
    expect(ref.current).toBe(b)
    expect(b).toHaveAttribute('aria-haspopup', 'menu')
    expect(b).toHaveAttribute('aria-expanded', 'true')
    fireEvent.click(b)
    expect(onClick).toHaveBeenCalledTimes(1)
  })
})
