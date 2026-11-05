// An in-memory CacheStorage for tests: keys are paths (query included);
// `ignoreSearch` compares paths without the query, like the browser.
const pathOf = (r: RequestInfo | URL) => {
  const s = typeof r === 'string' ? r : r instanceof URL ? r.href : r.url
  const u = new URL(s, 'http://vault.test')
  return { full: u.pathname + u.search, bare: u.pathname }
}

export class FakeCache {
  entries = new Map<string, Response>()
  async add(r: RequestInfo | URL) {
    const res = await fetch(typeof r === 'string' ? r : r instanceof URL ? r.href : r.url)
    if (!res.ok) throw new TypeError(`bad status ${res.status}`)
    this.entries.set(pathOf(r).full, res)
  }
  async addAll(rs: (RequestInfo | URL)[]) {
    const got = await Promise.all(
      rs.map(async (r) => {
        const res = await fetch(typeof r === 'string' ? r : r instanceof URL ? r.href : r.url)
        if (!res.ok) throw new TypeError(`bad status ${res.status}`)
        return [pathOf(r).full, res] as const
      }),
    )
    for (const [k, v] of got) this.entries.set(k, v)
  }
  async put(r: RequestInfo | URL, res: Response) {
    this.entries.set(pathOf(r).full, res)
  }
  async match(r: RequestInfo | URL, o?: { ignoreSearch?: boolean }) {
    const p = pathOf(r)
    const hit = this.entries.get(p.full) ?? (o?.ignoreSearch ? [...this.entries].find(([k]) => k.split('?')[0] === p.bare)?.[1] : undefined)
    return hit?.clone()
  }
  async delete(r: RequestInfo | URL) {
    return this.entries.delete(pathOf(r).full)
  }
  async keys() {
    return [...this.entries.keys()].map((k) => ({ url: `http://vault.test${k}` }) as Request)
  }
}

export class FakeCaches {
  stores = new Map<string, FakeCache>()
  async open(name: string) {
    let c = this.stores.get(name)
    if (!c) this.stores.set(name, (c = new FakeCache()))
    return c
  }
  async delete(name: string) {
    return this.stores.delete(name)
  }
  async keys() {
    return [...this.stores.keys()]
  }
  async match(r: RequestInfo | URL, o?: { ignoreSearch?: boolean }) {
    for (const c of this.stores.values()) {
      const hit = await c.match(r, o)
      if (hit) return hit
    }
    return undefined
  }
}
