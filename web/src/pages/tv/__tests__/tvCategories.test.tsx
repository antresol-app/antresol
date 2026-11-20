import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import type { ChannelCategory } from '../../../api/channels'
import { ChannelCategories, categorySections } from '../ChannelCategories'
import { ADULT, applyFilter, categoryList } from '../model'

const ch = (number: number, categories: string[], favorite = false) => ({ id: number, number, name: `C${number}`, category: '', categories, favorite })

const cats: ChannelCategory[] = [
  { id: ADULT, name: 'Для взрослых', kind: 'ai', count: 1, exclusive: true },
  { id: 'ai:movies', name: 'Кино', kind: 'ai', count: 2 },
  { id: 'source:1', name: 'A', kind: 'source', count: 2 },
  { id: 'group:Премиум', name: 'Премиум', kind: 'group', count: 1 },
]

describe('the adult category', () => {
  const list = [ch(1, ['ai:movies']), ch(2, ['ai:movies', 'group:Премиум'], true), ch(3, [ADULT], true)]
  it('is in no other list', () => {
    expect(applyFilter(list, { kind: 'all' }).map((c) => c.id)).toEqual([1, 2])
    expect(applyFilter(list, { kind: 'fav' }).map((c) => c.id)).toEqual([2])
    expect(applyFilter(list, { kind: 'cat', id: ADULT }).map((c) => c.id)).toEqual([3])
  })
  it('comes last, after the sources', () => {
    expect(categoryList(list, cats).at(-1)?.id).toBe(ADULT)
    const t = ((k: string) => k) as never
    const keys = categorySections(cats, 2, 1, t).flatMap((s) => s.rows.map((r) => r.key))
    expect(keys).toEqual(['fav', 'all', 'k:ai:movies', 'k:group:Премиум', 'k:source:1', `k:${ADULT}`])
  })
})

describe('the library and the languages', () => {
  it('«Из медиатеки» joins the lists, the languages have a section of their own', () => {
    const t = ((k: string) => k) as never
    const more: ChannelCategory[] = [
      { id: 'own:auto', name: 'Из медиатеки', kind: 'own', count: 3 },
      ...cats,
      { id: 'lang:de', name: 'Немецкие (DE)', kind: 'lang', count: 2 },
    ]
    const secs = categorySections(more, 5, 1, t)
    expect(secs.map((s) => s.title)).toEqual([undefined, 'tv.categoryGroup.ai', 'tv.categoryGroup.lang', 'tv.categoryGroup.source', undefined])
    expect(secs[0]!.rows.map((r) => r.key)).toEqual(['fav', 'all', 'k:own:auto'])
    expect(secs[2]!.rows.map((r) => r.key)).toEqual(['k:lang:de'])
  })
})

function Where() {
  const l = useLocation()
  return <output data-testid="where">{l.search}</output>
}

describe('the rail', () => {
  it('moves with the arrows and opens each category, → enters the list', () => {
    let entered = 0
    render(
      <MemoryRouter initialEntries={['/tv?f=all']}>
        <Routes>
          <Route
            path="/tv"
            element={
              <>
                <ChannelCategories variant="rail" categories={cats} all={2} favorites={1} current="all" href={(f) => `/tv?f=${encodeURIComponent(f)}`} onEnterList={() => entered++} />
                <Where />
              </>
            }
          />
        </Routes>
      </MemoryRouter>,
    )
    const all = screen.getByRole('link', { name: /Все каналы/ })
    expect(all).toHaveAttribute('aria-current', 'page')
    expect(all).toHaveAttribute('tabindex', '0')
    all.focus()
    fireEvent.keyDown(all, { key: 'ArrowDown' })
    expect(document.activeElement).toHaveTextContent('Кино')
    expect(screen.getByTestId('where').textContent).toBe(`?f=${encodeURIComponent('k:ai:movies')}`)
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowUp' })
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowUp' })
    expect(document.activeElement).toHaveTextContent('Избранное')
    fireEvent.keyDown(document.activeElement!, { key: 'End' })
    expect(document.activeElement).toHaveTextContent('Для взрослых')
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowRight' })
    expect(entered).toBe(1)
  })
})
