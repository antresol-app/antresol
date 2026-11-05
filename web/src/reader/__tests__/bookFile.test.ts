import { describe, expect, it } from 'vitest'
import { bookFile, bookFileName } from '../bookFile'

const ZIP = new Uint8Array([0x50, 0x4b, 0x03, 0x04, 0, 0])

describe('bookFile', () => {
  it('names the file the way foliate tells formats apart', async () => {
    expect(await bookFileName('EPUB', new Blob([ZIP]))).toBe('book.epub')
    expect(await bookFileName('FB2', new Blob(['<?xml version="1.0"?>']))).toBe('book.fb2')
    expect(await bookFileName('FB2', new Blob([ZIP]))).toBe('book.fb2.zip')
    expect(await bookFileName('MOBI', new Blob(['x']))).toBe('book.mobi')
    expect(await bookFileName('AZW3', new Blob(['x']))).toBe('book.azw3')
  })

  it('re-encodes an undeclared Windows-1251 FB2 to UTF-8', async () => {
    const head = new TextEncoder().encode('<?xml version="1.0"?><FictionBook><p>')
    const privet = new Uint8Array([0xcf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2]) // «Привет» in CP1251
    const tail = new TextEncoder().encode('</p></FictionBook>')
    const file = await bookFile('FB2', new Blob([head, privet, tail]))
    expect(file.name).toBe('book.fb2')
    const text = await file.text()
    expect(text).toContain('Привет')
    expect(text.startsWith('<?xml version="1.0" encoding="utf-8"?>')).toBe(true)
  })

  it('leaves an EPUB as it is', async () => {
    const file = await bookFile('EPUB', new Blob([ZIP]))
    expect(file.name).toBe('book.epub')
    expect(new Uint8Array(await file.arrayBuffer())).toEqual(ZIP)
  })
})
