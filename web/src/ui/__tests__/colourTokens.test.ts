// Pages that colour things in script (tile tints, file icons, the storage
// bar) take the colours from tokens, never from literal values.
import { describe, expect, it } from 'vitest'
import { literalColours } from './cssRule'

const FILES = [
  'pages/manage/Manage.tsx',
  'pages/files/FileIcon.tsx',
  'pages/files/StorageBar.tsx',
  'pages/files/StorageBar.module.css',
  'pages/files/glyphs.tsx',
  'components/DiskMeter.tsx',
]

describe('colours in script', () => {
  it.each(FILES)('%s names no literal colour', (file) => {
    expect(literalColours(file)).toEqual([])
  })
})
