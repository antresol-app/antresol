// «Профили → Изменить»: the photo is staged in the form and sent with Save,
// after the profile itself; the list shows it beside the name.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { User } from '../../../api/types'
import { Profiles } from '../Profiles'

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const user = (over: Partial<User> = {}) => ({ id: 3, name: 'Миша', avatar: 'grad-2', hasPassword: false, isAdmin: false, isKid: false, maxRating: null, ...over }) as User

let calls: { method: string; url: string; body?: unknown }[] = []
let profile: User
function show(photoFails = false) {
  calls = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      calls.push({ method, url, body: init?.body })
      if (url.endsWith('/api/users') && method === 'GET') return json([profile])
      if (url.endsWith('/api/screen-time/limited')) return json({ userIds: [], passwordless: [] })
      if (url.endsWith('/api/users/3') && method === 'PATCH') return json(profile)
      if (url.endsWith('/api/users/3/photo') && method === 'POST') return photoFails ? json({ error: 'Фото больше 8 МБ' }, 413) : json((profile = { ...profile, photo: '/api/profiles/3/photo?v=5' }))
      if (url.endsWith('/api/users/3/photo') && method === 'DELETE') return json((profile = { ...profile, photo: undefined }))
      return json({ error: 'нет' }, 404)
    }),
  )
  render(
    <MemoryRouter>
      <Profiles />
    </MemoryRouter>,
  )
}

beforeEach(() => {
  profile = user()
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

async function openForm() {
  await userEvent.click((await screen.findAllByRole('button', { name: /Изменить/ }))[0]!)
  return screen.findByRole('dialog', { name: 'Изменить профиль' })
}

describe('Profiles: the photo', () => {
  it('the row shows the photo', async () => {
    profile = user({ photo: '/api/profiles/3/photo?v=1' })
    show()
    const img = await waitFor(() => {
      const i = document.querySelector('ul img')
      expect(i).not.toBeNull()
      return i!
    })
    expect(img).toHaveAttribute('src', '/api/profiles/3/photo?v=1')
  })

  it('a picked and framed photo goes up with Save, after the profile', async () => {
    show()
    await openForm()
    fireEvent.change(screen.getByTestId('photo-input'), { target: { files: [new File(['x'], 'me.jpg', { type: 'image/jpeg' })] } })
    const done = await screen.findByRole('button', { name: 'Готово' })
    await waitFor(() => expect(done).toBeEnabled())
    await userEvent.click(done)
    // nothing is sent until Save
    await waitFor(() => expect(screen.getByRole('button', { name: 'Убрать фото' })).toBeInTheDocument())
    expect(calls.filter((c) => c.method !== 'GET')).toEqual([])
    await userEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(calls.some((c) => c.url.endsWith('/api/users/3/photo') && c.method === 'POST')).toBe(true))
    const writes = calls.filter((c) => c.method !== 'GET').map((c) => `${c.method} ${c.url}`)
    expect(writes).toEqual(['PATCH /api/users/3', 'POST /api/users/3/photo'])
    expect(calls.find((c) => c.url.endsWith('/photo'))!.body).toBeInstanceOf(FormData)
  })

  it('«Убрать фото» is staged too, and sent with Save', async () => {
    profile = user({ photo: '/api/profiles/3/photo?v=1' })
    show()
    await openForm()
    await userEvent.click(screen.getByRole('button', { name: 'Убрать фото' }))
    expect(calls.filter((c) => c.method === 'DELETE')).toEqual([])
    await userEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE' && c.url.endsWith('/api/users/3/photo'))).toBe(true))
  })

  it('Cancel sends nothing', async () => {
    show()
    await openForm()
    fireEvent.change(screen.getByTestId('photo-input'), { target: { files: [new File(['x'], 'me.jpg', { type: 'image/jpeg' })] } })
    const done = await screen.findByRole('button', { name: 'Готово' })
    await waitFor(() => expect(done).toBeEnabled())
    await userEvent.click(done)
    await userEvent.click(screen.getAllByRole('button', { name: 'Отмена' })[0]!)
    expect(calls.filter((c) => c.method !== 'GET')).toEqual([])
  })

  it('a refused photo says why and keeps the form open', async () => {
    show(true)
    await openForm()
    fireEvent.change(screen.getByTestId('photo-input'), { target: { files: [new File(['x'], 'me.jpg', { type: 'image/jpeg' })] } })
    const done = await screen.findByRole('button', { name: 'Готово' })
    await waitFor(() => expect(done).toBeEnabled())
    await userEvent.click(done)
    await userEvent.click(await screen.findByRole('button', { name: 'Сохранить' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Фото больше 8 МБ')
    expect(screen.getByRole('dialog', { name: 'Изменить профиль' })).toBeInTheDocument()
  })
})
