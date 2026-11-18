import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AvatarEditor, type PhotoChange } from '../AvatarEditor'

class FakeImage {
  naturalWidth = 800
  naturalHeight = 600
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  set src(_: string) {
    queueMicrotask(() => this.onload?.())
  }
}

let drawn: unknown[] = []
beforeEach(() => {
  drawn = []
  vi.stubGlobal('Image', FakeImage)
  URL.createObjectURL = vi.fn(() => 'blob:fake')
  URL.revokeObjectURL = vi.fn()
  // the frame is 280 px wide on this "screen"
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ x: 0, y: 0, left: 0, top: 0, right: 280, bottom: 280, width: 280, height: 280, toJSON: () => ({}) })
  HTMLCanvasElement.prototype.getContext = vi.fn(() => ({ fillRect: vi.fn(), drawImage: (...a: unknown[]) => drawn.push(a), set fillStyle(_: string) {}, set imageSmoothingQuality(_: string) {} })) as never
  HTMLCanvasElement.prototype.toBlob = function (cb: BlobCallback, type?: string) {
    cb(new Blob(['jpeg'], { type }))
  }
})
afterEach(() => vi.unstubAllGlobals())

function editor(props: Partial<Parameters<typeof AvatarEditor>[0]> = {}) {
  const onChange = vi.fn()
  const onAvatar = vi.fn()
  const r = render(<AvatarEditor name="Маша" avatar="grad-2" onAvatar={onAvatar} change={null} onChange={onChange} {...props} />)
  return { onChange, onAvatar, ...r }
}
const file = (name = 'me.jpg', type = 'image/jpeg', size = 1000) => {
  const f = new File(['x'], name, { type })
  Object.defineProperty(f, 'size', { value: size })
  return f
}

describe('AvatarEditor', () => {
  it('without a photo offers to upload one, and keeps the eight colours for the letter', () => {
    editor()
    expect(screen.getByRole('button', { name: 'Загрузить фото…' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Убрать фото' })).toBeNull()
    expect(screen.getAllByRole('radio')).toHaveLength(8)
    expect(screen.getByRole('radio', { name: 'Цвет 2' })).toBeChecked()
  })

  it('picking a colour is reported', async () => {
    const { onAvatar } = editor()
    await userEvent.click(screen.getByRole('radio', { name: 'Цвет 5' }))
    expect(onAvatar).toHaveBeenCalledWith('grad-5')
  })

  it('with a photo shows it, offers to replace and remove it; removal is staged, not done', async () => {
    const { onChange, container } = editor({ photo: '/api/profiles/3/photo?v=1' })
    expect(container.querySelector('img')).toHaveAttribute('src', '/api/profiles/3/photo?v=1')
    expect(screen.getByRole('button', { name: 'Заменить фото…' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Убрать фото' }))
    expect(onChange).toHaveBeenCalledWith({ kind: 'remove' })
  })

  it('a staged removal shows the letter again, with «Загрузить» back', () => {
    const { container } = editor({ photo: '/p?v=1', change: { kind: 'remove' } })
    expect(container.querySelector('img')).toBeNull()
    expect(screen.getByRole('button', { name: 'Загрузить фото…' })).toBeInTheDocument()
  })

  it('a staged new photo is previewed from its own bytes; removing it with no photo before drops the stage', async () => {
    const change: PhotoChange = { kind: 'set', blob: new Blob(['j'], { type: 'image/jpeg' }) }
    const { onChange, container } = editor({ change })
    await waitFor(() => expect(container.querySelector('img')).toHaveAttribute('src', 'blob:fake'))
    await userEvent.click(screen.getByRole('button', { name: 'Убрать фото' }))
    expect(onChange).toHaveBeenCalledWith(null)
  })

  it('refuses a file that is not a picture, and one that is too big, without opening the frame', () => {
    editor()
    const input = screen.getByTestId('photo-input')
    fireEvent.change(input, { target: { files: [file('a.pdf', 'application/pdf')] } })
    expect(screen.getByRole('alert')).toHaveTextContent('Выберите картинку')
    fireEvent.change(input, { target: { files: [file('big.jpg', 'image/jpeg', 30 * 1024 * 1024)] } })
    expect(screen.getByRole('alert')).toHaveTextContent('больше 25 МБ')
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('a picture opens the frame; «Готово» stages the framed square as a JPEG', async () => {
    const { onChange } = editor()
    fireEvent.change(screen.getByTestId('photo-input'), { target: { files: [file()] } })
    const done = await screen.findByRole('button', { name: 'Готово' })
    await waitFor(() => expect(done).toBeEnabled())
    await userEvent.click(done)
    await waitFor(() => expect(onChange).toHaveBeenCalledTimes(1))
    const staged = onChange.mock.calls[0]![0] as PhotoChange
    expect(staged).toMatchObject({ kind: 'set' })
    expect((staged as { blob: Blob }).blob.type).toBe('image/jpeg')
    // 800x600 at zoom 1: the middle 600 px square, shrunk to the 512 the server keeps
    const [, sx, sy, size, , dx, dy, out] = drawn[0] as number[]
    expect([sx, Math.abs(sy!), size]).toEqual([expect.closeTo(100), expect.closeTo(0), expect.closeTo(600)])
    expect([dx, dy, out]).toEqual([0, 0, 512])
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('Cancel in the frame leaves the form as it was', async () => {
    const { onChange } = editor()
    fireEvent.change(screen.getByTestId('photo-input'), { target: { files: [file()] } })
    await screen.findByRole('dialog')
    await userEvent.click(screen.getByRole('button', { name: 'Отмена' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(onChange).not.toHaveBeenCalled()
  })

  it('the frame zooms with its buttons and the keyboard, and the zoom buttons stop at the ends', async () => {
    editor()
    fireEvent.change(screen.getByTestId('photo-input'), { target: { files: [file()] } })
    const frame = await screen.findByTestId('photo-frame')
    const out = screen.getByRole('button', { name: 'Уменьшить' })
    const inn = screen.getByRole('button', { name: 'Увеличить' })
    await waitFor(() => expect(inn).toBeEnabled())
    expect(out).toBeDisabled() // already the whole short side
    await userEvent.click(inn)
    expect(out).toBeEnabled()
    frame.focus()
    for (let i = 0; i < 12; i++) fireEvent.keyDown(frame, { key: '+' })
    expect(inn).toBeDisabled()
  })
})
