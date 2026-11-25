import { describe, expect, it } from 'vitest'
import { safeHttpUrl } from '../client'

describe('safeHttpUrl', () => {
  it('lets http and https addresses through, as they are', () => {
    expect(safeHttpUrl('https://example.org/a?b=1')).toBe('https://example.org/a?b=1')
    expect(safeHttpUrl('HTTP://127.0.0.1:8765/x')).toBe('HTTP://127.0.0.1:8765/x')
  })

  it('drops everything else', () => {
    for (const u of ['javascript:alert(1)', ' javascript:alert(1)', 'data:text/html,x', 'tg://resolve?domain=x', 'ftp://example.org', '//example.org', '/relative', '', undefined, null]) {
      expect(safeHttpUrl(u)).toBeUndefined()
    }
  })
})
