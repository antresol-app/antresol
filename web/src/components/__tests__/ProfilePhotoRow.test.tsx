import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { User } from '../../api/types'
import { ProfilePhotoRow } from '../ProfilePhotoRow'
import { GroupedSection } from '../../ui/List'

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
let calls: { method: string; url: string; body?: unknown; headers: Headers }[] = []
let answer: () => Response

beforeEach(() => {
  calls = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ method: init?.method ?? 'GET', url: String(input), body: init?.body, headers: new Headers(init?.headers) })
      return answer()
    }),
  )
  vi.stubGlobal('Image', class { naturalWidth = 400; naturalHeight = 400; onload: (() => void) | null = null; set src(_: string) { queueMicrotask(() => this.onload?.()) } })
  URL.createObjectURL = vi.fn(() => 'blob:fake')
  URL.revokeObjectURL = vi.fn()
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ x: 0, y: 0, left: 0, top: 0, right: 280, bottom: 280, width: 280, height: 280, toJSON: () => ({}) })
  HTMLCanvasElement.prototype.getContext = vi.fn(() => ({ fillRect: vi.fn(), drawImage: vi.fn(), set fillStyle(_: string) {}, set imageSmoothingQuality(_: string) {} })) as never
  HTMLCanvasElement.prototype.toBlob = function (cb: BlobCallback, type?: string) {
    cb(new Blob(['jpeg'], { type }))
  }
})
afterEach(() => vi.unstubAllGlobals())

const me = (over: Partial<User> = {}) => ({ id: 7, name: 'Лена', avatar: 'grad-3', ...over }) as User
function row(user: Pick<User, 'name' | 'avatar' | 'photo'>) {
  const onUser = vi.fn()
  render(
    <GroupedSection>
      <ProfilePhotoRow user={user} onUser={onUser} />
    </GroupedSection>,
  )
  return onUser
}

describe('ProfilePhotoRow', () => {
  it('without a photo offers to upload one; with one, to replace or remove it', () => {
    row(me())
    expect(screen.getByRole('button', { name: 'Загрузить фото…' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Убрать фото' })).toBeNull()
    document.body.innerHTML = ''
    row(me({ photo: '/api/profiles/7/photo?v=1' }))
    expect(screen.getByRole('button', { name: 'Заменить фото…' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Убрать фото' })).toBeInTheDocument()
    expect(document.querySelector('img')).toHaveAttribute('src', '/api/profiles/7/photo?v=1')
  })

  it('a framed photo goes up at once, as the profile’s own, and the profile that comes back is reported', async () => {
    const after = me({ photo: '/api/profiles/7/photo?v=5' })
    answer = () => json(after)
    const onUser = row(me())
    fireEvent.change(screen.getByTestId('photo-input'), { target: { files: [new File(['x'], 'me.jpg', { type: 'image/jpeg' })] } })
    const done = await screen.findByRole('button', { name: 'Готово' })
    await waitFor(() => expect(done).toBeEnabled())
    await userEvent.click(done)
    await waitFor(() => expect(onUser).toHaveBeenCalledWith(after))
    expect(calls).toHaveLength(1)
    expect(calls[0]).toMatchObject({ method: 'POST', url: '/api/me/photo' })
    expect(calls[0]!.body).toBeInstanceOf(FormData)
    expect(calls[0]!.headers.get('X-Vault-Upload')).toBe('1')
  })

  it('«Убрать фото» removes it at once', async () => {
    const after = me()
    answer = () => json(after)
    const onUser = row(me({ photo: '/api/profiles/7/photo?v=1' }))
    await userEvent.click(screen.getByRole('button', { name: 'Убрать фото' }))
    await waitFor(() => expect(onUser).toHaveBeenCalledWith(after))
    expect(calls[0]).toMatchObject({ method: 'DELETE', url: '/api/me/photo' })
  })

  it('a refusal is said on the row, and nothing is reported', async () => {
    answer = () => json({ error: 'Фото больше 8 МБ' }, 413)
    const onUser = row(me())
    fireEvent.change(screen.getByTestId('photo-input'), { target: { files: [new File(['x'], 'me.jpg', { type: 'image/jpeg' })] } })
    const done = await screen.findByRole('button', { name: 'Готово' })
    await waitFor(() => expect(done).toBeEnabled())
    await userEvent.click(done)
    expect(await screen.findByText('Фото больше 8 МБ')).toBeInTheDocument()
    expect(onUser).not.toHaveBeenCalled()
  })

  it('a file that is not a picture is refused before anything is sent', () => {
    row(me())
    fireEvent.change(screen.getByTestId('photo-input'), { target: { files: [new File(['x'], 'a.pdf', { type: 'application/pdf' })] } })
    expect(screen.getByText(/Выберите картинку/)).toBeInTheDocument()
    expect(calls).toHaveLength(0)
  })
})
