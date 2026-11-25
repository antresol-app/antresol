/**
 * `u` when it is an http(s) address, else undefined: an address from a server's answer or a book
 * reaches a link, a redirect or a new tab only through this (no javascript:, data: or custom schemes).
 * Its own file: the reader's link handler uses it without the API client.
 */
export function safeHttpUrl(u: string | null | undefined): string | undefined {
  return typeof u === 'string' && /^https?:\/\//i.test(u) ? u : undefined
}
