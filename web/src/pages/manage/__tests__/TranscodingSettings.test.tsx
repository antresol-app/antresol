// «Настройки сервера → Перекодирование»: the mode, the device, tone mapping
// and HEVC are saved to /api/settings/transcoding; «Сейчас» says what the
// server picked; «Проверить заново» probes again; a changed setup can go
// back to the server's own.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { TranscodingView } from '../../../api/types'
import { TranscodingSection } from '../TranscodingSettings'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const view = (patch: Partial<TranscodingView> = {}): TranscodingView => ({
  settings: { mode: 'auto', device: '', tonemap: true, hevc: true },
  defaults: { mode: 'auto', device: '', tonemap: true, hevc: true },
  custom: false,
  devices: ['/dev/dri/renderD128', '/dev/dri/renderD129'],
  status: {
    mode: 'qsv',
    name: 'Intel Quick Sync',
    requested: 'auto',
    device: '/dev/dri/renderD128',
    hevc: true,
    tonemapGpu: true,
    tonemapCpu: true,
    probes: [{ mode: 'qsv', ok: true }],
    probedAt: '2026-11-14T15:00:00Z',
  },
  ...patch,
})

function stub(first: TranscodingView, after: (body: unknown, path: string) => TranscodingView) {
  const calls: { method: string; path: string; body: unknown }[] = []
  const f = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input), 'http://x').pathname
    const method = init?.method ?? 'GET'
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    calls.push({ method, path, body })
    if (method === 'GET') return json(first)
    return json(after(body, path))
  })
  vi.stubGlobal('fetch', f)
  return calls
}

afterEach(() => vi.unstubAllGlobals())

describe('transcoding settings', () => {
  it('says whether ffmpeg can find intros (chromaprint)', async () => {
    stub(view({ fingerprint: false }), () => view())
    render(<TranscodingSection />)
    expect(await screen.findByText('Поиск заставок')).toBeInTheDocument()
    expect(screen.getByText(/В ffmpeg нет chromaprint/)).toBeInTheDocument()
    cleanup()
    stub(view({ fingerprint: true }), () => view())
    render(<TranscodingSection />)
    expect(await screen.findByText('ffmpeg с chromaprint: заставки и титры серий находятся по звуку')).toBeInTheDocument()
    cleanup()
    // an older server says nothing: no row
    stub(view(), () => view())
    render(<TranscodingSection />)
    await screen.findByText('Сейчас')
    expect(screen.queryByText('Поиск заставок')).toBeNull()
  })

  it('shows what the server picked and its test encode', async () => {
    stub(view(), () => view())
    render(<TranscodingSection />)
    expect(await screen.findByText('Видеокарта Intel Quick Sync: аппаратное декодирование и кодирование работают · HEVC · тонмаппинг на видеокарте')).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'Тонмаппинг HDR' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('switch', { name: 'HEVC на выходе' })).toHaveAttribute('aria-checked', 'true')
    // the device row: there are two render nodes
    expect(screen.getByText('Устройство')).toBeInTheDocument()
    // not changed here: no way back
    expect(screen.queryByText('Как в настройках запуска')).toBeNull()
  })

  it('a failed explicit test encode is said, and the CPU when software', async () => {
    stub(view({ settings: { mode: 'vaapi', device: '', tonemap: true, hevc: true }, status: { ...view().status, mode: 'vaapi', name: 'VA-API', hevc: false, tonemapGpu: false, probes: [{ mode: 'vaapi', ok: false, error: 'no device' }] } }), () => view())
    const { unmount } = render(<TranscodingSection />)
    expect(await screen.findByText('Видеокарта VA-API не прошла пробу — видео перекодирует процессор')).toBeInTheDocument()
    unmount()
    stub(view({ devices: ['/dev/dri/renderD128'], status: { ...view().status, mode: 'none', name: 'software', hevc: false, tonemapGpu: false, probes: [] } }), () => view())
    render(<TranscodingSection />)
    expect(await screen.findByText('Процессор: видеокарта не найдена')).toBeInTheDocument()
    // one render node: nothing to choose
    expect(screen.queryByText('Устройство')).toBeNull()
    cleanup()
    // no render node in the container: say how to give it one
    stub(view({ devices: [], status: { ...view().status, mode: 'none', name: 'software', hevc: false, tonemapGpu: false, probes: [] } }), () => view())
    render(<TranscodingSection />)
    expect(await screen.findByText('Процессор: контейнер не видит видеокарту — откройте /dev/dri в compose.yml')).toBeInTheDocument()
  })

  it('a toggle saves the whole settings and shows the new state', async () => {
    const calls = stub(view(), (body) => view({ custom: true, settings: body as TranscodingView['settings'], status: { ...view().status, hevc: false } }))
    render(<TranscodingSection />)
    await userEvent.click(await screen.findByRole('switch', { name: 'HEVC на выходе' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true))
    expect(calls.find((c) => c.method === 'PUT')).toEqual({ method: 'PUT', path: '/api/settings/transcoding', body: { mode: 'auto', device: '', tonemap: true, hevc: false } })
    expect(await screen.findByText('Видеокарта Intel Quick Sync: аппаратное декодирование и кодирование работают · тонмаппинг на видеокарте')).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'HEVC на выходе' })).toHaveAttribute('aria-checked', 'false')
    // changed here: back to the server's own
    await userEvent.click(screen.getByRole('button', { name: 'Как в настройках запуска' }))
    await waitFor(() => expect(calls.at(-1)).toEqual({ method: 'PUT', path: '/api/settings/transcoding', body: { reset: true } }))
  })

  it('the mode is picked from the list', async () => {
    const calls = stub(view(), (body) => view({ custom: true, settings: body as TranscodingView['settings'] }))
    render(<TranscodingSection />)
    await userEvent.click(await screen.findByRole('button', { name: /Ускорение/ }))
    await userEvent.click(await screen.findByRole('menuitemradio', { name: 'VA-API' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ mode: 'vaapi', device: '', tonemap: true, hevc: true }))
  })

  it('«Проверить заново» probes again', async () => {
    const calls = stub(view(), () => view({ status: { ...view().status, probedAt: '2026-11-14T16:00:00Z' } }))
    render(<TranscodingSection />)
    await userEvent.click(await screen.findByRole('button', { name: 'Проверить заново' }))
    await waitFor(() => expect(calls.at(-1)).toMatchObject({ method: 'POST', path: '/api/settings/transcoding/probe' }))
  })
})

