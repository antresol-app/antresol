import { afterEach, describe, expect, it } from 'vitest'
import { setLanguagePref } from '../../i18n'
import { asPrefs } from '../trackPrefs'

describe('asPrefs: the household defaults follow the interface language', () => {
  afterEach(() => setLanguagePref('system'))
  const household = { audioLangs: ['ru', 'en'], subMode: 'off', subLangs: ['ru', 'en'], custom: false }

  it('an English interface hears English first', () => {
    setLanguagePref('en')
    expect(asPrefs(household)).toMatchObject({ audioLangs: ['en', 'ru'], subLangs: ['en', 'ru'] })
  })

  it('a profile’s own order is kept', () => {
    setLanguagePref('en')
    expect(asPrefs({ ...household, custom: true })).toMatchObject({ audioLangs: ['ru', 'en'], subLangs: ['ru', 'en'] })
  })

  it('a language the household lacks comes first', () => {
    setLanguagePref('ru')
    expect(asPrefs({ ...household, audioLangs: ['en'], subLangs: [] })).toMatchObject({ audioLangs: ['ru', 'en'], subLangs: [] })
  })
})
