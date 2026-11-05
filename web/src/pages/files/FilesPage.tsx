// «Файлы»: a Finder / iCloud Drive–style browser of the media disk. The URL
// is the location (/files/<root>/<path…>, /files/trash); the page keeps the
// selection, the view and the sheets, and handles the keyboard like Finder.
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type FocusEvent, type KeyboardEvent, type MouseEvent, type PointerEvent as RPointerEvent, type ReactNode } from 'react'
import { Link, Navigate, useLocation, useNavigate } from 'react-router'
import { ApiError, useEvents } from '../../api/client'
import { downloadFileUrl, getRoots, type FileSort, type SortOrder } from '../../api/files'
import type { FileEntry, FileRoot } from '../../api/types'
import { Menu, MenuItem, MenuSeparator } from '../../ui/Menu'
import { useSession } from '../../state/session'
import { useIsCompact } from '../../state/useMediaQuery'
import { ColumnView } from './ColumnView'
import { ContextMenu, type MenuEntry } from './ContextMenu'
import { FileOps, type Op, type Toast as FileToast } from './FileOps'
import { Toast } from '../../ui/Toast'
import { Glyph, itemGlyph } from './glyphs'
import { itemHref } from '../../components/Card'
import { IconView } from './IconView'
import { InfoContent, InfoPanel } from './InfoPanel'
import { ListView, type Row } from './ListView'
import {
  clickSelect,
  compareNames,
  emptySelection,
  folderHref,
  formatSize,
  joinPath,
  moveLead,
  objects,
  opensItem,
  parentPath,
  pruneSelection,
  selectAll,
  single,
  sortEntries,
  type Selection,
} from './model'
import { isBool, oneOf, usePref } from './prefs'
import { QuickLook } from './QuickLook'
import { Sheet } from './Sheet'
import { Sidebar } from './Sidebar'
import { SearchField, SORTS, Toolbar, type Crumb } from './Toolbar'
import { TrashView } from './TrashView'
import { invalidateDirs, peekDir, useDir, useDirs, useDirVersion } from './useDir'
import { useFolderHistory } from './useFolderHistory'
import { itemId, type ViewMode } from './views'
import styles from './FilesPage.module.css'
import { locale, t, useT } from '../../i18n'

/** /files/movies/A%20B/C → { root: 'movies', path: 'A B/C' }. */
export function parseLocation(pathname: string): { root: string | null; path: string; trash: boolean } {
  const parts = pathname
    .replace(/^\/files\/?/, '')
    .split('/')
    .filter(Boolean)
    .map((s) => {
      try {
        return decodeURIComponent(s)
      } catch {
        return s
      }
    })
  if (parts[0] === 'trash' && parts.length === 1) return { root: null, path: '', trash: true }
  return { root: parts[0] ?? null, path: parts.slice(1).join('/'), trash: false }
}

/** Default order of a column when it is first clicked (Finder: biggest and newest first). */
const FIRST_ORDER: Record<FileSort, SortOrder> = { name: 'asc', kind: 'asc', size: 'desc', mtime: 'desc' }

function useRoots() {
  const [state, setState] = useState<{ roots?: FileRoot[]; error?: string }>({})
  const load = useCallback(() => {
    setState({})
    getRoots().then(
      (roots) => setState({ roots }),
      (e: unknown) => setState({ error: e instanceof ApiError ? e.message : t('files.rootsFailed') }),
    )
  }, [])
  useEffect(load, [load])
  return { ...state, reload: load }
}

function download(root: string, e: FileEntry) {
  const a = document.createElement('a')
  a.href = downloadFileUrl(root, e.path)
  a.download = e.name
  document.body.appendChild(a)
  a.click()
  a.remove()
}

