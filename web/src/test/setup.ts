import '@testing-library/jest-dom/vitest'
import { afterEach, vi } from 'vitest'
import { cleanup } from '@testing-library/react'

// The suites assert the Russian (source) text: the browser prefers Russian.
// Set before the i18n store loads (it reads navigator.languages once and on
// 'languagechange').
Object.defineProperty(window.navigator, 'languages', { configurable: true, get: () => ['ru-RU', 'ru'] })
const { setLanguagePref, getLanguagePref } = await import('../i18n')

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  // a test that picked English hands the next one Russian again
  if (getLanguagePref() !== 'system') setLanguagePref('system')
})

/** Emulates a viewport width for components that use matchMedia. */
export function setViewport(width: number) {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: width })
  window.matchMedia = ((query: string) => {
    const max = /max-width:\s*(\d+)px/.exec(query)
    const min = /min-width:\s*(\d+)px/.exec(query)
    const reduce = /prefers-reduced-motion:\s*reduce/.test(query)
    let matches = false
    if (max) matches = width <= Number(max[1])
    else if (min) matches = width >= Number(min[1])
    else if (reduce) matches = false
    return {
      matches,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    } as MediaQueryList
  }) as typeof window.matchMedia
}

setViewport(1440)

// jsdom lacks these.
if (!('IntersectionObserver' in window)) {
  class IO {
    observe() {}
    unobserve() {}
    disconnect() {}
    takeRecords() {
      return []
    }
  }
  ;(window as unknown as { IntersectionObserver: unknown }).IntersectionObserver = IO
}
if (!('ResizeObserver' in window)) {
  class RO {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  ;(window as unknown as { ResizeObserver: unknown }).ResizeObserver = RO
}
Element.prototype.scrollTo = function () {}
Element.prototype.scrollBy = function () {}
