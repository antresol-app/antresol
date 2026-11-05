// Back / forward of the Finder toolbar. The browser history is the source of
// truth (so the browser's own buttons and swipes work too); this mirrors the
// part of it visited inside «Файлы» to know when back / forward are possible.
import { useEffect, useRef, useState } from 'react'
import { useNavigate, useNavigationType } from 'react-router'

export function useFolderHistory(href: string) {
  const navigate = useNavigate()
  const navType = useNavigationType()
  const st = useRef({ stack: [href], idx: 0 })
  const [, force] = useState(0)

  useEffect(() => {
    const s = st.current
    if (s.stack[s.idx] === href) return
    if (navType === 'POP') {
      if (s.stack[s.idx - 1] === href) s.idx--
      else if (s.stack[s.idx + 1] === href) s.idx++
      else {
        s.stack = [href]
        s.idx = 0
      }
    } else if (navType === 'REPLACE') {
      s.stack[s.idx] = href
    } else {
      s.stack = [...s.stack.slice(0, s.idx + 1), href]
      s.idx++
    }
    force((n) => n + 1)
  }, [href, navType])

  const s = st.current
  return {
    canBack: s.idx > 0,
    canForward: s.idx < s.stack.length - 1,
    back: () => navigate(-1),
    forward: () => navigate(1),
  }
}
