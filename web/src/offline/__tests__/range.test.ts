import { describe, expect, it } from 'vitest'
import { Blob as NodeBlob } from 'node:buffer'
import { parseRange, rangeResponse } from '../range'

// Node's Blob: the test Response (Node's) reads it; jsdom's Blob it would stringify.

const file = new NodeBlob([new Uint8Array(1000).map((_, i) => i % 256)], { type: 'audio/flac' }) as unknown as Blob

describe('rangeResponse', () => {
  it('bytes=100-199 of 1000 → 206 with Content-Range bytes 100-199/1000 and 100 bytes', async () => {
    const r = rangeResponse(file, 'bytes=100-199', 'audio/flac')
    expect(r.status).toBe(206)
    expect(r.headers.get('Content-Range')).toBe('bytes 100-199/1000')
    expect(r.headers.get('Content-Length')).toBe('100')
    const body = new Uint8Array(await r.arrayBuffer())
    expect(body.length).toBe(100)
    expect(body[0]).toBe(100)
  })

  it('open-ended bytes=900- → 900-999', () => {
    expect(parseRange('bytes=900-', 1000)).toEqual({ start: 900, end: 999 })
    expect(rangeResponse(file, 'bytes=900-', 'audio/flac').headers.get('Content-Range')).toBe('bytes 900-999/1000')
  })

  it('suffix bytes=-100 → 900-999', () => {
    expect(parseRange('bytes=-100', 1000)).toEqual({ start: 900, end: 999 })
    expect(parseRange('bytes=-5000', 1000)).toEqual({ start: 0, end: 999 })
  })

  it('out of range → 416', () => {
    const r = rangeResponse(file, 'bytes=1000-1100', 'audio/flac')
    expect(r.status).toBe(416)
    expect(r.headers.get('Content-Range')).toBe('bytes */1000')
    expect(parseRange('bytes=5-2', 1000)).toBeNull()
    expect(parseRange('items=0-1', 1000)).toBeNull()
    expect(parseRange('bytes=0-5000', 1000)).toEqual({ start: 0, end: 999 })
  })
})
