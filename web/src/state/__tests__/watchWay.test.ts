// The remembered way of «Смотреть ▾» per profile (state/watchWay.ts): ids
// are `online` or an addon way's `<addonId>:<label>`.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { pickWay, readWatchWay, useWatchWay, writeWatchWay } from '../watchWay'

describe('watchWay storage', () => {
  beforeEach(() => localStorage.clear())

  it('round-trips per profile', () => {
    writeWatchWay(1, 'fake:way')
    writeWatchWay(2, 'online')
    expect(readWatchWay(1)).toBe('fake:way')
    expect(readWatchWay(2)).toBe('online')
    expect(readWatchWay(3)).toBeNull()
    expect(localStorage.getItem('antresol.watchWay.1')).toBe('fake:way')
  })

  it('reads null when storage throws', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    expect(readWatchWay(1)).toBeNull()
  })

  it('does not throw when writing to blocked storage', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    expect(() => writeWatchWay(1, 'online')).not.toThrow()
  })

  it('reads an empty stored value as null', () => {
    localStorage.setItem('antresol.watchWay.1', '')
    expect(readWatchWay(1)).toBeNull()
  })
})

describe('useWatchWay', () => {
  beforeEach(() => localStorage.clear())

  it('a choice reaches every mounted consumer of the profile, not the others', () => {
    const a = renderHook(() => useWatchWay(1))
    const b = renderHook(() => useWatchWay(1))
    const other = renderHook(() => useWatchWay(2))
    expect(b.result.current).toBeNull()
    act(() => writeWatchWay(1, 'fake:way'))
    expect(a.result.current).toBe('fake:way')
    expect(b.result.current).toBe('fake:way')
    expect(other.result.current).toBeNull()
  })

  it('follows another tab through the storage event', () => {
    const a = renderHook(() => useWatchWay(1))
    act(() => {
      localStorage.setItem('antresol.watchWay.1', 'online')
      window.dispatchEvent(new StorageEvent('storage', { key: 'antresol.watchWay.1' }))
    })
    expect(a.result.current).toBe('online')
  })

  it('blocked storage: no throw, nothing remembered', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    const a = renderHook(() => useWatchWay(1))
    act(() => writeWatchWay(1, 'fake:way'))
    expect(a.result.current).toBeNull()
    vi.restoreAllMocks()
  })
})

describe('pickWay', () => {
  it('keeps the remembered way when it is available', () => {
    expect(pickWay('fake:way', ['online', 'fake:way'])).toBe('fake:way')
  })
  it('an id no way has any more reads as none', () => {
    expect(pickWay('gone:way', ['online', 'fake:way'])).toBe('online')
    expect(pickWay('gone:way', ['fake:way'])).toBe('fake:way')
  })
  it('falls back to online, then to the first available', () => {
    expect(pickWay('online', ['fake:way'])).toBe('fake:way')
    expect(pickWay(null, ['online', 'fake:way'])).toBe('online')
    expect(pickWay(null, ['fake:way'])).toBe('fake:way')
  })
})
