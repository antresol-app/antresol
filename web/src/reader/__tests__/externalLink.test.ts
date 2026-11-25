import { afterEach, describe, expect, it, vi } from 'vitest'
import { openExternalLink } from '../externalLink'

const click = (href_: string) => new CustomEvent('external-link', { detail: { href_ }, cancelable: true })

describe('openExternalLink', () => {
  afterEach(() => vi.restoreAllMocks())

  it('opens an http(s) or mailto link in a tab with no opener, and keeps foliate from opening its own', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const e = click('https://example.org/page')
    openExternalLink(e)
    expect(e.defaultPrevented).toBe(true)
    expect(open).toHaveBeenCalledWith('https://example.org/page', '_blank', 'noopener,noreferrer')
    const mail = click('mailto:a@example.org')
    openExternalLink(mail)
    expect(mail.defaultPrevented).toBe(true)
    expect(open).toHaveBeenLastCalledWith('mailto:a@example.org', '_blank', 'noopener,noreferrer')
  })

  it('opens nothing for another scheme', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    for (const href of ['javascript:alert(1)', ' mailto:a@example.org', 'ftp://example.org/x', 'itms-apps://x']) {
      const e = click(href)
      openExternalLink(e)
      expect(e.defaultPrevented).toBe(true)
    }
    expect(open).not.toHaveBeenCalled()
  })
})
