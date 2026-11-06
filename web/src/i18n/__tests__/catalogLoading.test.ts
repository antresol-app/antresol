// Catalogs are chunks loaded on demand (core.ts). The test setup preloads
// both; these tests forget one and stub its import.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { catalogImporters, forgetCatalogForTests, getLanguage, loadCatalog, loadCatalogAtBoot, setLanguagePref, translate } from '../core'

const realEn = catalogImporters.en

describe('catalog loading', () => {
  afterEach(async () => {
    catalogImporters.en = realEn
    setLanguagePref('system')
    await loadCatalog('en')
  })

  it('a failed load is not stuck: the next call asks again', async () => {
    forgetCatalogForTests('en')
    const fail = vi.fn(() => Promise.reject(new Error('offline')))
    catalogImporters.en = fail
    await expect(loadCatalog('en')).rejects.toThrow('offline')
    catalogImporters.en = realEn
    await expect(loadCatalog('en')).resolves.toBeUndefined()
    expect(translate('en', 'common.library')).toBe('Library')
  })

  it('at boot it asks twice, then says there are no strings', async () => {
    forgetCatalogForTests('en')
    const fail = vi.fn(() => Promise.reject(new Error('offline')))
    catalogImporters.en = fail
    expect(await loadCatalogAtBoot('en')).toBe(false)
    expect(fail).toHaveBeenCalledTimes(2)
    let n = 0
    catalogImporters.en = () => (n++ === 0 ? Promise.reject(new Error('blip')) : realEn())
    expect(await loadCatalogAtBoot('en')).toBe(true)
  })

  it('a language whose catalog fails keeps the loaded strings, and a later change asks again', async () => {
    forgetCatalogForTests('en')
    catalogImporters.en = () => Promise.reject(new Error('offline'))
    setLanguagePref('en')
    expect(getLanguage()).toBe('en')
    await Promise.resolve()
    // falls back to the Russian catalog that is in
    expect(translate('en', 'common.library')).toBe('Медиатека')
    setLanguagePref('ru')
    catalogImporters.en = realEn
    setLanguagePref('en')
    await vi.waitFor(() => expect(translate('en', 'common.library')).toBe('Library'))
  })
})
