import { describe, expect, it } from 'vitest'
import { fb2ToUtf8 } from '../fb2text'

// «Анна» in Windows-1251
const ANNA = [0xc0, 0xed, 0xed, 0xe0]
const bytes = (head: string, body: number[], tail: string) =>
  new Uint8Array([...new TextEncoder().encode(head), ...body, ...new TextEncoder().encode(tail)])
const text = async (b: Blob) => new TextDecoder().decode(await b.arrayBuffer())

describe('fb2ToUtf8', () => {
  it('decodes undeclared CP1251 and declares utf-8', async () => {
    const out = await fb2ToUtf8(new Blob([bytes('<?xml version="1.0"?><FictionBook><p>', ANNA, '</p></FictionBook>')]))
    const t = await text(out)
    expect(t).toContain('<p>Анна</p>')
    expect(t.startsWith('<?xml version="1.0" encoding="utf-8"?>')).toBe(true)
  })

  it('fixes a wrong utf-8 declaration and a missing one', async () => {
    const wrong = await text(await fb2ToUtf8(new Blob([bytes('<?xml version="1.0" encoding="UTF-8"?><p>', ANNA, '</p>')])))
    expect(wrong).toContain('<p>Анна</p>')
    expect(wrong.match(/<\?xml/g)).toHaveLength(1)
    const none = await text(await fb2ToUtf8(new Blob([bytes('<FictionBook><p>', ANNA, '</p></FictionBook>')])))
    expect(none).toBe('<?xml version="1.0" encoding="utf-8"?>\n<FictionBook><p>Анна</p></FictionBook>')
  })

  it('leaves valid UTF-8 and other declared encodings alone', async () => {
    const utf = new Blob(['<?xml version="1.0"?><p>Анна</p>'])
    expect(await fb2ToUtf8(utf)).toBe(utf)
    const koi = new Blob([bytes('<?xml version="1.0" encoding="koi8-r"?><p>', [0xe1, 0xce, 0xce, 0xc1], '</p>')])
    expect(await fb2ToUtf8(koi)).toBe(koi)
  })
})
