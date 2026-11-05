import { describe, expect, it, vi } from 'vitest'
import type { View } from '../../vendor/foliate/view.js'
import { HighlightLayer } from '../highlights'

function fakeView() {
  const view = new EventTarget() as unknown as View & EventTarget
  const added: string[] = []
  const removed: string[] = []
  Object.assign(view, {
    addAnnotation: vi.fn(async (a: { value: string }) => void added.push(a.value)),
    deleteAnnotation: vi.fn(async (a: { value: string }) => void removed.push(a.value)),
    resolveNavigation: () => ({ index: 0 }),
  })
  return { view, added, removed }
}

const range = 'epubcfi(/6/4!/4,/1:0,/1:5)'

describe('HighlightLayer', () => {
  it('keeps highlights by id: two on the same text, one removed, the other stays drawn', () => {
    const { view, added, removed } = fakeView()
    const onTap = vi.fn()
    const layer = new HighlightLayer(view, onTap)
    layer.set([
      { id: 'a', locator: range, color: 'yellow' },
      { id: 'b', locator: range, color: 'green' },
    ])
    expect(layer.has('a') && layer.has('b')).toBe(true)
    expect(added).toEqual([range])
    // a tap on the range names the one shown (the last)
    view.dispatchEvent(new CustomEvent('show-annotation', { detail: { value: range } }))
    expect(onTap).toHaveBeenCalledWith('b', range, null)

    layer.set([{ id: 'a', locator: range, color: 'yellow' }])
    expect(removed).toEqual([]) // the range still has a highlight: redrawn, not deleted
    expect(added).toEqual([range, range])
    layer.set([])
    expect(removed).toEqual([range])
    layer.destroy()
  })
})