export function FilesPage() {
  const tr = useT()
  const { user } = useSession()
  const location = useLocation()
  const navigate = useNavigate()
  const compact = useIsCompact()
  const { root, path, trash } = parseLocation(location.pathname)
  const { roots, error: rootsError, reload: reloadRoots } = useRoots()
  const isAdmin = Boolean(user?.isAdmin)
  const history = useFolderHistory(location.pathname)

  const [viewPref, setView] = usePref<ViewMode>('view', 'icons', oneOf('icons', 'list', 'columns'))
  const [sort, setSort] = usePref<FileSort>('sort', 'name', oneOf('name', 'size', 'mtime', 'kind'))
  const [order, setOrder] = usePref<SortOrder>('order', 'asc', oneOf('asc', 'desc'))
  const [infoOpen, setInfoOpen] = usePref<boolean>('info', false, isBool)
  const [query, setQuery] = useState('')
  const [q, setQ] = useState('')
  const [sel, setSel] = useState<Selection>(emptySelection)
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set())
  const [focused, setFocused] = useState(false)
  const [ql, setQl] = useState<string | null>(null)
  const [menu, setMenu] = useState<{ x: number; y: number; targets: FileEntry[] } | null>(null)
  const [op, setOp] = useState<Op | null>(null)
  const [toast, setToast] = useState<FileToast | null>(null)
  const [phoneInfo, setPhoneInfo] = useState<FileEntry | null>(null)
  const [refresh, setRefresh] = useState(0)
  const containerRef = useRef<HTMLDivElement>(null)
  const pending = useRef<string | 'first' | null>(null)
  const [selectFirst, setSelectFirst] = useState(false)
  const prevLoc = useRef({ root, path })
  const typeAhead = useRef({ text: '', at: 0 })

  // Debounced search inside the current folder.
  useEffect(() => {
    const t = setTimeout(() => setQ(query.trim()), 250)
    return () => clearTimeout(t)
  }, [query])

  const view: ViewMode = compact && viewPref === 'columns' ? 'list' : q && viewPref === 'columns' ? 'list' : viewPref
  const dir = useDir(trash ? null : root, path, q)
  const entries = useMemo(() => sortEntries(dir.data?.entries ?? [], sort, order), [dir.data, sort, order])
  const rootInfo = roots?.find((r) => r.id === root)

  // List view: expanded folders show their children in place.
  useDirs(view === 'list' && root && !q ? root : null, [...expanded])
  const dirVersion = useDirVersion()
  const rows: Row[] = useMemo(() => {
    const out: Row[] = []
    const walk = (list: FileEntry[], level: number) => {
      for (const e of list) {
        const open = e.isDir && expanded.has(e.path)
        const sub = open && root ? peekDir(root, e.path) : undefined
        out.push({ entry: e, level, loading: open && !sub?.data })
        if (open && sub?.data && level < 12) walk(sortEntries(sub.data.entries, sort, order), level + 1)
      }
    }
    walk(entries, 0)
    return out
    // dirVersion: a child listing arrived or reloaded (read through peekDir).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [entries, expanded, root, sort, order, dirVersion])

  const visible = view === 'list' ? rows.map((r) => r.entry) : entries
  const order_ = useMemo(() => visible.map((e) => e.path), [visible])
  const byPath = useMemo(() => new Map(visible.map((e) => [e.path, e])), [visible])
  const selected = useMemo(() => visible.filter((e) => sel.selected.has(e.path)), [visible, sel])
  const lead = sel.lead ? byPath.get(sel.lead) : undefined

  // A new location: reset search and expansion; select what makes sense.
  useLayoutEffect(() => {
    const prev = prevLoc.current
    if (prev.root === root && prev.path === path) return
    prevLoc.current = { root, path }
    setExpanded(new Set())
    setQuery('')
    setQ('')
    setQl(null)
    const p = pending.current
    pending.current = null
    if (p && p !== 'first') setSel(single(p))
    else if (p === 'first') {
      setSel(emptySelection)
      setSelectFirst(true)
    }
    else if (prev.root === root && root && (prev.path + '/').startsWith(path ? path + '/' : '') && prev.path !== path) {
      // Going up selects the folder we came from, as Finder does.
      const rest = prev.path.slice(path ? path.length + 1 : 0)
      setSel(single(joinPath(path, rest.split('/')[0]!)))
    } else setSel(emptySelection)
  }, [root, path])

  // «Right arrow» in column view: select the first item once the folder loads.
  useEffect(() => {
    if (!selectFirst || !dir.data) return
    setSelectFirst(false)
    setSel(order_[0] ? single(order_[0]) : emptySelection)
  }, [selectFirst, dir.data, order_])

  useEffect(() => {
    // Functional update: a selection set in the same commit (going up) must win.
    // Not while a reload is due: a just-renamed item is not in the old listing yet.
    if (selectFirst || !dir.data || dir.loading || dir.stale) return
    setSel((s) => pruneSelection(s, order_))
  }, [order_, dir.data, dir.loading, dir.stale, selectFirst])

  // Keep the lead visible.
  useEffect(() => {
    if (sel.lead) document.getElementById(itemId(sel.lead))?.scrollIntoView?.({ block: 'nearest' })
  }, [sel.lead])

  // Library rescans change the «Фильм в медиатеке» links.
  useEvents(() => invalidateDirs(), ['library.updated'])

  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(null), 6000)
    return () => clearTimeout(t)
  }, [toast])

  useEffect(() => {
    const base = rootInfo?.title ?? (trash ? tr('files.trash') : tr('common.files'))
    const name = path ? path.slice(path.lastIndexOf('/') + 1) : base
    document.title = `${name} — ${APP_NAME}`
  }, [rootInfo, path, trash, tr])

  const changed = useCallback(() => {
    invalidateDirs()
    setRefresh((n) => n + 1)
  }, [])

  // ---------- Actions ----------

  const goTo = useCallback(
    (r: string, p: string, opts: { replace?: boolean; select?: string | 'first' } = {}) => {
      pending.current = opts.select ?? null
      navigate(folderHref(r, p), { replace: opts.replace })
    },
    [navigate],
  )

  const open = useCallback(
    (e: FileEntry) => {
      if (!root) return
      if (e.isDir) goTo(root, e.path)
      else if (opensItem(e)) navigate(itemHref(e.item!.kind, e.item!.id))
      else {
        setSel(single(e.path))
        setQl(e.path)
      }
    },
    [root, goTo, navigate],
  )

  const goUp = () => {
    if (root && path) goTo(root, parentPath(path))
  }

  const askTrash = (list: FileEntry[]) => {
    if (isAdmin && list.length) setOp({ type: 'trash', entries: list })
  }

  const onSort = (k: FileSort) => {
    if (k === sort) setOrder(order === 'asc' ? 'desc' : 'asc')
    else {
      setSort(k)
      setOrder(FIRST_ORDER[k])
    }
  }

  const onSelectNow = (e: MouseEvent, entry: FileEntry) => {
    if (compact) return open(entry)
    setSel((s) => clickSelect(s, order_, entry.path, { toggle: e.metaKey || e.ctrlKey, range: e.shiftKey }))
  }

  const onMenuNow = (e: { preventDefault(): void; stopPropagation(): void; clientX: number; clientY: number }, entry: FileEntry | null) => {
    e.preventDefault()
    e.stopPropagation()
    let targets: FileEntry[] = []
    if (entry) {
      if (sel.selected.has(entry.path)) targets = visible.filter((x) => sel.selected.has(x.path))
      else {
        setSel(single(entry.path))
        targets = [entry]
      }
    } else setSel(emptySelection)
    setMenu({ x: e.clientX, y: e.clientY, targets })
  }

  const toggle = useCallback((e: FileEntry) => {
    setExpanded((s) => {
      const n = new Set(s)
      if (n.has(e.path)) {
        for (const k of [...n]) if (k === e.path || k.startsWith(e.path + '/')) n.delete(k)
      } else n.add(e.path)
      return n
    })
  }, [])

  const onColumnSelectNow = (d: string, e: MouseEvent, entry: FileEntry) => {
    if (!root) return
    if (d === path) return onSelectNow(e, entry)
    goTo(root, d, { replace: true, select: entry.path })
  }

  // Item handlers keep one identity for the page's lifetime, so memoised
  // tiles and rows re-render only when their own props change (5000 items).
  const latest = useRef({ onSelectNow, onMenuNow, open, onColumnSelectNow, byPath })
  latest.current = { onSelectNow, onMenuNow, open, onColumnSelectNow, byPath }

  // Phone: a long press on an item opens its action sheet (iOS has no right-click).
  const press = useRef<{ timer: ReturnType<typeof setTimeout>; x: number; y: number; fired: boolean } | null>(null)
  const longPress = useMemo(() => {
    const cancel = () => {
      const s = press.current
      if (!s) return
      if (!s.fired) {
        clearTimeout(s.timer)
        press.current = null
      } else {
        // Swallow only the click that ends this press, if the browser sends one.
        setTimeout(() => {
          if (press.current === s) press.current = null
        }, 400)
      }
    }
    return {
      onPointerDown: (e: RPointerEvent) => {
        if (e.pointerType === 'mouse') return
        const el = (e.target as HTMLElement).closest<HTMLElement>('[data-path]')
        const p = el?.dataset.path
        if (!p) return
        const x = e.clientX
        const y = e.clientY
        if (press.current) clearTimeout(press.current.timer)
        press.current = {
          x,
          y,
          fired: false,
          timer: setTimeout(() => {
            const entry = latest.current.byPath.get(p)
            if (!entry || !press.current) return
            press.current.fired = true
            navigator.vibrate?.(8)
            latest.current.onMenuNow({ preventDefault() {}, stopPropagation() {}, clientX: x, clientY: y }, entry)
          }, 480),
        }
      },
      onPointerMove: (e: RPointerEvent) => {
        const s = press.current
        if (s && !s.fired && Math.hypot(e.clientX - s.x, e.clientY - s.y) > 10) cancel()
      },
      onPointerUp: cancel,
      onPointerCancel: cancel,
      // The tap that ends a long press must not also open the item.
      onClickCapture: (e: MouseEvent) => {
        if (press.current?.fired) {
          e.stopPropagation()
          e.preventDefault()
          press.current = null
        }
      },
    }
  }, [])
  const handlers = useMemo(
    () => ({
      onSelect: (e: MouseEvent, entry: FileEntry) => latest.current.onSelectNow(e, entry),
      onOpen: (entry: FileEntry) => latest.current.open(entry),
      onMenu: (e: MouseEvent, entry: FileEntry | null) => latest.current.onMenuNow(e, entry),
      onClear: () => setSel(emptySelection),
      onMore: (e: MouseEvent, entry: FileEntry) => {
        setSel(single(entry.path))
        setMenu({ x: e.clientX, y: e.clientY, targets: [entry] })
      },
    }),
    [],
  )
  const onColumnSelect = useCallback((d: string, e: MouseEvent, entry: FileEntry) => latest.current.onColumnSelectNow(d, e, entry), [])

  const gridColumns = () => {
    // Walk the first row only (siblings until the top changes).
    let el = containerRef.current?.querySelector<HTMLElement>('[role="option"]')
    if (!el) return 1
    const top = el.offsetTop
    let n = 0
    while (el && el.offsetTop === top && n < 64) {
      n++
      el = el.nextElementSibling as HTMLElement | null
    }
    return Math.max(1, n)
  }

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const t = e.target as HTMLElement
    if (t.closest('input, textarea, select, [contenteditable="true"]')) return
    const meta = e.metaKey || e.ctrlKey
    const k = e.key
    // Navigation keys belong to the file view only; buttons, links, menus and
    // the sidebar keep Enter, Space and arrows. ⌘I / ⌘⌫ work anywhere else
    // in the window except on controls.
    const inView = Boolean(containerRef.current?.contains(t))
    if (!inView) {
      const global = meta && (k.toLowerCase() === 'i' || k === 'Backspace' || k === 'Delete')
      if (!global || t.closest('button, a, [role="menu"], [role="menuitem"], [role="dialog"], [role="alertdialog"]')) return
    }
    const one = lead && sel.selected.size <= 1 ? lead : selected.length === 1 ? selected[0] : lead
    const step = (d: number) => {
      e.preventDefault()
      setSel((s) => moveLead(s, order_, d, e.shiftKey))
    }
    if (meta && k.toLowerCase() === 'i') {
      e.preventDefault()
      if (compact) setPhoneInfo(one ?? null)
      else setInfoOpen(!infoOpen)
      return
    }
    if (trash || !root) return
    if (meta && (k === 'Backspace' || k === 'Delete')) {
      e.preventDefault()
      askTrash(selected)
      return
    }
    if (meta && k.toLowerCase() === 'a') {
      e.preventDefault()
      setSel(selectAll(order_))
      return
    }
    if (meta && k === 'ArrowUp') {
      e.preventDefault()
      goUp()
      return
    }
    if (meta && k === 'ArrowDown') {
      e.preventDefault()
      if (one) open(one)
      return
    }
    switch (k) {
      case 'ArrowDown':
        return step(view === 'icons' ? gridColumns() : 1)
      case 'ArrowUp':
        return step(view === 'icons' ? -gridColumns() : -1)
      case 'ArrowRight':
        if (view === 'icons') return step(1)
        e.preventDefault()
        if (view === 'list' && lead?.isDir && !expanded.has(lead.path)) toggle(lead)
        else if (view === 'columns' && lead?.isDir) goTo(root, lead.path, { replace: true, select: 'first' })
        return
      case 'ArrowLeft':
        if (view === 'icons') return step(-1)
        e.preventDefault()
        if (view === 'list') {
          if (lead?.isDir && expanded.has(lead.path)) toggle(lead)
          else if (lead && parentPath(lead.path) !== path) setSel(single(parentPath(lead.path)))
        } else if (view === 'columns' && path) goTo(root, parentPath(path), { replace: true, select: path })
        return
      case 'Home':
        e.preventDefault()
        if (order_[0]) setSel(single(order_[0]))
        return
      case 'End':
        e.preventDefault()
        if (order_.length) setSel(single(order_[order_.length - 1]!))
        return
      case 'Enter':
        e.preventDefault()
        if (one) open(one)
        return
      case ' ':
        e.preventDefault()
        if (one) setQl(ql ? null : one.path)
        return
      case 'Escape':
        if (sel.selected.size) {
          e.preventDefault()
          setSel(emptySelection)
        }
        return
    }
    // Type-to-select: jump to the first name starting with the typed letters.
    if (k.length === 1 && !meta && !e.altKey && k !== ' ') {
      const now = Date.now()
      const ta = typeAhead.current
      ta.text = now - ta.at > 900 ? k : ta.text + k
      ta.at = now
      const needle = ta.text.toLocaleLowerCase(locale())
      const hit = visible.find((x) => x.name.toLocaleLowerCase(locale()).startsWith(needle)) ?? visible.find((x) => compareNames(x.name, ta.text) >= 0)
      if (hit) setSel(single(hit.path))
    }
  }

  // ---------- Menus ----------

  const menuItems = (targets: FileEntry[]): MenuEntry[] => {
    const one = targets.length === 1 ? targets[0]! : null
    const out: MenuEntry[] = []
    if (one) {
      out.push({ id: 'open', label: one.isDir ? tr('files.open') : opensItem(one) ? tr('files.openInApp') : tr('files.open'), icon: 'open', run: () => open(one) })
      out.push({ id: 'ql', label: tr('files.quickLook'), icon: 'eye', shortcut: tr('files.key.space'), run: () => setQl(one.path) })
    }
    if (targets.length) {
      out.push('separator')
      if (one && !one.isDir) out.push({ id: 'dl', label: tr('files.download'), icon: 'arrowDownCircle', run: () => root && download(root, one) })
      if (one?.item) out.push({ id: 'reveal', label: tr('files.reveal'), icon: itemGlyph(one.item.kind), run: () => navigate(itemHref(one.item!.kind, one.item!.id)) })
      out.push({
        id: 'info',
        label: tr('files.info'),
        icon: 'info',
        shortcut: '⌘I',
        run: () => (compact ? setPhoneInfo(one ?? targets[0]!) : setInfoOpen(true)),
      })
    }
    if (isAdmin) {
      out.push('separator')
      if (one) out.push({ id: 'rename', label: tr('files.op.renameEllipsis'), icon: 'pencil', run: () => setOp({ type: 'rename', entry: one }) })
      if (targets.length) out.push({ id: 'move', label: tr('files.op.moveEllipsis'), icon: 'move', run: () => setOp({ type: 'move', entries: targets }) })
      // «Новая папка» belongs to the background menu, as in Finder.
      if (!targets.length) out.push({ id: 'mkdir', label: tr('files.newFolder'), icon: 'folderPlus', run: () => setOp({ type: 'mkdir' }) })
      if (targets.length) {
        out.push('separator')
        out.push({ id: 'trash', label: tr('files.moveToTrash'), icon: 'trash', shortcut: '⌘⌫', destructive: true, run: () => askTrash(targets) })
      }
    }
    if (!targets.length) {
      out.push({ id: 'refresh', label: tr('files.refresh'), icon: 'restore', run: changed })
    }
    return out.filter((x, i, a) => !(x === 'separator' && (i === 0 || a[i - 1] === 'separator' || i === a.length - 1)))
  }

  // ---------- Rendering ----------

  if (user?.isKid) return <Navigate to="/" replace />
  if (!compact && !root && !trash && roots) {
    const first = roots.find((r) => r.id === 'movies') ?? roots[0]
    if (first) return <Navigate to={folderHref(first.id, '')} replace />
  }
  if (trash && !isAdmin) return <Navigate to="/files" replace />

  const crumbs: Crumb[] = []
  if (trash) crumbs.push({ label: tr('files.trash'), to: '/files/trash' })
  else if (root) {
    crumbs.push({ label: rootInfo?.title ?? root, to: folderHref(root, '') })
    const segs = path ? path.split('/') : []
    segs.forEach((s, i) => crumbs.push({ label: s, to: folderHref(root, segs.slice(0, i + 1).join('/')) }))
  }
  const folderName = crumbs[crumbs.length - 1]?.label ?? tr('common.files')
  const whereOf = (e: FileEntry) => [rootInfo?.title ?? root ?? '', ...parentPath(e.path).split('/').filter(Boolean)].join(' › ')
  const folderEntry: FileEntry | null =
    root && dir.data ? { name: folderName, path, isDir: true, size: 0, mtime: '', kind: 'folder', childCount: dir.data.entries.length } : null

  const container = {
    ...(compact ? longPress : {}),
    ref: containerRef,
    tabIndex: 0,
    'aria-label': q ? tr('files.searchResultsIn', { folder: folderName }) : folderName,
    'aria-activedescendant': sel.lead && byPath.has(sel.lead) ? itemId(sel.lead) : undefined,
    onFocus: () => setFocused(true),
    onBlur: (e: FocusEvent) => {
      if (!e.currentTarget.contains(e.relatedTarget as Node)) setFocused(false)
    },
  }

  const notFound = dir.status === 404 || (root && roots && !rootInfo && root !== 'library')
  const emptyNode = (
    <div className={styles.empty}>
      {q ? (
        <>
          <Glyph name="search" size={40} strokeWidth={1.4} />
          <p className={styles.emptyTitle}>{tr('files.noResults')}</p>
          <p>{tr('files.noResultsHint', { q })}</p>
        </>
      ) : (
        <>
          <Glyph name="folder" size={44} strokeWidth={1.3} />
          <p className={styles.emptyTitle}>{tr('files.folderEmpty')}</p>
        </>
      )}
    </div>
  )

  let body: ReactNode
  if (rootsError) {
    body = (
      <div className={styles.empty} role="alert">
        <p className={styles.emptyTitle}>{rootsError}</p>
        <button type="button" className={styles.retry} onClick={reloadRoots}>
          {tr('common.retry')}
        </button>
      </div>
    )
  } else if (notFound) {
    body = (
      <div className={styles.empty} role="alert">
        <Glyph name="folder" size={44} strokeWidth={1.3} />
        <p className={styles.emptyTitle}>{tr('files.folderNotFound')}</p>
        <p>{tr('files.folderNotFoundHint')}</p>
        {root && rootInfo && (
          <Link className={styles.retry} to={folderHref(root, '')}>
            {tr('files.openRoot', { title: rootInfo.title })}
          </Link>
        )}
      </div>
    )
  } else if (dir.error && !dir.data) {
    body = (
      <div className={styles.empty} role="alert">
        <p className={styles.emptyTitle}>{dir.error}</p>
        <button type="button" className={styles.retry} onClick={changed}>
          {tr('common.retry')}
        </button>
      </div>
    )
  } else if (!dir.data) {
    body = <div className={styles.loading} aria-label={tr('common.loading')} aria-busy="true" />
  } else if (view === 'icons') {
    body = <IconView {...handlers} entries={entries} selection={sel} container={container} active={focused} empty={emptyNode} compact={compact} />
  } else if (view === 'list') {
    body = (
      <ListView
        {...handlers}
        rows={q ? entries.map((entry) => ({ entry, level: 0 })) : rows}
        selection={sel}
        container={container}
        active={focused}
        empty={emptyNode}
        expanded={expanded}
        onToggle={toggle}
        sort={sort}
        order={order}
        onSort={onSort}
        compact={compact}
        showWhere={Boolean(q)}
        onMore={handlers.onMore}
      />
    )
  } else {
    body = (
      <ColumnView
        root={root!}
        path={path}
        entries={entries}
        selection={sel}
        sort={sort}
        order={order}
        container={container}
        active={focused}
        onColumnSelect={onColumnSelect}
        onOpen={handlers.onOpen}
        onMenu={handlers.onMenu}
        onClear={handlers.onClear}
        whereOf={whereOf}
        refresh={refresh}
      />
    )
  }

  // Counts what is on screen: rows of open folders included, selection limited to them.
  const total = visible.length
  const selSize = selected.reduce((a, e) => a + (e.isDir ? 0 : e.size), 0)
  const status = dir.data
    ? [
        selected.length ? tr('files.selectedOf', { n: selected.length, total }) : q ? tr('files.found', { objects: objects(total) }) : objects(total),
        selected.length && selSize ? formatSize(selSize) : '',
        dir.data.truncated ? (q ? tr('files.truncatedResults', { n: dir.data.entries.length }) : tr('files.truncated', { objects: objects(dir.data.entries.length) })) : '',
      ]
        .filter(Boolean)
        .join(' · ')
    : ''

  const overlays = (
    <>
      {ql && root && (
        <QuickLook
          root={root}
          entries={visible}
          path={ql}
          compact={compact}
          onNavigate={(p) => {
            setQl(p)
            setSel(single(p))
          }}
          onClose={() => {
            setQl(null)
            containerRef.current?.focus()
          }}
          onOpenItem={(e) => navigate(itemHref(e.item!.kind, e.item!.id))}
        />
      )}
      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          title={menu.targets.length === 1 ? menu.targets[0]!.name : menu.targets.length ? objects(menu.targets.length) : folderName}
          items={menuItems(menu.targets)}
          sheet={compact}
          onClose={(refocus) => {
            setMenu(null)
            if (refocus) containerRef.current?.focus()
          }}
        />
      )}
      {op && root && (
        <FileOps
          op={op}
          root={root}
          dir={path}
          roots={roots ?? []}
          names={(dir.data?.entries ?? []).map((e) => e.name)}
          onClose={() => setOp(null)}
          onChanged={changed}
          onDone={(r) => {
            setOp(null)
            if (r.select) setSel(single(r.select))
            else if (op.type === 'trash' || op.type === 'move') setSel(emptySelection)
            if (r.toast) setToast(r.toast)
            changed()
            containerRef.current?.focus()
          }}
        />
      )}
      {phoneInfo && root && (
        <Sheet role="dialog" dismissOnOutside title={tr('files.info')} onCancel={() => setPhoneInfo(null)} wide>
          <InfoContent root={root} entry={phoneInfo} where={whereOf(phoneInfo)} />
        </Sheet>
      )}
      {/* the design system's toast: above the tab bar and the player bar */}
      <Toast
        messageKey={toast?.text}
        action={
          toast?.action
            ? {
                label: toast.action.label,
                onClick: () => {
                  toast.action!.run()
                  setToast(null)
                },
              }
            : undefined
        }
      >
        {toast?.text}
      </Toast>
    </>
  )

  // ---------- Phone: iOS «Файлы» ----------
  if (compact) {
    if (!root && !trash) {
      return (
        <div className={styles.phone}>
          <h1 className={styles.largeTitle}>{tr('files.browse')}</h1>
          {roots ? <Sidebar roots={roots} isAdmin={isAdmin} overview usageRefresh={refresh} /> : rootsError ? body : <div className={styles.loading} />}
        </div>
      )
    }
    const parent = trash || !path ? { label: tr('files.browse'), to: '/files' } : crumbs[crumbs.length - 2]!
    return (
      <div className={styles.phone} onKeyDown={onKeyDown}>
        <div className={styles.pbar}>
          <Link to={parent.to} className={styles.pback}>
            <Glyph name="chevronLeft" size={20} strokeWidth={2.4} />
            <span>{parent.label}</span>
          </Link>
          {!trash && (
            <Menu
              label={tr('files.viewSort')}
              placement="bottom-end"
              trigger={(t) => (
                <button {...t} type="button" className={styles.pmore} aria-label={tr('files.viewSort')}>
                  <Glyph name="ellipsisCircle" size={24} strokeWidth={1.6} />
                </button>
              )}
            >
              {(close) => (
                <>
                  <MenuItem label={tr('files.view.icons')} checked={view === 'icons'} onSelect={() => (setView('icons'), close())} />
                  <MenuItem label={tr('files.view.list')} checked={view === 'list'} onSelect={() => (setView('list'), close())} />
                  <MenuSeparator />
                  {SORTS.map((s) => (
                    <MenuItem
                      key={s.key}
                      label={tr(s.label)}
                      detail={s.key === sort ? (order === 'asc' ? tr('files.sort.ascLower') : tr('files.sort.descLower')) : undefined}
                      checked={s.key === sort}
                      onSelect={() => (onSort(s.key), close())}
                    />
                  ))}
                  {isAdmin && (
                    <>
                      <MenuSeparator />
                      <MenuItem label={tr('files.newFolder')} onSelect={() => (setOp({ type: 'mkdir' }), close())} />
                    </>
                  )}
                </>
              )}
            </Menu>
          )}
        </div>
        <h1 className={styles.largeTitle}>{folderName}</h1>
        {trash ? (
          <TrashView roots={roots ?? []} compact onChanged={changed} toolbar={() => null} />
        ) : (
          <>
            <SearchField value={query} onChange={setQuery} placeholder={tr('common.search')} className={styles.psearch} />
            <div className={styles.pcontent}>{body}</div>
            {dir.data && <p className={styles.pstatus}>{status}</p>}
          </>
        )}
        {overlays}
      </div>
    )
  }

  // ---------- Desktop: a Finder window ----------
  return (
    <div className={styles.page}>
      <div className={`${styles.window} ${infoOpen && !trash ? styles.withInfo : ''}`} onKeyDown={onKeyDown}>
        {roots ? <Sidebar roots={roots} isAdmin={isAdmin} usageRefresh={refresh} /> : <div className={styles.sidebarPlaceholder} />}
        <div className={styles.main}>
          {trash ? (
            <TrashView
              roots={roots ?? []}
              onChanged={changed}
              toolbar={(buttons) => (
                <Toolbar canBack={history.canBack} canForward={history.canForward} onBack={history.back} onForward={history.forward} crumbs={crumbs}>
                  {buttons}
                </Toolbar>
              )}
            />
          ) : (
            <>
              <Toolbar
                canBack={history.canBack}
                canForward={history.canForward}
                onBack={history.back}
                onForward={history.forward}
                crumbs={crumbs}
                view={view}
                onView={(v) => {
                  setView(v)
                  requestAnimationFrame(() => containerRef.current?.focus())
                }}
                sort={sort}
                order={order}
                onSort={(k) => setSort(k)}
                onOrder={setOrder}
                query={query}
                onQuery={setQuery}
                searchPlaceholder={tr('files.searchIn', { folder: folderName })}
                infoOpen={infoOpen}
                onInfo={() => setInfoOpen(!infoOpen)}
                onNewFolder={isAdmin && root ? () => setOp({ type: 'mkdir' }) : undefined}
              />
              <div className={`${styles.content} ${view === 'columns' ? styles.contentColumns : ''}`}>{body}</div>
              <div className={styles.status}>{status}</div>
            </>
          )}
        </div>
        {infoOpen && !trash && (
          <InfoPanel root={root} selected={selected} folder={folderEntry} whereOf={whereOf} onClose={() => setInfoOpen(false)} refresh={refresh} />
        )}
      </div>
      {overlays}
    </div>
  )
}
