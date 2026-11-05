import { describe, expect, it } from 'vitest'
import { activeLine, clampOffset, offsetSeconds, parseLrc, shiftLines } from '../lrc'

describe('lrc', () => {
  it('parses [mm:ss.xx] and returns null for plain text', () => {
    const text = ['[ar:Тестовый артист]', '[00:01.50]Первая строка', '[00:04.2]Вторая', '[01:02.345][00:03.00]Повтор', '[00:08]'].join('\n')
    expect(parseLrc(text)).toEqual([
      { timeSec: 1.5, line: 'Первая строка' },
      { timeSec: 3, line: 'Повтор' },
      { timeSec: 4.2, line: 'Вторая' },
      { timeSec: 8, line: '' },
      { timeSec: 62.345, line: 'Повтор' },
    ])
    expect(parseLrc('Просто текст\nбез меток')).toBeNull()
    expect(parseLrc('')).toBeNull()
  })

  it('applies the [offset:] tag', () => {
    expect(parseLrc('[offset:+500]\n[00:02.00]Строка')).toEqual([{ timeSec: 1.5, line: 'Строка' }])
  })

  it('activeLine picks last line not after time', () => {
    const lines = [
      { timeSec: 1, line: 'a' },
      { timeSec: 3, line: 'b' },
      { timeSec: 3, line: 'c' },
      { timeSec: 7, line: 'd' },
    ]
    expect(activeLine(lines, 0.5)).toBe(-1)
    expect(activeLine(lines, 1)).toBe(0)
    expect(activeLine(lines, 2.99)).toBe(0)
    expect(activeLine(lines, 3)).toBe(2)
    expect(activeLine(lines, 6)).toBe(2)
    expect(activeLine(lines, 100)).toBe(3)
    expect(activeLine([], 5)).toBe(-1)
  })

  it('shiftLines adds the track offset on top of the [offset:] tag', () => {
    const lines = parseLrc('[offset:+500]\n[00:02.00]Строка\n[00:00.20]Начало')!
    expect(shiftLines(lines, 1250)).toEqual([
      { timeSec: 1.25, line: 'Начало' },
      { timeSec: 2.75, line: 'Строка' },
    ])
    // earlier, never before 0
    expect(shiftLines(lines, -1000)).toEqual([
      { timeSec: 0, line: 'Начало' },
      { timeSec: 0.5, line: 'Строка' },
    ])
    expect(shiftLines(lines, 0)).toBe(lines)
  })

  it('clampOffset keeps whole ms within ±10 s; offsetSeconds formats it', () => {
    expect(clampOffset(260)).toBe(260)
    expect(clampOffset(-1900.4)).toBe(-1900)
    expect(clampOffset(25_000)).toBe(10_000)
    expect(clampOffset(-25_000)).toBe(-10_000)
    expect(Object.is(clampOffset(-0.2), 0)).toBe(true)
    expect(offsetSeconds(500, 'ru-RU')).toBe('+0,50')
    expect(offsetSeconds(-1250, 'en-US')).toBe('-1.25')
    expect(offsetSeconds(0, 'ru-RU')).toBe('0,00')
  })
})