describe('transcoding settings: no GPU passed', () => {
  it('says what to uncomment in compose.yml, only when the container sees no GPU at all', async () => {
    const none = { ...view().status, mode: 'none' as const, name: 'software', hevc: false, tonemapGpu: false, probes: [] }
    stub(view({ devices: [], status: none }), () => view())
    const { unmount } = render(<TranscodingSection />)
    expect(await screen.findByText(/Видеокарту в контейнер не передали/)).toBeInTheDocument()
    expect(screen.getByText((_, el) => el?.tagName === 'PRE' && el.textContent === 'devices:\n  - /dev/dri:/dev/dri')).toBeInTheDocument()
    unmount()
    // a GPU is there but failed its test encode: another cause, no compose hint
    stub(view({ devices: ['/dev/dri/renderD128'], status: { ...none, probes: [{ mode: 'vaapi', ok: false, error: 'no device' }] } }), () => view())
    render(<TranscodingSection />)
    expect(await screen.findByText('Процессор: видеокарта не прошла пробу')).toBeInTheDocument()
    expect(screen.queryByText(/Видеокарту в контейнер не передали/)).toBeNull()
    cleanup()
    // the CPU's tone mapping failing says nothing about a GPU: none was passed
    stub(view({ devices: [], status: { ...none, probes: [{ mode: 'tonemap-cpu', ok: false, error: 'No such filter: zscale' }] } }), () => view())
    render(<TranscodingSection />)
    expect(await screen.findByText('Процессор: контейнер не видит видеокарту — откройте /dev/dri в compose.yml')).toBeInTheDocument()
    expect(screen.getByText(/Видеокарту в контейнер не передали/)).toBeInTheDocument()
  })
})

describe('transcoding settings: why the GPU failed', () => {
  const none = { ...view().status, mode: 'none' as const, name: 'software', hevc: false, tonemapGpu: false }
  const detail = '[AVHWDeviceContext @ 0x1] No VA display found for device /dev/dri/renderD128.'

  it('no permission to the render node: the one-line reason and the group_add with the node’s group', async () => {
    stub(view({ devices: ['/dev/dri/renderD128'], status: { ...none, probes: [{ mode: 'vaapi', ok: false, error: detail }],
      problem: { kind: 'permission', mode: 'vaapi', device: '/dev/dri/renderD128', gid: 105, encoder: 'h264_vaapi', detail } } }), () => view())
    render(<TranscodingSection />)
    expect(await screen.findByText('Процессор: нет доступа к /dev/dri/renderD128 — добавьте в group_add группу 105')).toBeInTheDocument()
    expect(screen.getByText(/принадлежит группе 105/)).toBeInTheDocument()
    expect(screen.getByText((_, el) => el?.tagName === 'PRE' && el.textContent === 'group_add:\n  - "105"')).toBeInTheDocument()
    // ffmpeg's own words, to copy into a report
    expect(screen.getByText(detail)).toBeInTheDocument()
  })

  it('a driver that takes no GPU names the vendor’s driver; an explicit mode says it too', async () => {
    stub(view({ devices: ['/dev/dri/renderD128'], status: { ...none, probes: [{ mode: 'vaapi', ok: false, error: detail }],
      problem: { kind: 'driver', mode: 'vaapi', device: '/dev/dri/renderD128', driver: 'radeonsi (mesa-va-drivers)', encoder: 'h264_vaapi', detail } } }), () => view())
    const { unmount } = render(<TranscodingSection />)
    expect(await screen.findByText('Процессор: драйвер видеокарты не загрузился (нужен radeonsi (mesa-va-drivers))')).toBeInTheDocument()
    expect(screen.getByText('LIBVA_DRIVER_NAME=radeonsi')).toBeInTheDocument()
    unmount()
    stub(view({ settings: { mode: 'nvenc', device: '', tonemap: true, hevc: true }, status: { ...view().status, mode: 'nvenc', requested: 'nvenc', name: 'NVIDIA NVENC',
      probes: [{ mode: 'nvenc', ok: false, error: 'Cannot load libcuda.so.1' }], problem: { kind: 'nvidia', mode: 'nvenc', encoder: 'h264_nvenc', detail: 'Cannot load libcuda.so.1' } } }), () => view())
    render(<TranscodingSection />)
    expect(await screen.findByText('Видеокарта NVIDIA NVENC не прошла пробу: в контейнере нет драйвера NVIDIA — нужен NVIDIA Container Toolkit')).toBeInTheDocument()
  })
})
