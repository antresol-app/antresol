// The video player's tool row (speaker, audio and subtitles, auto-skip, AirPlay, picture in
// picture, full screen) is the kit's clear-glass discs, as the live player's: no tool of its
// own in VideoPlayer.module.css.
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SkipMenu } from '../SkipControls'
import { TrackMenu } from '../TrackMenu'
import { VolumeControl } from '../VolumeControl'

const here = path.dirname(fileURLToPath(import.meta.url))
const read = (rel: string) => readFileSync(path.resolve(here, rel), 'utf8')

describe('the video bars’ tools', () => {
  it('the speaker, the track menu and the skip menu are 36 pt clear-glass IconButtons', () => {
    render(
      <>
        <VolumeControl volume={1} muted={false} onVolume={() => {}} onToggleMute={() => {}} muteLabel="Выключить звук" unmuteLabel="Включить звук" />
        <TrackMenu audio={[]} audioIndex={0} subtitles={[]} subIndex={-1} onAudio={() => {}} onSubtitle={() => {}} />
        <SkipMenu kinds={{ recap: true, intro: true, credits: true }} value={{ recap: false, intro: false, credits: false }} onChange={() => {}} />
      </>,
    )
    for (const name of ['Выключить звук', 'Аудио и субтитры', 'Пропускать сам']) {
      const b = screen.getByRole('button', { name })
      expect(b, name).toHaveClass('glass-clear', 'sm')
      expect(b.className, name).not.toMatch(/plain/)
    }
  })

  it('VideoPlayer.module.css has no tool of its own to outrank the kit (only AirPlay’s accent)', () => {
    const css = read('../VideoPlayer.module.css')
    expect(css).not.toMatch(/\.tool\b/)
    expect(css).toMatch(/button\.airplay\[data-active\]\s*\{\s*color: var\(--accent\)/)
    for (const f of ['../TrackMenu.module.css', '../SkipControls.module.css', '../VolumeControl.module.css']) expect(read(f), f).not.toMatch(/composes:\s*tool/)
  })
})
