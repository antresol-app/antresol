// «Коллекция «…»» and «Похожие» under the cast of a movie's or series' page:
// the shelves of Главная, filled after the page (never holding it up) and
// hidden while empty. The server keeps a kid to its rating and leaves titles
// outside the library to the sources build.
import { useEffect, useState } from 'react'
import { getRelated } from '../api/client'
import type { RelatedRows } from '../api/types'
import { useT } from '../i18n'
import { Shelf } from './Shelf'

/** The rows of a library title; `stamp` changes when the page reloads (watched marks follow). */
export function useRelated(itemId: number, enabled: boolean, stamp?: unknown): RelatedRows | null {
  const [rows, setRows] = useState<RelatedRows | null>(null)
  useEffect(() => {
    if (!enabled || !(itemId > 0)) {
      setRows(null)
      return
    }
    const ac = new AbortController()
    getRelated(itemId, ac.signal)
      .then((r) => !ac.signal.aborted && setRows(r))
      .catch(() => {}) // no rows: the page stands without them
    return () => ac.abort()
  }, [itemId, enabled, stamp])
  // a new page starts without the previous title's rows
  useEffect(() => setRows(null), [itemId])
  return rows
}

export function RelatedShelves({ itemId, rows }: { itemId: number; rows: RelatedRows | null }) {
  const t = useT()
  if (!rows) return null
  const collection = rows.collection
  return (
    <>
      {collection && collection.parts.length > 0 && (
        <Shelf
          title={t('item.collection', { name: collection.name })}
          cards={collection.parts}
          seeAllTo={`/item/${itemId}/collection`}
        />
      )}
      <Shelf title={t('item.similar')} cards={rows.similar ?? []} />
    </>
  )
}
