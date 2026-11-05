import { describe, expect, it } from 'vitest'
import { pluralForm } from '..'

// Catalog plural entries are picked with Intl.PluralRules: ru one/few/many, en one/other.
const ruSongs = { one: 'песня', few: 'песни', many: 'песен' }
const enSongs = { one: 'song', other: 'songs' }

describe('plurals', () => {
  it.each([
    [0, 'песен'], [1, 'песня'], [2, 'песни'], [4, 'песни'], [5, 'песен'], [11, 'песен'], [12, 'песен'],
    [14, 'песен'], [21, 'песня'], [22, 'песни'], [25, 'песен'], [101, 'песня'], [111, 'песен'],
  ])('ru %i', (n, word) => {
    expect(pluralForm('ru', ruSongs, n)).toBe(word)
  })
  it.each([
    [0, 'songs'], [1, 'song'], [2, 'songs'], [21, 'songs'],
  ])('en %i', (n, word) => {
    expect(pluralForm('en', enSongs, n)).toBe(word)
  })
})
