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
2. **One component per job.** Need a round button? `IconButton`. A pop-up?
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

## Tokens

### Colour roles

| Role | Tokens | Use |
| --- | --- | --- |
| Backgrounds | `--bg`, `--bg-raised`, `--bg-elevated`, `--bg-elevated-2` | the window; a page's own opaque panel; opaque surfaces over content (a dragged row, glass without transparency); one step more |
| Materials | `--glass-fill`, `--glass-fill-hover`, `--glass-fill-strong`, `--glass-fill-menu`, `--glass-fill-clear`, `--glass-dim`, `--glass-border`, `--glass-rim`, `--glass-edge`, `--glass-shadow`, `--glass-saturate` | Liquid Glass (`design/glass.css`), only on the navigation and control layer that floats over content, never on content and never glass on glass: `.glass` (the top pill, the tab bar, the player bar, sidebars, a rail's pagers), `.glass-strong` (menus, sheets, toasts), `.glass-clear` (over video and artwork: the player's chrome, buttons on a title's art), `.glass-bar` (an edge-to-edge bar: the reader's), `.scroll-edge` (a bar's scroll edge effect). A tint, a blur with a saturation boost, a lit rim, a 0.5 px edge and a soft shadow; a component that lifts higher sets `--glass-lift`, never its own box-shadow. Reduce transparency makes them opaque; Increase contrast raises the edge. No other stylesheet writes `backdrop-filter` (`design/__tests__/glassOnly.test.ts`). |
| Surfaces | `--surface-grouped`, `--surface-grouped-hover`, `--surface-grouped-border` | content: a `Card`, a `GroupedSection`, a page's panel of rows — solid, as iOS 26 Settings |
| Over artwork | `--artwork-outline`, `--scrim-art`, `--scrim-art-strong`, `--text-shadow-art`, `--label-over-art`, `--progress-track-art` | a poster's inset edge; the dark disc or band behind a badge or text on a picture; its shadow and white; a progress track on art |
| Fills | `--fill-1` … `--fill-4`, `--fill-hover` | controls on a surface: 1 pressed/hovered, 2 at rest (fields, switch track), 3 tracks and placeholders, 4 faintest (inset lists); a menu item under the pointer |
| Labels | `--label`, `--label-2`, `--label-3`, `--label-4`, `--label-inverse`, `--label-on-tint` | primary text; secondary (hints, meta); tertiary (placeholders, de-emphasised numbers); quaternary; text on white; text on a tint |
| Separators | `--separator`, `--hairline` | lines between rows; outlines (badges, rings' tracks) |
| Tints | `--accent`, `--destructive`, `--success`, `--warning`, `--destructive-text` | meaning, never decoration: links, plain buttons, progress; delete and errors; done, on, seeding; problems that need attention; error text in small type |
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
  segments), `--control-h-md` 44 (buttons, toolbars, round icon buttons),
  `--control-h-lg` 52 (the item page's actions), `--control-h-field` 48
  (a form's text field). Glyphs: `--icon-xs` 16, `--icon-sm` 18,
  `--icon-md` 20, `--icon-lg` 24.
- Focus: `--focus-ring`, `--focus-ring-width`, `--focus-ring-offset`; inside
  a control's own shape `box-shadow: var(--focus-ring-inset)`. Keyboard
  focus is always visible; pointer focus never draws a ring.
- Layout chrome (phones): the tab bar is `--tabbar-item-h` 54 tall inside
  a `--tabbar-pad` capsule (`--tabbar-height` 62) floating `--tabbar-margin`
  above the home indicator; `--bottom-bar-space` is the band it covers
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
- Layers: `--z-raised` 1, `--z-above` 2 (inside a component), `--z-sticky` 5 (a list's header), `--z-playerbar` 45, `--z-nav` 50, `--z-overlay` 95 (a page's full-window overlay: Quick Look), `--z-sheet` 96, `--z-toast` 97, `--z-menu` 110
  (a menu opened inside a sheet stays above it), `--z-top` 200.
- Elevation: `--shadow-card`, `--shadow-lift` (a dragged thing),
  `--shadow-pop` (menus, sheets), `--shadow-knob`.
- Motion: `--ease-out`, `--spring`, `--d-fast`, `--d-base`, `--d-slow`
  (all zero with reduced motion).

## Components (`src/ui/`)

| Component | Use it for | Notes |
| --- | --- | --- |
| `Button` | any action with a text label | `primary` (white: the main action of a screen, one per screen), `secondary` (a grey fill: supporting actions), `plain` (accent text: an action inside a list — «Добавить язык»), `destructive` (the secondary look with a red label), `link` (inside a sentence — «Скрыть»). Sizes `small` 36 · `regular` 44 · `large` 52; optional leading `icon`; `loading`; `material` of a secondary button: `fill` (default, in content), `glass` (on a bar), `clear` (over artwork — the hero's «Подробнее»). Also `ButtonLink`, `ButtonAnchor`. |
| `IconButton` | every round, icon-only button | `size` xs 30 · sm 36 · md 44 · lg 52 — one size means one diameter, glyph size and colour; `material` `fill` (default) · `glass` · `clear` (over artwork or video); a row over a title's art (`ActionTiles`) gives its buttons `clear` (`MaterialContext`). `variant="plain"` inside a row (no fill until hovered); `tone="destructive"` (red glyph); `toggled` (white, aria-pressed); `label` is the accessible name and tooltip (`tooltip` when the state changes the words); `tile` shows the label under the glyph in the phone tile row. Also `IconButtonLink`, `IconButtonAnchor`. |
| `Artwork` | every picture of the library in a page (a cover, a track's or a playlist's art, a search row's thumb) | `width` it is drawn at (the srcset runs to twice it), `sizes` for a fluid one, `eager` for the page's main picture, `fallback` (a glyph on its tile) when there is none or it fails. Lazy and decoded off the main thread. |
| `Card` | a block of a page on a surface (a chart, a figure, an empty state) | paddings `sm`/`md`/`lg`. A list of rows is a `GroupedSection`, not a Card. (`components/Card` is the library's poster tile, a different thing.) |
| `GroupedSection`, `ListRow` | settings and every list of things with actions (downloads, profiles, statistics) | the System Settings list: a title over a solid group, a footnote under it; a row has optional `leading` media, `label`, `hint`, `status`, and the control/actions on the trailing edge. `stack`: the control goes under the text on phones (segmented controls); `wide`: always under (ordered lists, forms). Pages that build rows by hand compose the same classes (`Manage.module.css`). |
| `TextField` | a labelled text input in a form or sheet; every search field | the field height; `hint`, `error`, `revealable` passwords; `size="small"` for a toolbar or the top of a menu; `type="search"` is Apple's search field (magnifier, the focus ring when focused) with `onClear` (× and Escape). |
| `NumberField` | a number with a unit in a row («2 МБ/с», «10 ГБ») | the small height, the number and its unit on one line (one baseline), tabular digits; as wide as the number it shows, `chars` the narrowest (in digits); a press anywhere on it types into it; keeps what is typed, reports valid values (`min`, `max`, `decimals`), restores the saved value when left invalid. |
| `Select`, `MultiSelect` | choosing one / several of a fixed list | a filled pop-up button with ⌃⌄; the menu is a `Menu`. Put `placement="bottom-end"` on a trigger at a row's trailing edge. |
| `Menu`, `PointMenu`, `MenuItem`, `MenuHeading`, `MenuSeparator` | actions behind a button («⋯»), sort and filter; `PointMenu` is the same menu at a point (a right click, Files) | floating (rule 3); arrow keys, Home/End, Escape returns focus; radio and checkbox items, a `detail` line, `destructive`, `icon`, a `badge`, a trailing `shortcut` («⌘I»). `maxHeight` for a deliberately tall menu. A Sheet opened from an item gives focus back to the trigger (`menuTrigger`). |
| `floating.ts` | the positioning primitive | `placeFloating` (pure, tested), `positionFloating`, `portalHost` (the full-screen element in the player), `revealChosen`. Use it for any new floating thing. |
| `StatusRing`, `ProgressRing` | circular progress | `StatusRing`: a ring with its percent (a check when done, «!» on an error). `ProgressRing`: the ring alone over a round button (`DownloadButton`, pending cards). |
| `ProgressBar` | linear progress (a download, the watch progress) | tones `accent`, `paused`, `warning`, `label`; with `label` it is a progressbar for assistive tech. |
| `Badge`, `Badges` | a small fact or status capsule | tones `default`, `strong`, `outline` (over artwork), `warning`, `accent`, `success`, `destructive`; `round` for a status. Other modules `composes: badge … from '../ui/Badge.module.css'`. |
| `SegmentedControl` | 2–5 mutually exclusive options shown at once | the toolbar height (`size="small"`: the small height, in a toolbar); a segment may be an `icon` (its label is the name and tooltip); arrow keys move; scrolls sideways on phones. `row`: a page's filter under its title (Поиск, up to 7 segments) — centred in its column; when it does not fit, the whole pill scrolls out to the window's edges (`--segmented-bleed`, the page's gutter) and the edge with more segments past it fades. `align="start"`: the row starts at its column's edge instead of centring (a filter over a list that starts there — «ТВ»). |
| `Toggle` | on/off that applies at once | the iOS switch, green when on; the row's label names it. `size="small"` (40×24) for a column of switches in a table (the source's «Показ»); a finger still gets 44. |
| `FilterChips` | one choice of many, each with a count (a playlist's groups) | capsules of the small control height on `--fill-2`, the chosen one white with the inverse label (as SegmentedControl's thumb), the count tabular in `--label-2`; arrow keys, Home/End move. A wide window wraps them; a phone keeps one line that scrolls sideways out to the window's edges (`--chips-bleed`, the page's gutter). Two to five fixed words are a `SegmentedControl`, not chips. |
| `Notice` | a state that waits for the viewer, or a problem worth reading (a refresh that lost too many channels) | a tinted ground (`warning` default, `accent`, `destructive`), the glyph in the tint, a `title` and the message in the label colours, the `actions` (small secondary `Button`s) under the words. `as="li"` makes it the first row of a `GroupedSection`. A passing confirmation is a `Toast`, not a notice. |
| `VirtualList` | a list of thousands of rows of one height (a source's channel table) | renders only the rows near the screen plus `overscan`, keeps its full height so the scroll bar tells the truth, and follows whichever scroller moves (the window or an ancestor). `itemHeight` per layout (a phone's two-line row passes its own); `role`/`itemRole` `list`/`listitem` or `rowgroup`/`row` under a header row the caller draws. Rows are positioned inside it, so a scroller around it keeps them. |
| `Sheet` | confirmations, forms, read-only panels | `alert` (default), `wide`, `large`. Wide screens: centred. Phones: a bottom sheet that slides up and slides away when closed, inset 8 pt from the edges with 28 pt corners; the page behind it is locked (`scrollLock.ts`); the keyboard lifts it (`--keyboard-inset`, `--vv-top`); the message and the form scroll between the pinned title and buttons (`[data-sheet-body]`), so «Сохранить» stays on screen; its buttons are capsules at the thumb, three or more stacked with Cancel at the bottom. A read-only picker (`dismissOnOutside`) has the grabber: drag or flick it down to close. A dialog without Cancel gets the round ✕. |
| `TabBar` | the phone's navigation (TopNav renders it below 700 px) | a glass capsule of tabs (glyph over label) and a round `accessory` (search) beside it; the router marks the current tab. It is the layout's bottom bar (`data-bottom-bar`). Six tabs (Главная, Фильмы, Сериалы, ТВ, Музыка, Книги) fit a 375 pt phone: past five the tabs keep equal widths but never get narrower than their label (the others share the rest, 44 pt at least), labels lose their side padding and tracking, and the bar's insets tighten below 400 pt. Checked at 375/390/430 in both languages — no label is cut. |
| `PlayerBar`, `PlayerBarButton` | what plays, on every page | the Music app's mini player: artwork, title, subtitle (open the full player), three 44 pt controls, a progress hairline; at `--playerbar-bottom`. `MiniPlayer` feeds it. |
| `Toast` | every short confirmation at the bottom | one glass capsule at `--toast-bottom`, an optional `action` («Отменить»), `tone="error"` (an alert in the warning colour); its live region is always mounted (`live={false}` when another region reads the words). No page draws its own capsule. |
| `ActionTiles` | a title page's secondary actions | round `IconButton`s (`tile`) in the action row on a wide screen; equal icon-over-label tiles on a phone. Four fit a 375 pt phone: a destructive action goes into «Ещё» there. |
| `SortableList`, `SortableRowText` | every ordered list the viewer edits (the channel's «Состав канала»; `RankedList` — languages, studios — is built on it) | an inset group of rows inside a `ListRow wide`: the caller's content (`SortableRowText`: a glyph on a `--fill-3` tile, the name, a detail line, both cut), then `moveButtons` «Выше» / «Ниже» (`IconButton xs plain`), `removable` (−, red under the pointer; `min` rows stay) and the handle ≡ at the trailing edge, as iOS's edit mode. The handle drags with a mouse, a pen or a finger (`touch-action: none`): the row lifts (`--bg-elevated`, `--shadow-lift`) and follows, the others slide by one row, the scroller under it (a sheet's body) scrolls near its edge, the drop commits, Escape or a lost pointer puts it back; the DOM keeps its order until the drop. The keyboard: ↑/↓/Home/End on the handle, or the buttons; the new place is read out (`role="status"`) and the focus stays on the control (the handle when that one turns off at an edge). Below 700 px «Выше», «Ниже» and «Убрать» go into the row's «⋯». |
| `SwipeRow` | a row with actions on a phone (downloads) | swipe left for the actions (the destructive one last, at the edge); half the way or a quick flick keeps it open; a tap, a touch elsewhere or scrolling the list closes it. A shortcut only — the same actions are always in the row's «⋯» (a mouse never swipes). `className` on the `<li>`, `contentClassName` on the sliding row. |
| `keyboard.ts` | anything fixed to the bottom with a field in it | `useKeyboardInset()` keeps `--keyboard-inset` and `--vv-top` on `<html>` while mounted (Sheet uses it). |
| `scrollLock.ts` | anything modal | `useScrollLock()` keeps the page from scrolling while mounted (ref-counted; Sheet uses it). |

## Patterns

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
