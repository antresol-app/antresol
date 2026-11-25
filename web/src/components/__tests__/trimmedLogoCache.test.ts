// The trimmed logos are blob URLs: only the most recent ones are kept, the older ones are revoked.
import { renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const W = 10
let made = 0
const revoked: string[] = []

let useTrimmedLogo: typeof import('../useTrimmedImage').useTrimmedLogo

beforeEach(async () => {
  vi.resetModules() // the cache is the module's: each test starts with an empty one
  ;({ useTrimmedLogo } = await import('../useTrimmedImage'))
  made = 0
  revoked.length = 0
  // an image that loads at once, 10×10 with one opaque pixel: trimming crops it, so a blob URL is made
  vi.stubGlobal(
    'Image',
    class {
      decoding = ''
      naturalWidth = W
      naturalHeight = W
      onload: (() => void) | null = null
      onerror: (() => void) | null = null
      set src(_: string) {
        queueMicrotask(() => this.onload?.())
      }
    },
  )
  const data = new Uint8ClampedArray(W * W * 4)
  data[(5 * W + 5) * 4 + 3] = 255
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(() => ({ drawImage() {}, getImageData: () => ({ data }) }) as unknown as CanvasRenderingContext2D)
  vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation((cb) => cb(new Blob(['x'])))
  URL.createObjectURL = vi.fn(() => `blob:logo-${++made}`)
  URL.revokeObjectURL = vi.fn((u: string) => void revoked.push(u))
})
afterEach(() => vi.unstubAllGlobals())

describe('trimmed logo cache', () => {
  it('keeps the most recent logos and revokes the oldest blob URLs', async () => {
    const first = renderHook(() => useTrimmedLogo('/logo/0'))
    await waitFor(() => expect(first.result.current.url).toBe('blob:logo-1'))
    for (let i = 1; i <= 24; i++) {
      const r = renderHook(() => useTrimmedLogo(`/logo/${i}`))
      await waitFor(() => expect(r.result.current.url).toBeTruthy())
    }
    // 25 logos made, 24 kept: the first one went
    await waitFor(() => expect(revoked).toEqual(['blob:logo-1']))
  })

  it('a logo used again is the newest, so it outlives older ones', async () => {
    for (let i = 0; i < 24; i++) {
      const r = renderHook(() => useTrimmedLogo(`/logo/${i}`))
      await waitFor(() => expect(r.result.current.url).toBeTruthy())
    }
    const again = renderHook(() => useTrimmedLogo('/logo/0')) // cached: no new blob
    await waitFor(() => expect(again.result.current.url).toBe('blob:logo-1'))
    const r = renderHook(() => useTrimmedLogo('/logo/24'))
    await waitFor(() => expect(r.result.current.url).toBeTruthy())
    await waitFor(() => expect(revoked).toEqual(['blob:logo-2'])) // /logo/1, the oldest unused
  })
})
