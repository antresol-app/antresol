// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2026 Antresol Contributors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// The look of a book in foliate (theme, font, size, margins, columns): shared
// by the web reader (EpubReader) and the native app's reader bundle
// (reader/native), so a book pages the same way in both.
import type { Paginator } from '../vendor/foliate/view.js'
import { FONTS, LINE_SPACINGS, MARGINS, THEMES, type ReaderPrefs } from './prefs'

/** The stylesheet foliate puts into every section of the book. */
/** `narrow`: a phone-width column, set ragged to avoid wide word gaps. */
export function bookCss(p: ReaderPrefs, narrow = false): string {
  const t = THEMES[p.theme]
  const override = p.theme !== 'original'
  const bold = p.bold || p.theme === 'bold'
  // «Фокус» sets the book in a serif unless a font was picked
  const font = p.theme === 'focus' && p.font === 'system' ? FONTS.iowan : FONTS[p.font]
  const justify = p.justify ?? !narrow
  return `
    @namespace epub "http://www.idpf.org/2007/ops";
    html {
      color-scheme: ${t.dark ? 'dark' : 'light'};
      background: ${t.bg} !important;
      color: ${t.fg} !important;
      font-size: ${Math.round(p.fontScale * 100)}% !important;
      -webkit-text-size-adjust: none;
      text-size-adjust: none;
    }
    body {
      background: transparent !important;
      color: inherit !important;
      font-family: ${font.css} !important;
    }
    ${override ? 'body * { color: inherit !important; background-color: transparent !important; }' : ''}
    p, li, blockquote, dd, div, span, h1, h2, h3, h4, h5, h6, td, th {
      font-family: inherit !important;
    }
    p, li, blockquote, dd {
      line-height: ${LINE_SPACINGS[p.lineSpacing ?? 'normal']};
      text-align: ${justify ? 'justify' : 'start'};
      -webkit-hyphens: auto;
      hyphens: auto;
      hanging-punctuation: allow-end last;
      widows: 2;
      orphans: 2;
    }
    ${bold ? 'p, li, blockquote, dd, div, span, td, th, a { font-weight: 600 !important; } b, strong, h1, h2, h3, h4, h5, h6 { font-weight: 800 !important; }' : ''}
    [align="left"] { text-align: left; }
    [align="right"] { text-align: right; }
    [align="center"] { text-align: center; }
    a:link, a:visited { color: ${t.link} !important; }
    pre { white-space: pre-wrap !important; }
    img, svg { max-width: 100%; height: auto; }
    ::selection { background: ${t.dark ? 'rgba(10, 132, 255, 0.45)' : 'rgba(0, 122, 255, 0.25)'}; }
  `
}

/** Puts the prefs on foliate's paginator. `compact`: a phone-width window. */
export function applyReaderStyle(r: Paginator, p: ReaderPrefs, o: { compact: boolean; reduceMotion: boolean }) {
  r.setAttribute('gap', MARGINS[p.margin].gap)
  r.setAttribute('margin', o.compact ? '56px' : '64px')
  r.setAttribute('max-inline-size', '680px')
  r.setAttribute('max-column-count', p.columns === 'one' ? '1' : '2')
  // pages slide like paper, unless motion is reduced
  r.toggleAttribute('animated', !o.reduceMotion)
  r.setStyles(bookCss(p, o.compact))
}
