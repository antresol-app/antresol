# Antresol design system

One token set, one implementation of each control. A screen is built from
these parts; it does not invent its own button, menu, field or row. Look
at every part in every state on the dev server's gallery:
**`/dev/gallery`** (`npm run dev`, then open the route; it does not exist in a
build).

- Tokens: `src/design/tokens.css` (and the glass material in `src/design/glass.css`).
- Components: `src/ui/`.
- The look is Apple's dark mode: macOS System Settings for lists and
  settings, the Apple TV app for media pages. Judge it at 1x (a 2560×1440
  display without Retina) as well as on a phone.

## Rules

1. **No literal values in module CSS.** Colours, spacing, radii, type,
   control heights, shadows and layers come from tokens. The exceptions are
   art (posters, avatar gradients, the identity tile) and video chrome
   measured against the picture (`src/player`, `src/reader`).
2. **One component per job.** Need a round button? `IconButton` (a bar's square one: `ToolButton`). A pop-up?
   `Select`. A menu? `Menu`. A setting? `ListRow` in a `GroupedSection`. If
   the component cannot do it, extend the component (and the gallery), never
   copy it into a page.
3. **Menus float.** Every menu, select and multi-select renders in a portal
   through `src/ui/floating.ts`. No card clips it or grows with it. It flips
   above or below by the room there, never covers the top bar
   (`data-top-bar`) or the tab bar (`data-bottom-bar`), is clamped into the
   window and cut to scroll with the chosen item in view.
4. **A finger gets 44 pt.** On a touch screen (`pointer: coarse`) every
   control is at least 44×44 to the finger. A control drawn smaller (a
   30/36 pt round button, a 36 pt segment or pop-up) keeps its look and
   grows its hit area with a pseudo-element; a list item or a menu item
   grows to 44. Nothing is reachable only by hover.
5. **The safe area is never content.** Phones are laid out edge to edge
   (`viewport-fit=cover`): `--gutter` already keeps out of the notch and
   the rounded corners of a phone on its side, bars add the insets, menus
   (floating.ts) keep them free. Check at 375, 390, 430 and on its side.
6. **Every string is in the catalogs** (`src/i18n/ru`, `src/i18n/en`). The
   gallery's own headings are the only exception; the gallery is developer
   tooling and is not in a build.

### What enforces them

Two tests count what rules 1 and 2 forbid and compare it with
`src/design/__tests__/ratchet.json`, the list of what is still there:

- `tokensOnly.test.ts` walks every module CSS (the app's and each addon's):
  a literal colour, a written-out font size, a radius, a shadow colour, a
  numeric `font-weight` or `z-index`, a duration, a padding/margin/gap above
  2 px, a control size of the scale, and a second `@keyframes spin`.
  `design/`, `ui/gallery` and the video and reader chrome (`cssRules.ts`) are
  exempt. An excuse says why: a trailing comment on one declaration,
  `color: #000; /* ds-allow: a mask, its alpha only */`, or a comment that
  opens a rule, `/* ds-allow: a poster */`, which excuses every declaration in
  it.
- `rawControls.test.ts` counts, per component file outside `ui/`, the raw
  `<button>`, `<input>`, `<textarea>`, `<select>`, `role="switch"`,
  `role="menu"` and `style={{ … }}` that writes a number.

A count may fall and never rise; a file that has none gets none. A new raw
control or literal is therefore a line added to `ratchet.json`, which is where
review looks. When a count falls the test says so: lower the baseline with
`UPDATE_RATCHET=1 npx vitest run src/design --no-file-parallelism` (it refuses
to write a higher number; `force` is for a file that moved).

## Tokens

### Colour roles

| Role | Tokens | Use |
| --- | --- | --- |
| Backgrounds | `--bg`, `--bg-raised`, `--bg-elevated`, `--bg-elevated-2` | the window; a page's own opaque panel; opaque surfaces over content (a dragged row, glass without transparency); one step more |
| Materials | `--glass-fill`, `--glass-fill-float`, `--glass-blur-float`, `--glass-fill-hover`, `--glass-fill-strong`, `--glass-fill-menu`, `--glass-fill-clear`, `--glass-dim`, `--glass-border`, `--glass-rim`, `--glass-edge`, `--glass-shadow`, `--glass-saturate`, `--glass-saturate-float` | Liquid Glass (`design/glass.css`), only on the navigation and control layer that floats over content, never on content and never glass on glass: `.glass` (the top pill, sidebars, a rail's pagers), `.glass-float` (the phone's tab bar and its search, the player bar: bars over scrolling content, blurred hard enough that titles sliding under the labels are colour, not words, as on iOS 26) with `.glass-liquid` on those three (Liquid Glass's lens, `--liquid-*`, iOS 26 frosted: a translucent frost with a slight dim of what is behind (`--glass-brightness-float`) and a milder colour boost (`--glass-saturate-float`, so one coloured icon under a tab never spreads into a vivid blob), a thin specular edge lit from the top left that fades along the sides — never a grey outline: no hairline border, no outer edge line —, a soft inner light and a soft shadow (`--liquid-shadow`) in every engine — the whole look in iOS Safari; in Chromium `ui/liquidGlass.ts` also refracts the backdrop in a band along the rim, pulled inward and barely blurred, from an SVG displacement map made for the element's size and radius — WebKit and Gecko accept `backdrop-filter: url()` and draw nothing, and `@supports` says yes, so the engine is told by name; `.liquid-lens` gives the selected tab's lens the same rim), `.glass-strong` (menus, sheets, toasts), `.glass-clear` (over video and artwork: the player's chrome, buttons on a title's art), `.glass-bar` (an edge-to-edge bar: the reader's), `.scroll-edge` (a bar's scroll edge effect). A tint, a blur with a saturation boost, a lit rim, a 0.5 px edge and a soft shadow; a component that lifts higher sets `--glass-lift`, never its own box-shadow. Reduce transparency makes them opaque (and bends nothing); Increase contrast raises the edge (the floating bars: a denser frost, still glass, an even defined edge, nothing bent). Judge glass only on screenshots from the `visual` Playwright project (new-headless Chrome): the default headless shell and Playwright's WebKit drop `backdrop-filter` on a fixed bar once the page scrolls and show crisp text through it. No other stylesheet writes `backdrop-filter` (`design/__tests__/glassOnly.test.ts`). |
| Surfaces | `--surface-grouped`, `--surface-grouped-hover`, `--surface-grouped-border` | content: a `Card`, a `GroupedSection`, a page's panel of rows — solid, as iOS 26 Settings |
| Over artwork | `--artwork-outline`, `--scrim-art`, `--scrim-art-strong`, `--text-shadow-art`, `--label-over-art`, `--progress-track-art`, `--watched-dim`, `--watched-dim-lift` | a poster's inset edge; the dark disc or band behind a badge or text on a picture; its shadow and white; a progress track on art; the brightness of a watched title's picture at rest and under the pointer or focus (`A watched title`, Patterns) |
| Fills | `--fill-1` … `--fill-4`, `--fill-hover` | controls on a surface: 1 pressed/hovered, 2 at rest (fields, switch track), 3 tracks and placeholders, 4 faintest (inset lists); a menu item under the pointer |
| Labels | `--label`, `--label-2`, `--label-3`, `--label-4`, `--label-inverse`, `--label-on-tint` | primary text; secondary (hints, meta); tertiary (placeholders, de-emphasised numbers); quaternary; text on white; text on a tint |
| Separators | `--separator`, `--hairline` | lines between rows; outlines (badges, rings' tracks) |
| Tints | `--accent`, `--destructive`, `--success`, `--warning`, `--destructive-text` | meaning, never decoration: links, plain buttons, progress; delete and errors; done and on; problems that need attention; error text in small type |
| Tinted grounds | `--accent-fill`, `--destructive-fill`, `--success-fill`, `--warning-fill` | a notice or a status badge |
| Art | `--identity-*`, `--grad-1..8`, `--file-*`, `--folder-*` | the identity tile; avatar gradients; the file browser's Finder-like icons (a white paper document with a kind colour, a two-tone folder: deeper hues than the palette, for white paper) |
| Palette | `--blue`, `--green`, `--indigo`, `--orange`, `--pink`, `--purple`, `--red`, `--teal`, `--cyan`, `--yellow`, `--gray`… | data (the disk meter's categories) and art (avatar gradients, the coloured icon tiles of the Manage sidebar and the import services) only |

### Type

`--t-large-title`, `--t-title-1..3` for page and section titles;
`--t-headline` for a row's or card's name; `--t-body`, `--t-callout`
(a settings row's label), `--t-subhead`; `--t-group` for a section's name
over its group; `--t-footnote` for hints and meta, `--t-footnote-em` for a
status word; `--t-caption`, `--t-caption-2` for tiny labels;
`--t-control`, `--t-control-sm` for button labels and `--t-field` for
pop-up buttons, segments and number fields. A control centres its
label on the capitals: the element holding the text takes
`text-box: var(--control-text-box)`, never a `translateY` nudge — every
button, pop-up, segment, field, the toast's and the sheet's buttons, the top
bar's sections and the library menu. Numbers that change
(sizes, speeds, percents) add `font-variant-numeric: tabular-nums`.

### Space, shape, size

- Spacing: a 4pt grid with the half steps HIG layouts use —
  `--s-0` 2, `--s-1` 4, `--s-1h` 6, `--s-2` 8, `--s-2h` 10, `--s-3` 12,
  `--s-3h` 14, `--s-4` 16, `--s-5` 20, `--s-6` 24, `--s-8` 32 … `--s-16` 64.
- Page widths: `--gutter` (the window's side margin, 16–64), `--column-max`
  1280 (a page that is one centred column — Поиск — stops growing there),
  `--column-narrow` 680 (a field and the list under it, centred in that
  column).
- Radii: `--r-xs` 4 (bars), `--r-sm` 6 (badges, small posters, meters),
  `--r-md` 8 (menu items, posters in rows), `--r-field` 10 (fields, inset
  lists), `--r-control` 12, `--r-group` 14 (cards, groups, menus),
  `--r-sheet` 20, `--r-pill`.
- Control heights: `--control-h-xs` 30 (a round button inside a row),
  `--control-h-sm` 36 (pop-up buttons, number fields, small buttons,
  segments), `--control-h-tool` 40 (a bar's `ToolButton`), `--control-h-md` 44 (buttons, toolbars, round icon buttons),
  `--control-h-lg` 52 (the item page's actions), `--control-h-field` 48
  (a form's text field). Glyphs: `--icon-xs` 16, `--icon-sm` 18,
  `--icon-md` 20, `--icon-lg` 24.
- Focus: `--focus-ring`, `--focus-ring-width`, `--focus-ring-offset`; inside
  a control's own shape `box-shadow: var(--focus-ring-inset)`. Keyboard
  focus is always visible; pointer focus never draws a ring.
- Layout chrome (phones): the tab bar is `--tabbar-item-h` 54 tall inside
  a `--tabbar-pad` capsule (`--tabbar-height` 62) floating `--tabbar-bottom`
  over the screen's edge (`--tabbar-margin` 26, clear of the home
  indicator); `--bottom-bar-space` is the band it covers
  (only the home indicator on wide screens). Above it: the player bar
  (`--playerbar-h` 60 at `--playerbar-bottom`) and toasts (`--toast-bottom`,
  above the player bar while music plays). A page starts at `--page-top`
  and leaves `--page-end` under its content. `--safe-top/right/bottom/left`
  are the insets for script; `--keyboard-inset` (set by `ui/keyboard.ts`)
  is how much the on-screen keyboard covers.
- Tiles: `--tile-h` 76, `--icon-tile` 22 (ActionTiles on a phone).
- Sheets: `--sheet-alert-w` 300, `--sheet-wide-w` 440, `--sheet-large-w`
  880, `--r-sheet-phone` 28, `--scrim`, `--shadow-sheet`, the alert's type
  `--t-alert-title/-message/-button`.
- Layers: `--z-behind` −1 (behind its parent's content, a scrim), `--z-raised` 1, `--z-above` 2 (inside a component), `--z-sticky` 5 (a list's header), `--z-playerbar` 45, `--z-nav` 50, `--z-overlay` 95 (a page's full-window overlay: Quick Look), `--z-sheet` 96, `--z-toast` 97, `--z-menu` 110
  (a menu opened inside a sheet stays above it), `--z-top` 200.
- Elevation: `--shadow-card`, `--shadow-lift` (a dragged thing),
  `--shadow-pop` (menus, sheets), `--shadow-knob`.
- Motion: `--ease-out`, `--spring`, `--d-fast`, `--d-base`, `--d-slow`
  (all zero with reduced motion). The busy ring turns by the one global
  `@keyframes spin` and its `.spin` class (`design/motion.css`; Reduce motion
  slows it to 2.4 s, never stops it). A new module `@keyframes spin` fails
  `tokensOnly.test.ts`; the copies that are still there are counted in
  `ratchet.json` until their pages draw `ui/Spinner`.
- Weights: `--w-medium` 500 and `--w-semibold` 600 for a weight set after a
  type token.
- Over a cover: `--shadow-cover` (a book's or comic's detail page) and
  `--shadow-cover-sm` (a book's card in a row); `--scrim-hero-bottom`,
  `--scrim-hero-side` and `--scrim-hero-phone` are the hero's scrims.
- Slider: `--slider-fill`, `--slider-track`, `--slider-thumb`,
  `--slider-thumb-size`, `--slider-track-h` (`-active` under the pointer),
  `--slider-thumb-shadow`.

## Components (`src/ui/`)

| Component | Use it for | Notes |
| --- | --- | --- |
| `Button` | any action with a text label | `primary` (white: the main action of a screen, one per screen), `secondary` (a grey fill: supporting actions), `plain` (accent text: an action inside a list — «Добавить язык»), `destructive` (the secondary look with a red label), `link` (inside a sentence — «Скрыть»). Sizes `small` 36 · `regular` 44 · `large` 52; optional leading `icon`; `loading`; never wider than what holds it (a long label is cut with an ellipsis); `material` of a secondary button: `fill` (default, in content), `glass` (on a bar), `clear` (over artwork — the hero's «Подробнее»). Also `ButtonLink`, `ButtonAnchor`. |
| `IconButton` | every round, icon-only button | `size` xs 30 · sm 36 · md 44 · lg 52 — one size means one diameter, glyph size and colour; `material` `fill` (default) · `glass` · `clear` (over artwork or video); a row over a title's art (`ActionTiles`) gives its buttons `clear` (`MaterialContext`). `variant="plain"` inside a row (no fill until hovered); `tone="destructive"` (red glyph); `toggled` (white, aria-pressed); `label` is the accessible name and tooltip (`tooltip` when the state changes the words); `tile` shows the label under the glyph in the phone tile row. Also `IconButtonLink`, `IconButtonAnchor`. |
| `ToolButton` | a bar's own tools: the full player's mode and tool rows, the readers' top bars («‹ Книги», search, contents, «Аа»), a queue row's tools | the rounded square, `--control-h-tool` 40 (`size="sm"` 36 inside a row) with `--r-field`; a bare glyph (`icon`: an Icon's name, or a glyph of the page's own) with an optional word after it (`children`: «Книги»), a wash under the pointer, `toggled` white with a dark glyph (aria-pressed). The bar paints it by setting `--tool-color`, `--tool-hover-fill`, `--tool-hover-color`, `--tool-on-fill`, `--tool-on-color` on itself (the full player's white over the cover; the reader's colours of each theme); the tokens are the look on a plain page; `--tool-h` sets the side where a bar wants bigger tools on a touch screen. Round buttons are `IconButton`s. |
| `Artwork` | every picture of the library in a page (a cover, a track's or a playlist's art, a search row's thumb) | `width` it is drawn at (the srcset runs to twice it), `sizes` for a fluid one, `eager` for the page's main picture, `fallback` (a glyph on its tile) when there is none or it fails. Lazy and decoded off the main thread. |
| `Card` | a block of a page on a surface (a chart, a figure, an empty state) | paddings `sm`/`md`/`lg`. A list of rows is a `GroupedSection`, not a Card. (`components/Card` is the library's poster tile, a different thing.) |
| `GroupedSection`, `ListRow` | settings and every list of things with actions (downloads, profiles, statistics) | the System Settings list: a title over a solid group, a footnote under it; a row has optional `leading` media, `label`, `hint`, `status`, and the control/actions on the trailing edge. `stack`: the control goes under the text on phones (segmented controls); `wide`: always under (ordered lists, forms); `shrink`: the control gives way and cuts its choice. On a phone a plain row's control goes under the text by itself when the text would be narrower than `--row-text-min`, so a word is never split; a `ListLinkRow`'s value is cut by an ellipsis before the chevron or the label's longest word gives way. Pages that build rows by hand compose the same classes (`Manage.module.css`). `ListSelectRow`: a row that chooses (a source, a device, a playlist to add to) — the link row's button with the choice's check at the trailing edge; `selected` marks the chosen one (`aria-pressed`), without it the row only acts; `leading`, `hint`, `disabled`. |
| `TextField`, `TextArea` | a labelled text input in a form or sheet; every search field; a note or a list of lines | the field height; `hint`, `error`, `revealable` passwords; `size="small"` for a toolbar or the top of a menu; `type="search"` is Apple's search field (magnifier, the focus ring when focused) with `onClear` (× and Escape); `type="time"` a time of day in even digits. `TextArea` is the same field for several lines (`rows`, default 4; the corner stretches it down). |
| `NumberField` | a number with a unit in a row («2 МБ/с», «10 ГБ») | the small height, the number and its unit on one line (one baseline), tabular digits; as wide as the number it shows, `chars` the narrowest (in digits); a press anywhere on it types into it; keeps what is typed, reports valid values (`min`, `max`, `decimals`), restores the saved value when left invalid. |
| `Select`, `MultiSelect` | choosing one / several of a fixed list | a filled pop-up button with ⌃⌄; the menu is a `Menu`. Put `placement="bottom-end"` on a trigger at a row's trailing edge. |
| `Menu`, `PointMenu`, `MenuItem`, `MenuHeading`, `MenuSeparator` | actions behind a button («⋯»), sort and filter; `PointMenu` is the same menu at a point (a right click, Files) | floating (rule 3); arrow keys, Home/End, Escape returns focus; radio and checkbox items, a `detail` line, `destructive`, `icon`, a `badge`, a trailing `shortcut` («⌘I»). `maxHeight` for a deliberately tall menu. A Sheet opened from an item gives focus back to the trigger (`menuTrigger`). |
| `floating.ts` | the positioning primitive | `placeFloating` (pure, tested), `positionFloating`, `portalHost` (the full-screen element in the player), `revealChosen`. Use it for any new floating thing. |
| `StatusRing`, `ProgressRing` | circular progress | `StatusRing`: a ring with its percent (a check when done, «!» on an error). `ProgressRing`: the ring alone over a round button (`DownloadButton`, pending cards). |
| `ProgressBar` | linear progress (a download, the watch progress) | tones `accent`, `paused`, `warning`, `label`; with `label` it is a progressbar for assistive tech. |
| `Badge`, `Badges` | a small fact or status capsule | tones `default`, `strong`, `outline` (over artwork), `warning`, `accent`, `success`, `destructive`; `round` for a status. Other modules `composes: badge … from '../ui/Badge.module.css'`. |
| `SegmentedControl` | 2–5 mutually exclusive options shown at once | the toolbar height (`size="small"`: the small height, in a toolbar); a segment may be an `icon` (its label is the name and tooltip); arrow keys move; scrolls sideways on phones. `row`: a page's filter under its title (Поиск, up to 7 segments) — centred in its column; when it does not fit, the whole pill scrolls out to the window's edges (`--segmented-bleed`, the page's gutter) and the edge with more segments past it fades. `align="start"`: the row starts at its column's edge instead of centring (a filter over a list that starts there — «ТВ»). |
| `Toggle` | on/off that applies at once | the iOS switch, green when on; the row's label names it. `size="small"` (40×24) for a column of switches in a table (the source's «Показ»); a finger still gets 44. |
| `Checkbox` | a choice that changes what an action will do (a confirmation's «Удалить навсегда, минуя корзину»), never a setting that applies at once (that is a `Toggle`) | the native box in `--accent`, the label beside it, the whole row the target (a finger’s 44 pt on touch); `tone="destructive"` turns the label red while checked. Unchecked by default when it makes an action irreversible. `description` is a footnote line under the label; `indeterminate` is a group's box over its rows (the dash; a press makes it checked, the caller decides); with no `label` a box in a table's row is named by `aria-label`. |
| `Chips` | one choice of many, each with a count (a playlist's groups), or several on at once (a channel's genres) | capsules of the small control height on `--fill-2`, the chosen ones white with the inverse label (as SegmentedControl's thumb), the count tabular in `--label-2`. One choice (default): radio buttons, arrow keys and Home/End move, a wide window wraps them and a phone keeps one line that scrolls sideways out to the window's edges (`--chips-bleed`, the page's gutter). `multiple`: toggles (`aria-pressed`) that always wrap, `onChange(value, on)`, `children` after them (a «Ещё…» pop-up). `FilterChips` and `ToggleChips` are the same component under the names older callers use. Two to five fixed words are a `SegmentedControl`, not chips. (`components/Chips`, the accent-filled pill an addon radio group draws, is the SDK's older `Chip`/`Chips`, a different thing from `ui.Chips`.) |
| `Spinner` | a wait: a button whose request is out, a confirmed action, a panel that loads | a ring in the colour of the text around it, a quarter open, turning by the global `.spin`; `size` xs 16 · sm 18 (default) · md 20 · lg 24 (the `--icon-*` sizes) or, without one, `--spinner-size` / `--spinner-width` set on the class the caller passes (a button's `1em`); `track` adds a faint full ring for a ring over artwork or video; `label` names it for assistive tech (without one it is decoration and the control says what is happening with `aria-busy`). `Button`, `IconButton` and `Sheet` draw it when busy. A page that still draws its own ring has its own `@keyframes spin`; `tokensOnly.test.ts` counts those copies and fails on a new one. |
| `EmptyState` | an empty page or panel (nothing here yet, an empty trash) and a failed load | the glyph (`icon`, tertiary), the `title` as a heading (`as` h2; h1 when it is the page's only one), a `hint` line (42 characters, balanced), the `actions` under it, centred above the middle of the window (`margin-top: 14vh`); `inset` keeps the page's gutters; `role="alert"` for a failure. An empty list inside a group is a `ListRow` with a hint, not this. |
| `Skeleton` | a placeholder where content is on its way | a quiet static block (`--fill-4`; `tone="soft"` is `--fill-3`, a title's bar), no shimmer; the caller's class gives it size and shape; hidden from assistive tech, the list around it carries `aria-busy`. |
| `Slider` | one value out of a range: a volume, a speed | the native range input in the video bars' volume look: a 4 pt track filled in white up to the thumb (6 pt under the pointer), a white thumb that shows under the pointer and the focus (`thumb="always"`: always), the accent ring round the thumb for the keyboard, a 44 pt strip; `value`, `min`, `max`, `step`, `onChange(number)`, `valueText`. Not a stream's seek bar (that is the player's scrubber). |
| `RankedList` | an ordered list of names the viewer edits, with «+ Добавить…» (languages, studios) | built on `SortableList` + `Menu`; `field` adds a text field for any name. In the SDK as `ui.RankedList` (an addon need not copy it). |
| `Notice` | a state that waits for the viewer, or a problem worth reading (a refresh that lost too many channels) | a tinted ground (`warning` default, `accent`, `destructive`), the glyph in the tint, a `title` and the message in the label colours, the `actions` (small secondary `Button`s) under the words. `as="li"` makes it the first row of a `GroupedSection`. A passing confirmation is a `Toast`, not a notice. |
| `VirtualList` | a list of thousands of rows of one height (a source's channel table) | renders only the rows near the screen plus `overscan`, keeps its full height so the scroll bar tells the truth, and follows whichever scroller moves (the window or an ancestor). `itemHeight` per layout (a phone's two-line row passes its own); `role`/`itemRole` `list`/`listitem` or `rowgroup`/`row` under a header row the caller draws. Rows are positioned inside it, so a scroller around it keeps them. |
| `Sheet` | confirmations, forms, read-only panels | `alert` (default), `wide`, `large`. Wide screens: centred. Phones: a bottom sheet that slides up and slides away when closed, inset 8 pt from the edges with 28 pt corners; the page behind it is locked (`scrollLock.ts`); the keyboard lifts it (`--keyboard-inset`, `--vv-top`); the message and the form scroll between the pinned title and buttons (`[data-sheet-body]`), so «Сохранить» stays on screen; its buttons are capsules at the thumb, three or more stacked with Cancel at the bottom. A read-only picker (`dismissOnOutside`) has the grabber: drag or flick it down to close. A dialog without Cancel gets the round ✕. On open, focus goes to a field the caller marks (`data-autofocus`, `autoFocus`), else the first field — but not on a touch screen, where a form opens with the sheet itself focused so the on-screen keyboard and AutoFill bar wait for a tap. |
| `TabBar` | the phone's navigation (TopNav renders it below 700 px) | iOS 26: a floating `.glass-float` capsule (`--r-tabbar` = half its height) of at most five equal tabs (HIG) — Главная · Фильмы · Сериалы · ТВ · Медиа — and a circular search `accessory` (`--tabbar-height` 58, `--r-tabbar`) `--tabbar-gap` 10 beside it, `--tabbar-inset` 16 from the sides (below 360 pt: 8, a gap of 8 and labels a hair tighter, so «Фильмы» and «Сериалы» stay whole in an equal share) and `--tabbar-bottom` 26 over the screen's edge (or the home indicator's inset less 8, where that is more). The tabs share the capsule inside `--tabbar-pad` 4; the current one (aria-current, from the item's `roots`) sits under the lens (50 tall, `--r-tabbar-lens` = the bar's radius less the padding, concentric, `--tabbar-lens-fill` with `--tabbar-lens-rim`) that slides to the next tab on `--ease-tabbar-spring` over `--d-tabbar-spring`, stretching along its way and squashing across it like a drop (`--liquid-stretch` a tab, at most `--liquid-stretch-max`), with the Liquid Glass rim (`.liquid-lens`); its glyph and label are in `--tabbar-selected` (the app mark's violet) and the glyph grows 1.06; the others are white (`--label`), never grey. Glyphs (`--tabbar-glyph` 24, iOS 26's tab bar symbols) are original drawings in SF Symbols' language at the tab bar's Medium weight (`homeFill`, `filmFrame` — an outlined film frame, perforated strips, nothing in the picture —, `playTv` — an outlined screen with a play mark —, `antennaRadiowaves`, `squareStackFill`; the search's `magnifyingglass`): SF's widths, heights, ~1.8 pt strokes, ~3 pt corners and cut-outs, measured against the system's symbols — never Apple's paths, whose licence keeps them off the web; like SF they are balanced by their own widths, not rescaled to one size; labels `--t-tab`, one line, their box trimmed to the capitals so glyph and word centre on what is drawn. A press squeezes to 0.88 and springs back; a finger's 44 pt is the hit area (a share narrower than that grows into its neighbours'). Scrolling down folds the bar: only the capsule's width moves (to a 58 circle; the labels fade out, the current tab's glyph fades in, and a tap or the keyboard opens it again), every part stays 58 high at `--r-tabbar`; the `PlayerBar`, if something plays, moves into the room between the circle and the search in that same shape (artwork `--tabbar-art` 40 at the concentric `--r-tabbar-art`), with only play/pause; scrolling up opens it. Reduce motion: no spring and no squeeze, only the crossfade (`--d-tabbar-fade`). «Медиа» holds Музыка and Книги (with audiobooks and comics): it leads to the one open last (`state/mediaSection.ts`), /music and /books keep their URLs and make it current, and their pages carry a `MediaSwitch` (a segmented control) on phones. It is the layout's bottom bar (`data-bottom-bar`, `data-minimized` while folded). Checked at 320/360/390/430 in both languages, folded, with the player and with Reduce motion (e2e/tabbar.spec.ts); the glass at 320/390/430 on a 3x phone, the droplet, Reduce motion and transparency (e2e/screenshots-liquid-glass.spec.ts). |
| `PlayerBar`, `PlayerBarButton` | what plays, on every page | iOS 26's Now Playing accessory: a `--playerbar-h` 48 capsule, artwork `--playerbar-art` 32, title `--t-playerbar-title` and subtitle (open the full player), up to three 44 pt targets with `--playerbar-glyph` 18 glyphs (play/pause 22), a progress hairline inside the bottom edge clear of the round ends; at `--playerbar-bottom`. `MiniPlayer` feeds it. |
| `Toast` | every short confirmation at the bottom | one glass capsule at `--toast-bottom`, an optional `action` («Отменить»), `tone="error"` (an alert in the warning colour); its live region is always mounted (`live={false}` when another region reads the words). No page draws its own capsule. |
| `ActionTiles` | a title page's secondary actions | round `IconButton`s (`tile`) in the action row on a wide screen; equal icon-over-label tiles on a phone. Four fit a 375 pt phone: a destructive action goes into «Ещё» there. |
| `SortableList`, `SortableRowText` | every ordered list the viewer edits (the channel's «Состав канала»; `RankedList` — languages, studios — is built on it) | an inset group of rows inside a `ListRow wide`: the caller's content (`SortableRowText`: a glyph on a `--fill-3` tile, the name, a detail line, both cut), then `moveButtons` «Выше» / «Ниже» (`IconButton xs plain`), `removable` (−, red under the pointer; `min` rows stay) and the handle ≡ at the trailing edge, as iOS's edit mode. The handle drags with a mouse, a pen or a finger (`touch-action: none`): the row lifts (`--bg-elevated`, `--shadow-lift`) and follows, the others slide by one row, the scroller under it (a sheet's body) scrolls near its edge, the drop commits, Escape or a lost pointer puts it back; the DOM keeps its order until the drop. The keyboard: ↑/↓/Home/End on the handle, or the buttons; the new place is read out (`role="status"`) and the focus stays on the control (the handle when that one turns off at an edge). Below 700 px «Выше», «Ниже» and «Убрать» go into the row's «⋯». |
| `SwipeRow` | a row with actions on a phone (downloads) | swipe left for the actions (the destructive one last, at the edge); half the way or a quick flick keeps it open; a tap, a touch elsewhere or scrolling the list closes it. A shortcut only — the same actions are always in the row's «⋯» (a mouse never swipes). `className` on the `<li>`, `contentClassName` on the sliding row. |
| `keyboard.ts` | anything fixed to the bottom with a field in it | `useKeyboardInset()` keeps `--keyboard-inset` and `--vv-top` on `<html>` while mounted (Sheet uses it). |
| `scrollLock.ts` | anything modal | `useScrollLock()` keeps the page from scrolling while mounted (ref-counted; Sheet uses it). |

## Patterns

- **A watched title** (`components/WatchedMark`; the library's tile in all
  its kinds — a film, a series or season every episode of which is watched,
  an episode, a book or comic volume read, a comic series every volume of
  which is read, an audiobook heard — in `Card`, `EpisodeList`'s tiles and
  `ComicSeries`'s volumes): one look on every platform, so a finished title
  reads as finished at a glance. Its picture is **dimmed to half**
  (`--watched-dim`, a brightness; `--watched-dim-lift` while the pointer or
  focus is on the card, so a chosen card still reads), with **the eye on a
  dark disc in the middle of the picture** at full strength, and **no
  progress bar**, not even a full one: the bar is for a title in progress
  only (0 < progress < 1). The disc is a quarter of the tile's width, 28 to
  52 pt, so it reads on a big card and does not smother a small one
  (`WatchedMark` takes the width from its picture box, a size container:
  `container-type: inline-size`); its name — «Просмотрено», «Прочитано»,
  «Прослушано» — is part of the link's name. The picture takes the class
  `dimClass`, the host raises `--art-dim` to the lift on hover and focus. A
  card never decides this from `progress`: the flag is the server's
  `Card.watched` (every library kind; `progress` is 1 then). What else
  claims the middle, and who wins: a download (a title on its way) draws its
  ring (in the middle of a card of its own, in the corner of a library card
  getting a new season) and that card shows neither the eye nor the dimming;
  the play
  glyph of an episode tile (hover, focus) takes the middle and the eye steps
  aside until the pointer leaves; a tile with a play button that is always
  there (the phone's episode row) shows no eye — it is dimmed and its toggle
  says watched. The corner is the cloud's («Ещё не на сервере»). The apps
  match it: iOS (`CardView`, `Tokens.Opacity.watched` 0.5), tvOS
  (`watchedDimming`, a 0.5 black layer, 0.15 while focused), Android phone
  and TV (`WatchedStyle`, alpha 0.5, 0.85 while focused) — each in its one
  shared card, the disc a quarter of the tile's width (28 to 52 dp or pt; on
  the TV 52 to 88).
- **Other states of a library card** (the same on any tile): *in progress* —
  a 4 pt bar along the bottom of the picture, white on `--progress-track-art`;
  *not on the server* («Похожие», a missing part of a collection) — the cloud
  on a dark disc in the corner (web only: none of the apps draws it); *on its way into the library* — the
  picture dimmed and desaturated under the download ring (`PendingOverlay`;
  sources build, web only); *new* (an aired episode the profile has not
  watched, the server's `new`) — the accent «новое» badge beside the state's
  badge on the episode in the guide, on the web and on the episode tiles of
  iOS, Apple TV and Android (phone and TV).
- **A settings screen**: `GroupedSection` per topic, a `ListRow` per
  setting, the control on the trailing edge — `Toggle`, `Select`,
  `SegmentedControl` (`stack`), `NumberField`, a small secondary `Button`.
- **A list of items with actions** (Downloads): rows of one height
  (the poster sets it) and one trailing column — the row's single action as
  a small `IconButton` (an empty slot of the same width when there is none)
  and «⋯» (`Menu`) with the rest, destructive last. Recovery actions that
  belong to a message («Добавить в медиатеку») are `link` buttons in it.
- **The item page's actions**: the primary `Button` (or the split watch
  button), then `IconButton size="lg"` for everything else, in one row; on
  phones they become equal tiles (`tile`).
- **A page that is one centred column** (Поиск): `--column-max` plus the
  gutters, centred; the title, the field (`--column-narrow`) and the filter
  (`SegmentedControl row`) centred in it; results, rails (`Shelf
  contained`) and grids as wide as the column, their titles on its edge.
  Below 900 px the field takes the whole column.
- **A top result** (Поиск): a `Card` (`padding="lg"`) beside a list of five
  rows and as tall as it — the artwork (round for an artist, square for
  music, the poster's own shape otherwise), the name (the card's link), a
  kind line and one small primary `Button`: «Слушать», «Смотреть» or
  «Открыть». On a narrow window the art moves to the left of the name.
- **A profile's picture** (`components/Avatar`): the photo (`photo`, the
  profile's address — `/api/profiles/{id}/photo?v=…`, public, so the sign-in
  screen draws it too) or the initial on a gradient (`avatar`, grad-1…8),
  always a circle; the gradient is also under the photo while it loads and the
  letter stands in if it does not. Every place that shows a profile passes
  both (the top bar, the picker, the list, the family marks, devices,
  statistics). It is set in the profile's form (`AvatarEditor`): «Загрузить
  фото…» opens the frame (`PhotoCropSheet`: the picture under a round window,
  dragged, pinched or wheeled, the arrow keys and +/− too), the framed square
  goes up as a JPEG with the form's Save; «Убрать фото» brings the letter back.
- **A sheet** (`ui/Sheet`): confirmations and forms; `size="large"`
  for a list (the disk breakdown).
- **The phone layout** (below 700 px): the compact header (brand, downloads,
  profile) and the `TabBar`; pages start at `--page-top` and end at
  `--page-end`; the `PlayerBar` and toasts float above the tab bar. A row
  keeps one trailing control — «⋯» with every action — and a `SwipeRow`
  for the frequent ones; a header's tools share one row and its links
  scroll sideways edge to edge; two equal capsules at most per row («Слушать», «Перемешать»),
  everything else in a «⋯» — a destructive action always there. A page's
  main action («Смотреть», «Продолжить») is whole on the first screen, above
  the tab bar. Sheets come from the bottom; menus float by their trigger.
- **Anything absolute inside a scroller** (a rail's cells) takes a
  positioned ancestor inside that scroller as its block (`Rail`'s list and
  `Badge` are positioned for it). With one outside, it lies past the
  scroller's clip and widens the page, and a phone's Chrome grows the
  layout viewport to fit (up to 4x the screen). The fixed bars (`TabBar`,
  the header) are `max-width: 100vw`, so they stay the screen wide
  whatever widens the page (e2e/phone-width.spec.ts).
- **The video bar's tools row** (`VideoPlayer`, `LivePlayer`; the chrome is
  measured against the picture): the sound control (`player/VolumeControl`,
  the speaker and a horizontal slider) leads the row at the left, as Safari's
  full-screen controls (WebKit) have it, as do other big web players;
  the menus and the other tools stay at the right edge. The slider is always
  there. Nothing on the row opens, grows or pops up under the pointer or the
  focus, so touching one control never moves another (a slider that grew in the
  right-anchored group used to push every tool left of it). A phone's buttons
  set its volume: touch hides the control; a window under 560 pt keeps only
  the speaker.
- **Live TV** (`pages/tv`): a channel is its **number** (tabular, tertiary
  label) and its **name**; its picture is the programme on air
  (`ChannelArt`: backdrop 16:9, else the poster whole on `--fill-3`, else
  `--fill-3` with the `tv` glyph) — never a coloured tile with the name on
  it. An uploaded logo (`logoUrl`) is shown small beside the name
  (`ChannelLogo`: the picture fitted whole in a neutral `--fill-3` box with
  a hairline, `--r-sm`, one height per place — xs 24 the list row and the
  player's channel strip, sm 30 the guide's column and the details pane,
  md 36 the player's header, lg 44 the editor); without one the number and
  the name stand alone (no generated tile); in a list where some channel
  has a logo the logo is a column of its own (the list and the player's
  strip: after the still; /tv/manage: after the number; the guide: before
  the name, the column widened by it) and a row without one keeps the
  empty slot, so names and programme lines start at one place (phones:
  the list's logo at 20). The editor's «Состав канала»
  is a `SortableList` of picks (`SortableRowText`: kind glyph, name, kind ·
  year · path) in a `ListRow wide`, with «Добавить в состав» under it —
  a `SegmentedControl` (Поиск · Папки), a search field and result rows
  (`ListRow` with the glyph tile and a trailing ⊕ that turns into a green
  ✓ once added), or a folder level (the path row with ‹ back and ⊕ for the
  whole folder, folder rows with ⊕ and ›, file rows with ⊕). «Логотип» is
  a `ListRow`: the preview (or an empty dashed well) leading, «Убрать» and
  «Загрузить…» / «Заменить…» trailing, the format hint or the refusal
  (`--destructive-text`) under the label. The list is one numbered list on a grouped surface (`ChannelRow`: the
  number, the still, name / now / a `ProgressBar` with the minutes left /
  «Далее»; the star at the trailing edge on hover and focus, always on touch
  and when starred; the last channel's number in the accent), filtered by a
  `SegmentedControl row align="start"` (Все · Избранное · темы); a wide
  window adds the details pane. «Телегид» is a grid in its own scroller:
  the channel column and the time ruler stick, blocks are `--fill-3` (on
  air `--fill-1`, past `--fill-4` with tertiary text), the now line and its
  time pill in the accent. The live player is the film player's chrome
  (`VideoPlayer.module.css`) with the programme's info in the bottom bar.
- **Deleting a channel** (`/tv/manage`): «Удалить» is in every row's «⋯» and
  always asks first — the `Sheet`'s message says what goes: a hand-made
  channel, its schedule and its number; one with fetching, also what was
  fetched for it (after its keeping period); an automatic one is only
  remembered as deleted, so the generator leaves it alone. A deleted
  automatic channel moves to «Удалённые каналы», a `GroupedSection` under the
  list that exists only while there is one: a `ListRow` per channel (its
  name and group) with a small secondary «Вернуть» at the end. A channel of
  an external source is deleted from its source's editor («Удалить из
  источника» in the row's «⋯» and in the channel's sheet, after the same
  kind of confirmation): the playlist has it, so it is hidden for good (a
  refresh does not undo it), listed under «Скрыты» and shown again by its
  switch.
- **External sources** (`pages/tv/sources`, admin): «Внешние источники»
  is a settings column — the sources as link rows (glyph tile, name with
  a «N новых» badge, the masked address, counts · when refreshed, the
  last error in `--destructive-text`, a pending drop as a `Notice` row
  under the source with «Оставить старый список» (secondary) and
  «Подтвердить» (destructive)), «Наборы», «Устройства» (a set `Select` at
  each row's end; an IPTV link's «⋯» copies its addresses or deletes it after a confirmation), «Настройки». A set's sheet lists its sources, then per source its categories with switches and a search to leave single channels out (the left-out ones listed while the search is empty). A source opens
  its own page: the header (name, masked address, counts, «Обновить»,
  «⋯»: settings, guides, delete), its notices, then the editor table.
  Settings, a channel and a guide binding are sheets (`size="large"` /
  `"wide"`); a stored secret is never shown — its field is empty with the
  mask as the placeholder and is sent only when typed. An external
  channel the browser cannot play says «Этот канал открывается в
  приложении Antresol или в IPTV-плеере» and why, in the player's error
  card (compact inside the picture on an upright phone).
- **An editor table** («Внешние источники» → a source's channels): the
  page's wide column (`--column-max`); a search field (`size="small"`)
  and a `SegmentedControl size="small"` on one line, `FilterChips` under
  them, a `GroupedSection` with the chosen group's bulk actions, then one
  surface (`--surface-grouped`, `--r-group`) holding a header row of
  column names (`--t-footnote-em`, `--label-2`) that sticks under the top
  bar — and, once stuck, hides the rows passing behind the bar — and a
  `VirtualList` of rows at `--control-h-lg`. A cell's default value
  recedes (`--label-3`), the exceptions read in `--label-2`; a hidden row's
  name turns `--label-2`. The name edits in place (the text, then a small
  `TextField`); the rest is in the row's «⋯» and the channel's sheet. A
  phone folds the columns into the row's second line and keeps the switch;
  a tap on the row opens the sheet. Without any logo in the list the logo
  column folds away.
- **A phone on its side** (`SHORT_QUERY`, height ≤ 500): wide-screen layout,
  but the first screen must still hold the title and the main action —
  smaller titles, two lines of overview, tighter gaps; sign-in scrolls from
  the top. Everything stays inside the side insets.
