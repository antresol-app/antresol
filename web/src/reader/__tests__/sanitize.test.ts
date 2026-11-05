import { describe, expect, it } from 'vitest'
import { guardBook, sanitizeMarkup } from '../sanitize'

const EVIL = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>c1</title><script src="evil.js"></script><script>top.__inline='ran'</script></head>
<body><p>Hello <a href="javascript:alert(1)">link</a></p><img src="nope.png" onerror="top.__onerror='ran'"/><iframe src="/api/me"/></body></html>`

describe('sanitizeMarkup', () => {
  it('drops scripts, handlers and javascript: URLs from XHTML', () => {
    const out = sanitizeMarkup(EVIL, 'application/xhtml+xml')
    expect(out).not.toMatch(/<script/i)
    expect(out).not.toMatch(/onerror/i)
    expect(out).not.toMatch(/javascript:/i)
    expect(out).not.toMatch(/<iframe/i)
    expect(out).toContain('Hello')
    expect(out).toContain('nope.png')
    expect(out.startsWith('<?xml')).toBe(true)
  })

  it('scrubs markup the parser rejects', () => {
    const broken = '<html><body><p>x<script>bad()</script><img onerror=bad() src=a.png></body>'
    const out = sanitizeMarkup(broken, 'application/xhtml+xml')
    expect(out).not.toMatch(/<script|onerror/i)
    expect(out).toContain('a.png')
  })

  it('handles HTML and SVG, leaves CSS alone', () => {
    expect(sanitizeMarkup('<p onclick="x()">a</p><script>x()</script>', 'text/html')).not.toMatch(/onclick|<script/i)
    expect(sanitizeMarkup('<svg xmlns="http://www.w3.org/2000/svg"><script>x()</script><rect onload="x()"/></svg>', 'image/svg+xml')).not.toMatch(
      /script|onload/i,
    )
    expect(sanitizeMarkup('p { color: red }', 'text/css')).toBe('p { color: red }')
  })

  it('guardBook refuses scripts and sanitizes section data', async () => {
    const t = new EventTarget()
    guardBook({ transformTarget: t })
    const load = new CustomEvent('load', { detail: { isScript: true, allow: true } })
    t.dispatchEvent(load)
    expect(load.detail.allow).toBe(false)
    const data = new CustomEvent('data', { detail: { data: Promise.resolve(EVIL), type: 'application/xhtml+xml' } })
    t.dispatchEvent(data)
    expect(await data.detail.data).not.toMatch(/<script/i)
  })
})

describe('round 2', () => {
  const fire = (t: EventTarget, name: string, detail: object) => {
    const e = new CustomEvent(name, { detail })
    t.dispatchEvent(e)
    return e.detail as Record<string, unknown>
  }

  it('sanitizes markup sections with a non-exact type (Blob data) and gives them an exact type', async () => {
    const t = new EventTarget()
    guardBook({ transformTarget: t })
    for (const type of ['text/html; charset=utf-8', 'TEXT/HTML', 'application/xml', 'text/xml']) {
      const d = fire(t, 'data', { data: Promise.resolve(new Blob([EVIL])), type })
      const data = await d.data
      const text = typeof data === 'string' ? data : await (data as Blob).text()
      expect(text, type).not.toMatch(/<script|onerror/i)
      expect(text, type).toContain('Hello')
      expect(await d.type, type).toMatch(/^(text\/html|application\/xhtml\+xml)$/)
    }
  })

  it('refuses resources that are neither safe media nor sanitizable markup', () => {
    const t = new EventTarget()
    guardBook({ transformTarget: t })
    const allow = (type: string) => fire(t, 'load', { type, isScript: false, allow: true }).allow
    for (const ok of ['image/jpeg', 'image/svg+xml', 'text/css', 'font/woff2', 'application/vnd.ms-opentype', 'application/xhtml+xml', 'text/html; charset=utf-8', 'audio/mpeg'])
      expect(allow(ok), ok).toBe(true)
    for (const bad of ['application/javascript', 'text/javascript', 'application/octet-stream', 'application/x-shockwave-flash', 'application/pdf'])
      expect(allow(bad), bad).toBe(false)
  })

  it('puts a frame policy without remote images or scripts into every section', () => {
    const out = sanitizeMarkup(EVIL, 'application/xhtml+xml')
    const csp = /<meta[^>]+http-equiv="Content-Security-Policy"[^>]+content="([^"]+)"/i.exec(out)?.[1] ?? ''
    expect(csp).toContain("script-src 'none'")
    expect(csp).toMatch(/img-src 'self' blob: data:/)
    expect(out.indexOf('Content-Security-Policy')).toBeLessThan(out.indexOf('<title'))
    expect(sanitizeMarkup('<html><head></head><body><p>x</p></body></html>', 'text/html')).toContain('Content-Security-Policy')
  })
})
