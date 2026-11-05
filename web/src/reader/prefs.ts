// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2026 Antresol Contributors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Reader settings of this device (size, theme, font, margins, columns). A convenience
// only: kept in localStorage, and any storage failure falls back to defaults.
// The web panel offers original, paper and night; the native app adds the
// Apple Books cards (quiet, bold, calm, focus) and renders a dark «Оригинал» as night.
import type { Key } from '../i18n'

export type ReaderTheme = 'original' | 'paper' | 'night' | 'quiet' | 'bold' | 'calm' | 'focus'
export type ReaderFont = 'system' | 'georgia' | 'iowan'
export type ReaderColumns = 'auto' | 'one'
export type ReaderMargin = 'narrow' | 'normal' | 'wide'
export type ReaderLineSpacing = 'tight' | 'normal' | 'loose'

export interface ReaderPrefs {
  /** 0.8..2.0, step 0.1 */
  fontScale: number
  theme: ReaderTheme
  font: ReaderFont
  columns: ReaderColumns
  /** Side margins of the text column. */
  margin: ReaderMargin
  /** The native app's «Межстрочный интервал»; missing is normal. */
  lineSpacing?: ReaderLineSpacing
  /** «Жирный текст» (the bold theme is always bold). */
  bold?: boolean
  /** «Выравнивание по ширине»; missing: justified except in a phone-width column. */
  justify?: boolean
}

export const DEFAULT_PREFS: ReaderPrefs = { fontScale: 1, theme: 'original', font: 'system', columns: 'auto', margin: 'normal' }
export const MIN_SCALE = 0.8
export const MAX_SCALE = 2
export const SCALE_STEP = 0.1

const KEY = 'vault.reader.v1'

export interface ThemeColors {
  /** Catalog key of the theme's name. */
  label: Key
  bg: string
  fg: string
  link: string
  dark: boolean
}

export const THEMES: Record<ReaderTheme, ThemeColors> = {
  original: { label: 'reader.theme.original', bg: '#ffffff', fg: '#1c1c1e', link: '#0a63c9', dark: false },
  paper: { label: 'reader.theme.paper', bg: '#f8f1e3', fg: '#3b2f22', link: '#8a4f0e', dark: false },
  night: { label: 'reader.theme.night', bg: '#000000', fg: '#d1d1d6', link: '#64a8ff', dark: true },
  quiet: { label: 'reader.theme.quiet', bg: '#4a4a4d', fg: '#d4d4d6', link: '#9fc6ff', dark: true },
  bold: { label: 'reader.theme.bold', bg: '#ffffff', fg: '#000000', link: '#0a63c9', dark: false },
  calm: { label: 'reader.theme.calm', bg: '#ede0c8', fg: '#4a3a26', link: '#8a4f0e', dark: false },
  focus: { label: 'reader.theme.focus', bg: '#fffbf0', fg: '#28241e', link: '#8a4f0e', dark: false },
}

/** Line heights of «Межстрочный интервал». */
export const LINE_SPACINGS: Record<ReaderLineSpacing, number> = { tight: 1.3, normal: 1.5, loose: 1.75 }

export const FONTS: Record<ReaderFont, { label: Key; css: string }> = {
  system: { label: 'reader.font.system', css: '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif' },
  georgia: { label: 'reader.font.georgia', css: 'Georgia, "Times New Roman", serif' },
  iowan: { label: 'reader.font.iowan', css: '"Iowan Old Style", "Palatino Linotype", Palatino, Georgia, serif' },
}

export const MARGINS: Record<ReaderMargin, { label: Key; gap: string }> = {
  narrow: { label: 'reader.margin.narrow', gap: '4%' },
  normal: { label: 'reader.margin.normal', gap: '7%' },
  wide: { label: 'reader.margin.wide', gap: '12%' },
}

/** Rounds to the 10 % grid and keeps it in 80–200 %. */
export function clampScale(v: number): number {
  if (!Number.isFinite(v)) return DEFAULT_PREFS.fontScale
  return Math.min(MAX_SCALE, Math.max(MIN_SCALE, Math.round(v * 10) / 10))
}

export function loadPrefs(): ReaderPrefs {
  let raw: string | null = null
  try {
    raw = localStorage.getItem(KEY)
  } catch {
    return { ...DEFAULT_PREFS }
  }
  if (!raw) return { ...DEFAULT_PREFS }
  try {
    return normalizePrefs(JSON.parse(raw))
  } catch {
    return { ...DEFAULT_PREFS }
  }
}

/** Prefs from untrusted JSON (storage, the native app): unknown values fall back to the defaults. */
export function normalizePrefs(raw: unknown): ReaderPrefs {
  if (!raw || typeof raw !== 'object') return { ...DEFAULT_PREFS }
  const v = raw as Partial<Record<keyof ReaderPrefs, unknown>>
  return {
    fontScale: typeof v.fontScale === 'number' ? clampScale(v.fontScale) : DEFAULT_PREFS.fontScale,
    theme: typeof v.theme === 'string' && v.theme in THEMES ? (v.theme as ReaderTheme) : DEFAULT_PREFS.theme,
    font: typeof v.font === 'string' && v.font in FONTS ? (v.font as ReaderFont) : DEFAULT_PREFS.font,
    columns: v.columns === 'one' || v.columns === 'auto' ? v.columns : DEFAULT_PREFS.columns,
    margin: typeof v.margin === 'string' && v.margin in MARGINS ? (v.margin as ReaderMargin) : DEFAULT_PREFS.margin,
    ...(typeof v.lineSpacing === 'string' && v.lineSpacing in LINE_SPACINGS ? { lineSpacing: v.lineSpacing as ReaderLineSpacing } : {}),
    ...(typeof v.bold === 'boolean' ? { bold: v.bold } : {}),
    ...(typeof v.justify === 'boolean' ? { justify: v.justify } : {}),
  }
}

export function savePrefs(p: ReaderPrefs) {
  try {
    localStorage.setItem(KEY, JSON.stringify(p))
  } catch {
    /* private mode or blocked storage: the settings just aren't remembered */
  }
}
