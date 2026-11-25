// A click on an external link inside a book (foliate's cancelable `external-link`):
// http(s) and mailto only, in a new tab that cannot reach back to this one
// (foliate's own window.open keeps window.opener and lets any scheme through).
// mailto opens a draft the person still has to send, as in the Apple reader.
import { safeHttpUrl } from '../api/safeUrl'

export function openExternalLink(e: Event): void {
  e.preventDefault()
  const href = (e as CustomEvent<{ href_?: string }>).detail?.href_
  const url = safeHttpUrl(href) ?? (typeof href === 'string' && /^mailto:/i.test(href) ? href : undefined)
  if (url) window.open(url, '_blank', 'noopener,noreferrer')
}
