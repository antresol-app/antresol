import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { lazy, Suspense, type JSX } from 'react'
import { ChunkBoundary } from '../ChunkBoundary'
import { chunk, lazyFrom } from '../lazyChunk'

describe('lazy section', () => {
  it('shows a spinner while loading and a retry when the chunk fails', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    let fail: (e: Error) => void = () => {}
    const Broken = lazy(() => new Promise<{ default: () => null }>((_, reject) => (fail = reject)))
    render(
      <ChunkBoundary>
        <Broken />
      </ChunkBoundary>,
    )
    expect(screen.getByLabelText('Загрузка')).toHaveAttribute('aria-busy', 'true')
    fail(new Error('Failed to fetch dynamically imported module'))
    expect(await screen.findByRole('alert')).toHaveTextContent('Не удалось открыть раздел')
    expect(screen.getByRole('button', { name: 'Повторить' })).toBeInTheDocument()
  })
})

describe('lazyFrom', () => {
  it('suspends while the chunk loads, and draws at once once it is in', async () => {
    let done: (m: { Page: () => JSX.Element }) => void = () => {}
    const c = chunk(() => new Promise<{ Page: () => JSX.Element }>((r) => (done = r)))
    const First = lazyFrom(c, (m) => m.Page)
    const Second = lazyFrom(c, (m) => m.Page)
    const first = render(
      <Suspense fallback={<p>loading</p>}>
        <First />
      </Suspense>,
    )
    expect(screen.getByText('loading')).toBeInTheDocument()
    done({ Page: () => <p>page</p> })
    expect(await screen.findByText('page')).toBeInTheDocument()
    first.unmount()
    render(
      <Suspense fallback={<p>loading</p>}>
        <Second />
      </Suspense>,
    )
    expect(screen.getByText('page')).toBeInTheDocument()
    expect(screen.queryByText('loading')).not.toBeInTheDocument()
  })
})
