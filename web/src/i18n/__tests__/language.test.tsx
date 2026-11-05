import { afterEach, describe, expect, it } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { getLanguage, getLanguagePref, resolveLanguage, setLanguagePref, STORAGE_KEY, t, translate, useT } from '..'

describe('resolveLanguage (the shared rule)', () => {
  it.each([
    [['ru-RU', 'en-US'], 'ru'],
    [['ru'], 'ru'],
    [['en-GB', 'ru-RU'], 'en'],
    [['uk-UA', 'ru-RU'], 'ru'],
    [['de-DE', 'en-US', 'ru'], 'en'],
    [['be-BY'], 'en'],
    [['uk', 'de'], 'en'],
    [['RU_ru'], 'ru'],
    [[], 'en'],
  ] as const)('%j → %s', (prefs, want) => {
    expect(resolveLanguage(prefs)).toBe(want)
  })
  it('no languages at all → en', () => {
    expect(resolveLanguage(undefined)).toBe('en')
  })
})

describe('language store', () => {
  afterEach(() => setLanguagePref('system'))

  it('follows the browser by default (test setup prefers Russian)', () => {
    expect(getLanguagePref()).toBe('system')
    expect(getLanguage()).toBe('ru')
    expect(document.documentElement.lang).toBe('ru')
  })

  it('a choice is stored per browser and sets <html lang>', () => {
    setLanguagePref('en')
    expect(localStorage.getItem(STORAGE_KEY)).toBe('en')
    expect(getLanguage()).toBe('en')
    expect(document.documentElement.lang).toBe('en')
    expect(t('common.library')).toBe('Library')
    setLanguagePref('ru')
    expect(t('common.library')).toBe('Медиатека')
  })

  it('«Как в системе» follows the browser again', () => {
    setLanguagePref('en')
    setLanguagePref('system')
    expect(localStorage.getItem(STORAGE_KEY)).toBe('system')
    expect(getLanguage()).toBe('ru')
  })

  it('a component re-renders on a change, without a reload', () => {
    function Title() {
      const tr = useT()
      return <h1>{tr('common.settings')}</h1>
    }
    render(<Title />)
    expect(screen.getByRole('heading')).toHaveTextContent('Настройки')
    act(() => setLanguagePref('en'))
    expect(screen.getByRole('heading')).toHaveTextContent('Settings')
  })
})

describe('translate', () => {
  it('fills placeholders', () => {
    expect(translate('ru', 'common.ago.minutes', { n: 5 })).toBe('5 мин назад')
    expect(translate('en', 'common.ago.minutes', { n: 5 })).toBe('5 min ago')
  })
})
