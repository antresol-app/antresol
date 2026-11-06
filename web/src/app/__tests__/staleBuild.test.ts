import { afterEach, describe, expect, it, vi } from 'vitest'
import { reloadForNewBuild } from '../staleBuild'

describe('a chunk of an old build', () => {
  afterEach(() => sessionStorage.clear())

  it('reloads once, then not again within the minute', () => {
    const reload = vi.fn()
    vi.spyOn(window, 'location', 'get').mockReturnValue({ ...window.location, reload } as Location)
    expect(reloadForNewBuild()).toBe(true)
    expect(reloadForNewBuild()).toBe(false)
    expect(reload).toHaveBeenCalledTimes(1)
  })
})

describe('chunk', () => {
  it('an import that resolves to nothing is a failure, and a later load asks again', async () => {
    const { chunk } = await import('../lazyChunk')
    let n = 0
    const c = chunk(async () => (n++ === 0 ? (undefined as unknown as { A: number }) : { A: 1 }))
    await expect(c.load()).rejects.toThrow()
    expect(c.loaded()).toBeUndefined()
    await expect(c.load()).resolves.toEqual({ A: 1 })
    expect(c.loaded()).toEqual({ A: 1 })
  })

  it('a background prefetch that fails is ignored: no reload', async () => {
    const reload = vi.fn()
    vi.spyOn(window, 'location', 'get').mockReturnValue({ ...window.location, reload } as Location)
    const { prefetchSections } = await import('../chunks')
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new TypeError('blip'))),
    )
    expect(() => prefetchSections()).not.toThrow()
    await new Promise((r) => setTimeout(r, 0))
    expect(reload).not.toHaveBeenCalled()
    vi.unstubAllGlobals()
  })
})
