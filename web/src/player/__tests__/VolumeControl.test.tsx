// The speaker and the volume slider at the left of the video bars: the
// names, the muted state, the pointer leaving the slider, and the one
// property they exist for — nothing on the bar moves when the volume does
// (no hover, focus or press rule resizes or shifts anything).
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { VolumeControl } from '../VolumeControl'

const bar = (over: Partial<Parameters<typeof VolumeControl>[0]> = {}) => {
  const onVolume = vi.fn()
  const onToggleMute = vi.fn()
  render(<VolumeControl volume={0.5} muted={false} onVolume={onVolume} onToggleMute={onToggleMute} muteLabel="Выключить звук" unmuteLabel="Включить звук" {...over} />)
  return { onVolume, onToggleMute, slider: screen.getByRole('slider', { name: 'Громкость' }) as HTMLInputElement }
}

describe('VolumeControl', () => {
  it('a slider of 0–1 at the level, with its percent for assistive tech', () => {
    const { slider } = bar({ volume: 0.35 })
    expect(slider).toHaveValue('0.35')
    expect(slider).toHaveAttribute('aria-valuetext', '35%')
    expect(screen.getByRole('button', { name: 'Выключить звук' })).toBeInTheDocument()
  })

  it('moving the slider reports the value; the speaker reports a toggle', () => {
    const { slider, onVolume, onToggleMute } = bar()
    fireEvent.change(slider, { target: { value: '0.8' } })
    expect(onVolume).toHaveBeenCalledWith(0.8)
    fireEvent.click(screen.getByRole('button', { name: 'Выключить звук' }))
    expect(onToggleMute).toHaveBeenCalledOnce()
  })

  it('muted: the slider shows 0 and the speaker offers «Включить звук»', () => {
    const { slider } = bar({ muted: true })
    expect(slider).toHaveValue('0')
    expect(slider).toHaveAttribute('aria-valuetext', '0%')
    expect(screen.getByRole('button', { name: 'Включить звук' })).toHaveAttribute('title', 'Включить звук (M)')
  })

  it('at volume 0 it reads as muted too, and the title can be the caller\'s', () => {
    bar({ volume: 0, title: 'Звук (M)' })
    expect(screen.getByRole('button', { name: 'Включить звук' })).toHaveAttribute('title', 'Звук (M)')
  })

  it('a pointer press leaves the slider, so the arrows go on to the player\'s own steps', () => {
    const { slider } = bar()
    slider.focus()
    expect(slider).toHaveFocus()
    fireEvent.pointerUp(slider, { pointerType: 'mouse' })
    expect(slider).not.toHaveFocus()
  })
})

// The look is the kit's Slider (ui/Slider.module.css: every state of the track and the thumb);
// VolumeControl.module.css only says where it sits.
describe('the volume slider\'s CSS', () => {
  const here = path.dirname(fileURLToPath(import.meta.url))
  const parse = (rel: string) => {
    const css = readFileSync(path.resolve(here, rel), 'utf8')
    return [...css.replace(/\/\*[\s\S]*?\*\//g, '').matchAll(/([^{}]+)\{([^{}]*)\}/g)].map((m) => ({ selector: m[1]!.trim(), body: m[2]! }))
  }
  const kit = parse('../../ui/Slider.module.css')
  const own = parse('../VolumeControl.module.css')

  it('has hover / focus / press rules', () => {
    expect(kit.some((r) => /:(hover|focus|active)/.test(r.selector))).toBe(true)
  })

  it('no hover, focus or press state changes the size or the place of the control (only the track and thumb are drawn differently)', () => {
    // a pseudo-element (the track, the thumb) is drawn inside the input's own box; nothing else may react
    const offenders = [...kit, ...own]
      .filter((r) => /:(hover|focus|active)/.test(r.selector) && !/::/.test(r.selector))
      .filter((r) => /(^|[;\s])(width|height|min-|max-|margin|padding|display|flex|gap|position|inset|left|right|top|bottom|transform)/.test(r.body))
    expect(offenders).toEqual([])
  })

  it('the slider has one width and one height for every state', () => {
    // a width of its own in the bar, and the kit's strip of a button's height
    expect(own.some((r) => r.selector === '.volume .range' && /width:\s*\d+px/.test(r.body))).toBe(true)
    expect(kit.some((r) => r.selector === '.slider' && /height:\s*var\(--control-h-md\)/.test(r.body))).toBe(true)
  })
})
