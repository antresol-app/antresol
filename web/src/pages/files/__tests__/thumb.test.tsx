import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, render } from '@testing-library/react'
import type { FileEntry } from '../../../api/types'

// A controllable IntersectionObserver: the test decides what is on screen.
type CB = (items: { target: Element; isIntersecting: boolean }[]) => void
let ioCallback: CB | null = null
class FakeIO {
  constructor(cb: CB) {
    ioCallback = cb
  }
  observe() {}
  unobserve() {}
  disconnect() {}
}

const video: FileEntry = { name: 'a.mkv', path: 'a.mkv', isDir: false, size: 1, mtime: '', kind: 'video', thumb: '/api/files/thumb?root=movies&path=a.mkv&v=1' }

async function load() {
  vi.resetModules()
  vi.stubGlobal('IntersectionObserver', FakeIO)
  return (await import('../Thumb')).Thumb
}

describe('lazy thumbnails', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    ioCallback = null
  })

  it('start loading near the viewport and are cancelled when scrolled away before they arrive', async () => {
    const Thumb = await load()
    const { container } = render(<Thumb entry={video} size={84} />)
    const box = container.firstElementChild!
    expect(container.querySelector('img')).toBeNull()
    act(() => ioCallback!([{ target: box, isIntersecting: true }]))
    const img = container.querySelector('img')!
    expect(img.getAttribute('src')).toContain('&w=')
    act(() => ioCallback!([{ target: box, isIntersecting: false }]))
    expect(container.querySelector('img')).toBeNull()
    // The request was cancelled (src cleared), not left running in the background.
    expect(img.getAttribute('src')).toBe('')
  })

  it('are cancelled on unmount', async () => {
    const Thumb = await load()
    const { container, unmount } = render(<Thumb entry={video} size={84} />)
    act(() => ioCallback!([{ target: container.firstElementChild!, isIntersecting: true }]))
    const img = container.querySelector('img')!
    unmount()
    expect(img.getAttribute('src')).toBe('')
  })
})
