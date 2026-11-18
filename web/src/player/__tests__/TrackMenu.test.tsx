// The addons' rows of the player's track menu (playerTrackActions): the
// audio column's last rows while a library movie or episode plays, of the
// item the player names; never during online playback, never for a kid
// (a library-only profile gets them: libraryOnly gates online only). An
// element a row returns is drawn over the player until it closes.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { MediaFile, User } from '../../api/types'
import { registerAddon, resetAddonsForTests, type TrackActionItem } from '../../addons/registry'
import { session } from '../../state/session'
import { Sheet } from '../../ui/Sheet'

vi.mock('hls.js', () => {
  class FakeHls {
    static Events = { ERROR: 'error' }
    static ErrorTypes = { NETWORK_ERROR: 'networkError' }
    static isSupported = () => true
    on() {}
    loadSource() {}
    attachMedia() {}
    destroy() {}
  }
  return { default: FakeHls }
})

import { VideoPlayer, type StreamPlayback } from '../VideoPlayer'

const adult: User = { id: 2, name: 'Мама', avatar: 'grad-1', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const kid: User = { ...adult, id: 3, name: 'Вася', isKid: true }
const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const series: TrackActionItem = { id: 40, title: 'Миллиарды', kind: 'series' }

async function open(user: User, o: { stream?: StreamPlayback } = {}) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => (String(input).endsWith('/api/login') ? json(user) : json({}))),
  )
  await session.login(user.id)
  const start = vi.fn(async () => ({
    mode: 'direct', url: '/api/stream/5', startSec: 0, durationMs: 60000, audioIndex: 1,
    audio: [{ index: 1, codec: 'aac', lang: 'eng', title: '', channels: 2, default: true }], subtitles: [],
  }))
  render(
    <VideoPlayer itemId={41} title="Миллиарды" parts={[{ id: 5, name: 'e1.mkv', durationMs: 60000 } as MediaFile]} startSec={0}
      onClose={() => {}} start={start as never} trackActionItem={series} stream={o.stream} />,
  )
  await waitFor(() => expect(start).toHaveBeenCalled())
  await userEvent.click(screen.getByRole('button', { name: 'Аудио и субтитры' }))
  return screen.findByRole('menu', { name: 'Аудио и субтитры' })
}

const seen: TrackActionItem[] = []
function registerFake() {
  registerAddon('fake', {
    strings: { ru: { 'track.find': 'Найти дорожку…' }, en: {} },
    playerTrackActions: [
      {
        label: 'track.find',
        icon: 'search',
        run: (it, close) => {
          seen.push(it)
          return <Sheet title="Дорожки дополнения" role="dialog" onCancel={close} actions={[{ label: 'Закрыть', kind: 'cancel', run: close }]} />
        },
      },
      { label: 'Скрытая', visible: () => false, run: () => undefined },
    ],
  })
}

describe('the player’s track menu: addon rows', () => {
  beforeEach(() => {
    session.reset()
    resetAddonsForTests()
    localStorage.clear()
    seen.length = 0
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('track actions from addons', async () => {
    registerFake()
    const menu = await open(adult)
    const audio = within(menu).getByRole('group', { name: 'Аудио' })
    const items = Array.from(audio.querySelectorAll<HTMLElement>('[role^=menuitem]'))
    expect(items.at(-1)).toHaveAccessibleName('Найти дорожку…')
    expect(within(menu).queryByRole('menuitem', { name: 'Скрытая' })).toBeNull()
    await userEvent.click(items.at(-1)!)
    const sheet = await screen.findByRole('dialog', { name: 'Дорожки дополнения' })
    expect(seen).toEqual([series])
    // drawn inside the player, so it shows in full screen too
    expect(screen.getByTestId('player')).toContainElement(sheet)
    await userEvent.click(within(sheet).getByRole('button', { name: 'Закрыть' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Дорожки дополнения' })).toBeNull())
  })

  it('none while a stream plays or for a kid', async () => {
    registerFake()
    const stream: StreamPlayback = { stop: async () => {}, saveProgress: async () => {}, onOtherSource: () => {} }
    const first = await open(adult, { stream })
    expect(within(first).queryByRole('menuitem', { name: 'Найти дорожку…' })).toBeNull()
    document.body.innerHTML = ''
    session.reset()
    const kidMenu = await open(kid)
    expect(within(kidMenu).queryByRole('menuitem', { name: 'Найти дорожку…' })).toBeNull()
  })

  it('a library-only profile gets them (libraryOnly gates online only)', async () => {
    registerFake()
    const menu = await open({ ...adult, libraryOnly: true })
    expect(within(menu).getByRole('menuitem', { name: 'Найти дорожку…' })).toBeInTheDocument()
  })

  it('a row whose sheet throws goes and the player stays', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    function Broken(): never {
      throw new Error('boom')
    }
    registerAddon('broken', { playerTrackActions: [{ label: 'Сломанная', run: () => <Broken /> }] })
    const menu = await open(adult)
    await userEvent.click(within(menu).getByRole('menuitem', { name: 'Сломанная' }))
    expect(screen.getByRole('button', { name: 'Аудио и субтитры' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})
