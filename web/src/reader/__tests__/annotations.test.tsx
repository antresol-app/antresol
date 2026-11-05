import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { Annotation } from '../../api/types'
import { AnnotationsSheet, NoteSheet, SelectionMenu } from '../Annotations'
import { ReaderChrome } from '../ReaderChrome'

const base: Omit<Annotation, 'id' | 'kind' | 'locator'> = {
  fraction: 0.25,
  label: 'Глава 1',
  excerpt: '',
  note: '',
  color: '',
  createdAt: '2026-11-01T10:00:00.000Z',
  updatedAt: '2026-11-01T10:00:00.000Z',
  rev: 1,
  deleted: false,
}
const mark: Annotation = { ...base, id: 'b', kind: 'bookmark', locator: 'epubcfi(/6/4!/4/2/1:0)' }
const hl: Annotation = { ...base, id: 'h', kind: 'note', locator: 'epubcfi(/6/4!/4,/1:0,/1:5)', excerpt: 'Никогда не разговаривайте', note: 'с неизвестными', color: 'green', fraction: 0.5 }

describe('«Закладки и заметки»', () => {
  it('lists bookmarks and highlights apart, jumps, edits and deletes', () => {
    const onJump = vi.fn()
    const onRemove = vi.fn()
    const onEditNote = vi.fn()
    render(<AnnotationsSheet list={[mark, hl]} highlights onJump={onJump} onRemove={onRemove} onEditNote={onEditNote} exportUrl="/api/annotations/9/export.md" onClose={() => {}} />)
    // bookmarks first
    fireEvent.click(screen.getByRole('button', { name: /Глава 1 · 25/ }))
    expect(onJump).toHaveBeenCalledWith(mark)
    expect(screen.getByRole('link', { name: 'Экспорт в Markdown' }).getAttribute('href')).toBe('/api/annotations/9/export.md')

    fireEvent.click(screen.getByRole('radio', { name: 'Выделения' }))
    expect(screen.getByText('Никогда не разговаривайте')).toBeTruthy()
    expect(screen.getByText('с неизвестными')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Изменить заметку' }))
    expect(onEditNote).toHaveBeenCalledWith(hl)
    fireEvent.click(screen.getByRole('button', { name: 'Удалить: Никогда не разговаривайте' }))
    expect(onRemove).toHaveBeenCalledWith(hl)
  })

  it('marks one the server refused', () => {
    render(<AnnotationsSheet list={[mark]} failed={new Set(['b'])} onJump={() => {}} onRemove={() => {}} onClose={() => {}} />)
    expect(screen.getByRole('status')).toHaveTextContent('Не удалось сохранить заметку')
  })

  it('says how to make the first one', () => {
    render(<AnnotationsSheet list={[]} onJump={() => {}} onRemove={() => {}} onClose={() => {}} />)
    expect(screen.getByText(/Закладок пока нет/)).toBeTruthy()
    expect(screen.queryByRole('radio')).toBeNull() // PDF: bookmarks only
  })
})

describe('the selection menu', () => {
  it('is a toolbar: arrows move, a colour highlights, Esc closes', () => {
    const onHighlight = vi.fn()
    const onClose = vi.fn()
    render(<SelectionMenu rect={{ x: 300, y: 300, width: 80, height: 20 }} autoFocus onHighlight={onHighlight} onNote={() => {}} onClose={onClose} />)
    const bar = screen.getByRole('toolbar', { name: 'Выделенный текст' })
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Выделить: жёлтый' }))
    fireEvent.keyDown(bar, { key: 'ArrowRight' })
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Выделить: зелёный' }))
    fireEvent.keyDown(bar, { key: 'ArrowLeft' })
    fireEvent.keyDown(bar, { key: 'ArrowLeft' })
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Заметка' }))
    fireEvent.click(screen.getByRole('button', { name: 'Выделить: синий' }))
    expect(onHighlight).toHaveBeenCalledWith('blue')
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: 'Убрать выделение' })).toBeNull()
  })

  it('a note saves its text and colour with ⌘Enter', () => {
    const onSave = vi.fn()
    render(<NoteSheet excerpt="Слово" note="" color="yellow" onSave={onSave} onClose={() => {}} />)
    const field = screen.getByRole('textbox', { name: 'Заметка' })
    fireEvent.change(field, { target: { value: ' мысль ' } })
    fireEvent.click(screen.getByRole('radio', { name: 'розовый' }))
    fireEvent.keyDown(field, { key: 'Enter', metaKey: true })
    expect(onSave).toHaveBeenCalledWith('мысль', 'pink')
  })
})

describe('the bookmark button', () => {
  it('says what it does and shows a bookmarked page', () => {
    const onBookmark = vi.fn()
    const { rerender } = render(
      <ReaderChrome title="Книга" visible theme="night" onBack={() => {}} chapter="" fraction={0.2} onLockChange={() => {}} onBookmark={onBookmark} bookmarked={false} />,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Добавить закладку' }))
    expect(onBookmark).toHaveBeenCalled()
    rerender(<ReaderChrome title="Книга" visible theme="night" onBack={() => {}} chapter="" fraction={0.2} onLockChange={() => {}} onBookmark={onBookmark} bookmarked />)
    expect(screen.getByRole('button', { name: 'Убрать закладку' }).getAttribute('aria-pressed')).toBe('true')
  })
})
