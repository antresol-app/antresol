// /dev/gallery (dev server only): every token and every component of the
// design system in every state, on the app's real background — the page to
// check a change against before it ships. Developer tooling: its own
// headings are plain English, never translated; the components' strings
// come from the catalogs as everywhere else.
import { useState, type ReactNode } from 'react'
import { Avatar } from '../../components/Avatar'
import { Icon } from '../../components/Icon'
import { ActionTiles } from '../ActionTiles'
import { Badge } from '../Badge'
import { Button } from '../Button'
import { Card } from '../Card'
import { Checkbox } from '../Checkbox'
import { Chips } from '../Chips'
import { EmptyState } from '../EmptyState'
import { IconButton } from '../IconButton'
import { GroupedSection, ListLinkRow, ListRow, ListSelectRow } from '../List'
import { RankedList } from '../RankedList'
import { Skeleton } from '../Skeleton'
import { Slider } from '../Slider'
import { Spinner } from '../Spinner'
import { SETTINGS_GLYPHS, SETTINGS_TINTS, SettingsIcon } from '../SettingsIcon'
import { ToggleChips } from '../ToggleChips'
import { Menu, MenuHeading, MenuItem, MenuSegments, MenuSeparator, MenuStepper } from '../Menu'
import { NumberField } from '../NumberField'
import { PlayerBar, PlayerBarButton } from '../PlayerBar'
import { ProgressBar } from '../ProgressBar'
import { SegmentedControl } from '../SegmentedControl'
import { Sheet } from '../Sheet'
import { SortableList, SortableRowText } from '../SortableList'
import { SwipeRow } from '../SwipeRow'
import { TabBar } from '../TabBar'
import { MultiSelect, Select } from '../Select'
import { ProgressRing, StatusRing } from '../StatusRing'
import { TextArea, TextField } from '../TextField'
import { Toast } from '../Toast'
import { Toggle } from '../Toggle'
import { ToolButton } from '../ToolButton'
import { FilterChips } from '../FilterChips'
import { Notice } from '../Notice'
import { VirtualList } from '../VirtualList'
import styles from './Gallery.module.css'

const COLORS: [string, string[]][] = [
  ['Backgrounds', ['--bg', '--bg-raised', '--bg-elevated', '--bg-elevated-2']],
  ['Materials', ['--glass-fill', '--glass-fill-hover', '--glass-fill-strong', '--glass-fill-menu', '--glass-fill-clear', '--glass-dim']],
  ['Surfaces', ['--surface-grouped', '--surface-grouped-hover', '--surface-grouped-border', '--scrim-art', '--scrim-art-strong']],
  ['Fills', ['--fill-1', '--fill-2', '--fill-3', '--fill-4', '--fill-hover']],
  ['Labels', ['--label', '--label-2', '--label-3', '--label-4']],
  ['Separators', ['--separator', '--hairline', '--glass-border']],
  ['Tints', ['--accent', '--destructive', '--success', '--warning', '--destructive-text']],
  ['Tinted grounds', ['--accent-fill', '--destructive-fill', '--success-fill', '--warning-fill']],
  ['Palette', ['--blue', '--green', '--indigo', '--orange', '--pink', '--purple', '--red', '--teal', '--cyan', '--yellow', '--gray']],
]
const TYPE = ['--t-large-title', '--t-title-1', '--t-title-2', '--t-title-3', '--t-headline', '--t-body', '--t-callout', '--t-subhead', '--t-group', '--t-footnote', '--t-footnote-em', '--t-caption', '--t-caption-2', '--t-control', '--t-control-sm', '--t-field']
const SPACE = ['--s-0', '--s-1', '--s-1h', '--s-2', '--s-2h', '--s-3', '--s-3h', '--s-4', '--s-5', '--s-6', '--s-8', '--s-10', '--s-12', '--s-16']
const RADII = ['--r-xs', '--r-sm', '--r-md', '--r-field', '--r-control', '--r-group', '--r-sheet', '--r-pill']
const HEIGHTS = ['--control-h-xs', '--control-h-sm', '--control-h-md', '--control-h-lg', '--control-h-field']

const SCOPES = [
  { value: 'all', label: 'Всё' },
  { value: 'movies', label: 'Фильмы' },
  { value: 'series', label: 'Сериалы' },
  { value: 'music', label: 'Музыка' },
  { value: 'books', label: 'Книги' },
  { value: 'audiobooks', label: 'Аудиокниги', short: 'Аудио' },
  { value: 'comics', label: 'Манга' },
]

const LANGS: Record<string, string> = { ru: 'Русский', en: 'Английский', de: 'Немецкий', fr: 'Французский' }

const SORTABLE: { key: string; icon: 'tv' | 'film' | 'folder'; title: string; detail: string }[] = [
  { key: 'title:1', icon: 'tv', title: 'Друзья', detail: 'Сериал · 1994 · 236 серий' },
  { key: 'item:2', icon: 'film', title: 'Матрица', detail: 'Фильм · 1999' },
  { key: 'item:3', icon: 'tv', title: 'Друзья · S1 E7', detail: 'Серия · Отключение электричества' },
  { key: 'folder:a', icon: 'folder', title: 'Season 02', detail: 'Папка · series/Друзья/Season 02' },
  { key: 'item:4', icon: 'film', title: 'Очень длинное название фильма, которое не помещается в одну строку', detail: 'Фильм · 2015' },
]

function Section({ title, note, children }: { title: string; note?: ReactNode; children: ReactNode }) {
  return (
    <section className={styles.section}>
      <h2 className={styles.h2}>{title}</h2>
      {note && <p className={styles.note}>{note}</p>}
      {children}
    </section>
  )
}

function Swatch({ name }: { name: string }) {
  return (
    <div className={styles.swatch}>
      <span className={styles.chip} style={{ background: `var(${name})` }} />
      <code>{name}</code>
    </div>
  )
}

export function Gallery() {
  const [seg, setSeg] = useState('1080')
  const [sorted, setSorted] = useState(SORTABLE)
  const [scope, setScope] = useState('all')
  const [on, setOn] = useState(true)
  const [chip, setChip] = useState('')
  const [size, setSize] = useState('0')
  const [langs, setLangs] = useState(['ru', 'en'])
  const [mbs, setMbs] = useState(2)
  const [gb, setGb] = useState(10)
  const [wl, setWl] = useState(false)
  const [tool, setTool] = useState(false)
  const [sheet, setSheet] = useState<'' | 'alert' | 'wide' | 'large' | 'three' | 'busy'>('')
  const [toast, setToast] = useState('')
  const [following, setFollowing] = useState(false)
  const [volume, setVolume] = useState(0.6)
  const [source, setSource] = useState('hd')
  const [checks, setChecks] = useState({ a: true, b: false, c: true })
  const [genres, setGenres] = useState<string[]>(['drama'])
  const [ranked, setRanked] = useState(['ru', 'en'])
  const SIZES = ['2', '4', '6', '8', '10', '15', '20', '30', '40', '60', '80', '0']
  return (
    <main className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.h1}>Design system</h1>
        <p className={styles.note}>
          Tokens: <code>src/design/tokens.css</code>. Components: <code>src/ui/</code>. When to use what: <code>web/DESIGN.md</code>.
        </p>
      </header>

      <Section title="Colour roles">
        {COLORS.map(([group, names]) => (
          <div key={group} className={styles.swatchGroup}>
            <h3 className={styles.h3}>{group}</h3>
            <div className={styles.swatches}>
              {names.map((n) => (
                <Swatch key={n} name={n} />
              ))}
            </div>
          </div>
        ))}
      </Section>

      <Section title="Type scale">
        <div className={styles.typeList}>
          {TYPE.map((n) => (
            <div key={n} className={styles.typeRow}>
              <code>{n}</code>
              <span style={{ font: `var(${n})` }}>Антресоль — the quick brown fox 0123</span>
            </div>
          ))}
        </div>
      </Section>

      <Section title="Spacing, radii, control heights">
        <div className={styles.scale}>
          {SPACE.map((n) => (
            <div key={n} className={styles.scaleRow}>
              <code>{n}</code>
              <span className={styles.space} style={{ width: `var(${n})` }} />
            </div>
          ))}
        </div>
        <div className={styles.row}>
          {RADII.map((n) => (
            <div key={n} className={styles.radius} style={{ borderRadius: `var(${n})` }}>
              <code>{n}</code>
            </div>
          ))}
        </div>
        <div className={styles.row}>
          {HEIGHTS.map((n) => (
            <div key={n} className={styles.height} style={{ height: `var(${n})` }}>
              <code>{n}</code>
            </div>
          ))}
        </div>
      </Section>

      <Section title="Button" note="primary · secondary · plain · destructive · link; small 36 · regular 44 · large 52.">
        {(['large', 'regular', 'small'] as const).map((s) => (
          <div key={s} className={styles.row}>
            <Button size={s} icon="play">
              Смотреть
            </Button>
            <Button size={s} variant="secondary">
              Подробнее
            </Button>
            <Button size={s} variant="secondary" icon="restart">
              Начать
            </Button>
            <Button size={s} variant="plain" icon="plusCircle">
              Добавить язык
            </Button>
            <Button size={s} variant="destructive" icon="trash">
              Удалить
            </Button>
            <Button size={s} variant="secondary" loading>
              Загрузка
            </Button>
            <Button size={s} variant="secondary" disabled>
              Недоступно
            </Button>
          </div>
        ))}
        <p className={styles.body}>
          A link inside a line of text: the release wasn’t responding.{' '}
          <Button variant="link" onClick={() => {}}>
            Скрыть
          </Button>
        </p>
      </Section>

      <Section title="IconButton" note="Every round button. One size = one diameter, one glass, one glyph size and colour; only the tone (red, or the accent of a row's own «…») and the toggled state (white) differ.">
        {(['lg', 'md', 'sm', 'xs'] as const).map((s) => (
          <div key={s} className={styles.row}>
            <code className={styles.label}>{s}</code>
            <IconButton size={s} icon="plus" label="Добавить сезон…" />
            <IconButton size={s} icon="eye" label="Следить" />
            <IconButton size={s} icon="watchlist" label="Хочу посмотреть" toggled={wl} onClick={() => setWl(!wl)} />
            <IconButton size={s} icon="ellipsis" label="Ещё" />
            <IconButton size={s} icon="trash" label="Удалить" tone="destructive" />
            <IconButton size={s} icon="eye" label="Следить (вкл.)" toggled />
            <IconButton size={s} icon="pause" label="Пауза" loading />
            <IconButton size={s} icon="pause" label="Пауза" disabled />
            <IconButton size={s} icon="minusCircle" label="Убрать" variant="plain" />
            <IconButton size={s} icon="grip" label="Переместить" variant="plain" />
            <IconButton size={s} icon="ellipsis" label="Действия" variant="plain" tone="accent" />
          </div>
        ))}
        <div className={styles.row}>
          <code className={styles.label}>ring</code>
          <span className={styles.ringWrap}>
            <IconButton size="lg" icon="arrowDown" label="Загружается" />
            <ProgressRing diameter={52} mode="active" pct={64} label="Прогресс" />
          </span>
          <span className={styles.ringWrap}>
            <IconButton size="lg" icon="arrowDown" label="Пауза" />
            <ProgressRing diameter={52} mode="paused" pct={30} label="Прогресс" />
          </span>
          <span className={styles.ringWrap}>
            <IconButton size="lg" icon="arrowDown" label="Начинается" />
            <ProgressRing diameter={52} mode="starting" pct={0} label="Прогресс" />
          </span>
        </div>
      </Section>

      <Section
        title="ToolButton"
        note="The rounded-square toolbar button of a bar (the full player's rows, the readers' top bars, a queue row's tools). Bare glyph, wash under the pointer, white while toggled on. A bar paints it with --tool-* custom properties: the first two rows are the plain-page look, the last the full player's over a cover."
      >
        {(['md', 'sm'] as const).map((s) => (
          <div key={s} className={styles.row}>
            <code className={styles.label}>{s}</code>
            <ToolButton size={s} icon="search" label="Поиск" />
            <ToolButton size={s} icon="quote" label="Текст" toggled={tool} onClick={() => setTool(!tool)} />
            <ToolButton size={s} icon="queue" label="Очередь (вкл.)" toggled />
            <ToolButton size={s} icon="list" label="Оглавление" disabled />
            <ToolButton size={s} icon="chevronLeft" label="Книги">
              <span>Книги</span>
            </ToolButton>
          </div>
        ))}
        <div className={`${styles.row} ${styles.toolOverArt}`}>
          <code className={styles.label}>over a cover</code>
          <ToolButton icon="shuffle" label="Перемешать" />
          <ToolButton icon="repeat" label="Повтор" toggled />
          <ToolButton icon="quote" label="Текст" toggled={tool} onClick={() => setTool(!tool)} />
          <ToolButton icon="queue" label="Очередь" />
          <ToolButton icon="list" label="Оглавление" disabled />
        </div>
      </Section>

      <Section title="Liquid Glass" note="design/glass.css: regular (bars, the player bar), strong (menus, sheets, toasts), clear (over video and artwork), bar (edge to edge). Content is never glass. Check with Reduce transparency and Increase contrast.">
        <div className={styles.glassStage}>
          {(['glass', 'glass-strong', 'glass-clear', 'glass-bar'] as const).map((g) => (
            <div key={g} className={`${styles.glassSample} ${g}`}>
              .{g}
            </div>
          ))}
          <Button variant="secondary" material="clear" icon="info">
            Clear
          </Button>
          <IconButton icon="ellipsis" label="Clear" material="clear" size="lg" />
          <IconButton icon="ellipsis" label="Glass" material="glass" size="lg" />
          <IconButton icon="ellipsis" label="Fill" size="lg" />
        </div>
      </Section>

      <Section title="Card">
        <div className={styles.cards}>
          {(['sm', 'md', 'lg'] as const).map((p) => (
            <Card key={p} padding={p}>
              <p className={styles.cardTitle}>padding {p}</p>
              <p className={styles.note}>A surface: one radius, one glass, one hairline.</p>
            </Card>
          ))}
        </div>
      </Section>

      <Section title="ToggleChips" note="Several choices on at once (a channel's genres, countries, decades); the count dimmed; «Ещё…» after them for a long list.">
        <ToggleChips
          label="Страны"
          chips={[
            { value: 'SU', label: 'СССР', count: 214 },
            { value: 'US', label: 'США', count: 980 },
            { value: 'FR', label: 'Франция', count: 61 },
          ]}
          value={['SU']}
          onToggle={() => {}}
        />
      </Section>

      <Section title="SettingsIcon" note="The tile of a settings list: one filled glyph set, white on one palette colour. «Управление», the addons' tiles and every ListLinkRow use it; the app's line icons never go on a tile.">
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12 }}>
          {SETTINGS_GLYPHS.map((g, i) => (
            <span key={g} title={g} style={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'center', gap: 4, fontSize: 11, width: 72 }}>
              <SettingsIcon glyph={g} tint={SETTINGS_TINTS[i % SETTINGS_TINTS.length]!} />
              {g}
            </span>
          ))}
        </div>
      </Section>

      <Section title="ListLinkRow" note="A row that opens a sheet or a page: the whole row is the button, a coloured icon tile, an optional value, the chevron.">
        <GroupedSection title="Просмотр">
          <ListLinkRow icon="speaker" tint="pink" label="Звук и субтитры" onOpen={() => {}} />
          <ListLinkRow icon="key" tint="gray" label="Пароль для приложений" hint="One short line under the label." onOpen={() => {}} />
          <ListLinkRow label="Избранное" value="12" to="/dev/gallery" />
          <ListLinkRow label="Без иконки" value="Включены" onOpen={() => {}} />
        </GroupedSection>
        <GroupedSection title="valueTone" footer="A status as the value: a dot in the tone before the words; only a problem's words are coloured, as iOS does.">
          <ListLinkRow label="gemini-flash-lite" hint="LiteLLM · litellm:4000" value="420 мс" valueTone="success" onOpen={() => {}} />
          <ListLinkRow label="claude-haiku-5-5" hint="Anthropic · api.anthropic.com" value="Нет ключа" valueTone="warning" onOpen={() => {}} />
          <ListLinkRow label="openai/gpt-oss-120b" hint="Groq · api.groq.com" value="Ошибка 401" valueTone="destructive" onOpen={() => {}} />
          <ListLinkRow label="local-gemma" hint="Ollama · localhost:11434" value="Не проверена" valueTone="neutral" onOpen={() => {}} />
        </GroupedSection>
        <GroupedSection dimmed title="disabled" footer="A group under a master switch that is off: dimmed (GroupedSection dimmed) and its rows do nothing (ListLinkRow disabled).">
          <ListLinkRow label="gemini-flash-lite" hint="LiteLLM · litellm:4000" value="420 мс" valueTone="success" disabled onOpen={() => {}} />
          <ListLinkRow label="whisper-large-v3-turbo" hint="Распознавание речи · Groq · api.groq.com" value="Не проверена" valueTone="neutral" disabled onOpen={() => {}} />
          <ListSelectRow tone="accent" label="Добавить модель…" disabled onSelect={() => {}} />
        </GroupedSection>
        <GroupedSection title="ListRow statusTone" footer="The live line under a row's hint in the warning tint: the setting is on but cannot work yet.">
          <ListRow label="Расшифровка аудиокниг" hint="Распознаёт речь аудиокниг." status="Нет модели распознавания речи — добавьте её в «Модели»" statusTone="warning">
            <Toggle label="Расшифровка аудиокниг" checked onChange={() => {}} />
          </ListRow>
        </GroupedSection>
      </Section>

      <Section title="GroupedSection · ListRow" note="The System Settings list. Every control sits on the trailing edge of its row; stack/wide put it under the text.">
        <GroupedSection title="Новые фильмы и сериалы" footer="A footnote under the group.">
          <ListRow label="Качество видео" hint="Лучшее, что Antresol будет искать." stack>
            <SegmentedControl
              label="Качество"
              value={seg}
              onChange={setSeg}
              segments={[
                { value: '720', label: '720p' },
                { value: '1080', label: '1080p' },
                { value: '2160', label: '4K' },
              ]}
            />
          </ListRow>
          <ListRow label="Превью при перемотке" hint="A switch on the trailing edge.">
            <Toggle label="Превью" checked={on} onChange={setOn} />
          </ListRow>
          <ListRow label="Максимальный размер фильма" hint="A pop-up button.">
            <Select
              label="Максимальный размер фильма"
              placement="bottom-end"
              value={size}
              onChange={setSize}
              options={SIZES.map((v) => ({ value: v, label: v === '0' ? 'Без ограничения' : `До ${v} ГБ` }))}
            />
          </ListRow>
          <ListRow label="Языки субтитров" hint="Several at once.">
            <MultiSelect
              label="Языки субтитров"
              placement="bottom-end"
              value={langs}
              onChange={setLangs}
              options={[
                { value: 'ru', label: 'Русский' },
                { value: 'en', label: 'Английский' },
                { value: 'de', label: 'Немецкий' },
              ]}
            />
          </ListRow>
          <ListRow label="Ограничить отдачу" hint="A number with its unit, then a switch.">
            <NumberField label="Скорость отдачи" unit="МБ/с" value={mbs} min={0.01} decimals={2} onChange={setMbs} />
            <Toggle label="Ограничить" checked onChange={() => {}} />
          </ListRow>
          <ListRow label="Кеш просмотра без загрузки">
            <NumberField label="Размер кеша" unit="ГБ" value={gb} min={1} onChange={setGb} />
          </ListRow>
          <ListRow label="Пересканировать медиатеку" hint="A small secondary button.">
            <Button size="small" variant="secondary" icon="restart">
              Начать
            </Button>
          </ListRow>
          <ListRow
            label="A row with leading media"
            hint="Poster, avatar or icon before the text."
            leading={
              <span className={styles.leading}>
                <Icon name="film" size={18} />
              </span>
            }
          >
            <IconButton size="sm" icon="ellipsis" label="Ещё" />
          </ListRow>
        </GroupedSection>
        <GroupedSection dimmed title="dimmed" footer="Its switch is off: the rows' text steps back (label-2, label-3) and the caller disables their controls («Функции ИИ» while the assistant is off).">
          <ListRow label="Описание канала" hint="Turns a phrase into a channel's rules.">
            <Toggle label="Описание канала" checked disabled onChange={() => {}} />
          </ListRow>
          <ListRow label="Главы аудиокниг" hint="Splits a transcript into chapters.">
            <Toggle label="Главы аудиокниг" checked={false} disabled onChange={() => {}} />
          </ListRow>
        </GroupedSection>
      </Section>

      <Section
        title="SortableList"
        note="An ordered list to edit: the caller's content (SortableRowText: a glyph tile, the name, a detail line), then «Выше» / «Ниже», remove and the drag handle. Drag the handle with a mouse or a finger (the list scrolls at the edge, Escape puts it back), or ↑/↓/Home/End on it, or the buttons. Below 700 px the buttons go into the row's «⋯». RankedList is built on it."
      >
        <GroupedSection title="Состав канала">
          <ListRow label="Inset in a wide row" wide>
            <SortableList
              label="Состав канала"
              items={sorted}
              getKey={(x) => x.key}
              itemLabel={(x) => x.title}
              renderItem={(x) => <SortableRowText leading={<Icon name={x.icon} size={16} />} title={x.title} detail={x.detail} />}
              onChange={setSorted}
              removable
              moveButtons
              empty="Пусто — добавьте что-нибудь"
            />
          </ListRow>
        </GroupedSection>
        <GroupedSection title="flush" footer="The rows of the group itself, as iOS's edit mode turns a settings list into edit rows: the red − leads, ≡ ends the row. No inset list, no wrapper; a row that cannot go (canRemove) has no −. The group's other rows stay as they are.">
          <SortableList
            flush
            label="Модели"
            items={sorted}
            getKey={(x) => x.key}
            itemLabel={(x) => x.title}
            renderItem={(x) => <SortableRowText title={x.title} detail={x.detail} />}
            onChange={setSorted}
            removable
            canRemove={(x) => x.key !== sorted[0]?.key}
          />
          <ListSelectRow tone="accent" label="Добавить…" onSelect={() => {}} />
        </GroupedSection>
      </Section>

      <Section title="TextField · NumberField">
        <div className={styles.fields}>
          <TextField label="Сервер SMTP" placeholder="smtp.gmail.com" />
          <TextField label="Пароль" type="password" revealable defaultValue="secret" />
          <TextField label="Адрес" error="Нужен адрес почты" defaultValue="me@" />
          <TextField label="Поиск" hideLabel size="small" placeholder="Название студии" />
        </div>
        <div className={styles.row}>
          <NumberField label="МБ/с" unit="МБ/с" value={2} decimals={2} onChange={() => {}} />
          <NumberField label="ГБ" unit="ГБ" value={10} onChange={() => {}} />
          <NumberField label="Порт" value={42069} chars={5} onChange={() => {}} />
          <NumberField label="ГБ" unit="ГБ" value={10} disabled onChange={() => {}} />
        </div>
      </Section>

      <Section
        title="Select · MultiSelect · Menu"
        note="One floating primitive: a portal, flipped by the room above and below, clamped in the window, the height capped with the chosen item scrolled into view. It is an iOS 26 menu on every screen: 250 pt of glass, 42 pt rows of 17 pt text, icons leading, a check column only when the menu checks something, a capsule under the highlighted row, hairlines between groups; it grows out of its button and shrinks back into it."
      >
        <div className={styles.row}>
          <Select label="Размер" value={size} onChange={setSize} options={SIZES.map((v) => ({ value: v, label: v === '0' ? 'Без ограничения' : `До ${v} ГБ` }))} />
          <Select label="Сезон" showLabel value="2" onChange={() => {}} options={[{ value: '1', label: '1' }, { value: '2', label: '2' }]} />
          <Select label="Битрейт" disabled value="320" onChange={() => {}} options={[{ value: '320', label: '320 кбит/с' }]} />
          <Menu label="Ещё" placement="bottom-end" trigger={(p) => <IconButton {...p} icon="ellipsis" label="Ещё" />}>
            {(close) => (
              <>
                <MenuHeading>Сортировка</MenuHeading>
                <MenuItem label="По дате добавления" checked onSelect={() => close()} />
                <MenuItem label="По названию" checked={false} detail="А → Я" onSelect={() => close()} />
                <MenuSeparator />
                <MenuItem icon={<Icon name="pause" />} label="Приостановить загрузку" onSelect={() => close()} />
                <MenuItem icon={<Icon name="restart" />} label="Очищено" badge={{ text: 'Новое' }} onSelect={() => close()} />
                <MenuItem icon={<Icon name="trash" />} label="Удалить…" destructive onSelect={() => close()} />
                <MenuItem label="Недоступно" disabled />
              </>
            )}
          </Menu>
          {/* the profile menu's shape: a head row (avatar, name, role), places, a titled group of choices, a last group */}
          <Menu label="Профиль" placement="bottom-end" pickList={false} trigger={(p) => <IconButton {...p} icon="person" label="Профиль" />}>
            {(close) => (
              <>
                <MenuItem aside label="Александра Константинопольская" detail="Администратор" icon={<Avatar name="Александра" avatar="grad-3" size={30} />} onSelect={() => close()} />
                <MenuSeparator />
                <MenuItem icon={<Icon name="calendar" />} label="Календарь" onSelect={() => close()} />
                <MenuItem icon={<Icon name="folder" />} label="Файлы" onSelect={() => close()} />
                <MenuItem icon={<Icon name="sliders" />} label="Настройки" badge={{ text: '3' }} onSelect={() => close()} />
                <MenuSeparator />
                <div role="group" aria-label="Медиатека">
                  <MenuHeading>Медиатека</MenuHeading>
                  <MenuItem label="Общая медиатека" checked={false} onSelect={() => {}} />
                  <MenuItem label="Моя медиатека" checked onSelect={() => {}} />
                </div>
                <MenuSeparator />
                <MenuItem icon={<Icon name="switchUser" />} label="Сменить профиль" onSelect={() => close()} />
              </>
            )}
          </Menu>
          <Menu label="Вид текста" placement="bottom-end" trigger={(p) => <IconButton {...p} icon="ellipsis" label="Вид текста" />}>
            {() => (
              <>
                <MenuStepper label="Размер" value="100 %" less={{ label: 'Меньше', onPress: () => {}, children: 'A−' }} more={{ label: 'Больше', onPress: () => {}, children: 'A+' }} />
                <MenuHeading>Поля</MenuHeading>
                <MenuSegments
                  label="Поля"
                  value={seg}
                  onChange={setSeg}
                  options={[
                    { value: '720', label: 'Узкие' },
                    { value: '1080', label: 'Обычные' },
                    { value: '2160', label: 'Широкие' },
                  ]}
                />
              </>
            )}
          </Menu>
        </div>
      </Section>

      <Section title="StatusRing · ProgressBar · Badge · SegmentedControl · Toggle">
        <div className={styles.row}>
          <StatusRing state="active" value={0.05} />
          <StatusRing state="active" value={0.64} />
          <StatusRing state="paused" value={0.35} />
          <StatusRing state="error" value={0} />
          <StatusRing state="done" value={1} />
          <StatusRing state="bad" value={1} />
          <StatusRing state="off" value={0} />
          <StatusRing state="done" value={1} size={28} />
          <StatusRing state="error" value={1} size={28} />
          <StatusRing state="bad" value={1} size={28} />
          <StatusRing state="off" value={0} size={28} />
        </div>
        <div className={styles.bars}>
          <ProgressBar value={0.64} />
          <ProgressBar value={0.35} tone="paused" />
          <ProgressBar value={0.1} tone="warning" />
          <ProgressBar value={0.5} tone="label" />
        </div>
        <div className={styles.row}>
          <Badge>1080p</Badge>
          <Badge tone="strong">RU</Badge>
          <Badge tone="outline">16+</Badge>
          <Badge round>без сохранения</Badge>
          <Badge tone="accent" round>
            Новое
          </Badge>
          <Badge tone="success" round>
            Готово
          </Badge>
          <Badge tone="warning">FLAC</Badge>
          <Badge tone="destructive" round>
            Ошибка
          </Badge>
        </div>
        <div className={styles.row}>
          <Badge tone="strong" round size="status">
            <Icon name="clock" size={16} />
            Ждёт ответа
          </Badge>
          <Badge tone="success" round size="status">
            <Icon name="checkCircle" size={16} />
            Одобрено
          </Badge>
        </div>
        <div className={styles.row}>
          <SegmentedControl
            label="Раздел"
            value={seg}
            onChange={setSeg}
            segments={[
              { value: 'all', label: 'Все' },
              { value: '720', label: 'Фильмы' },
              { value: '1080', label: 'Сериалы' },
              { value: 'music', label: 'Музыка' },
            ]}
          />
          <div style={{ width: '100%' }}>
            <SegmentedControl label="Тип модели (fill)" value={seg} onChange={setSeg} fill segments={[{ value: 'all', label: 'Текст' }, { value: '720', label: 'Распознавание речи' }]} />
          </div>
          <Toggle label="Вкл." checked={on} onChange={setOn} />
          <Toggle label="Выкл." checked={false} onChange={() => {}} />
          <Toggle label="Недоступно" checked disabled onChange={() => {}} />
          <Toggle label="Маленький" size="small" checked={on} onChange={setOn} />
          <Toggle label="Маленький выкл." size="small" checked={false} onChange={() => {}} />
        </div>
      </Section>

      <Section title="Notice" note="A state that waits for the viewer or a problem worth reading: a tinted ground, the glyph in the tint, the actions under the words. as=&quot;li&quot; makes it a group's first row.">
        <Notice
          title="Пропало 412 каналов из 1 300 — подтвердить?"
          actions={
            <>
              <Button variant="secondary" size="small">Оставить старый список</Button>
              <Button variant="secondary" size="small">Подтвердить</Button>
            </>
          }
        >
          Провайдер прислал список короче прежнего. Пока вы не решите, остаётся старый.
        </Notice>
        <Notice tone="accent">Новые каналы источника скрыты: в плейлисте больше 300 каналов.</Notice>
        <Notice tone="destructive">Последнее обновление не удалось.</Notice>
      </Section>

      <Section title="FilterChips" note="One choice of many (a playlist's groups), each with its count; wraps on a wide window, one sideways-scrolling line on a phone (--chips-bleed). 2–5 fixed words: SegmentedControl.">
        <FilterChips
          label="Группы"
          value={chip}
          onChange={setChip}
          formatCount={(n) => n.toLocaleString('ru-RU')}
          chips={[{ value: '', label: 'Все группы', count: 3000 }, ...['Новости', 'Спорт', 'Кино', 'Сериалы', 'Детям', 'Музыка', 'Познавательные', 'Развлекательные', 'Региональные'].map((g, i) => ({ value: g, label: g, count: 120 + i * 37 }))]}
        />
      </Section>

      <Section title="VirtualList" note="Thousands of rows of one height; only those near the screen are in the DOM. It follows whichever scroller moves (here a box of its own; on a page, the window).">
        <div className={styles.virtualBox}>
          <VirtualList
            count={5000}
            itemHeight={44}
            label="5 000 строк"
            renderItem={(i) => (
              <div className={styles.virtualRow}>
                <span>{i + 1}</span>
                <span>Строка {i + 1}</span>
              </div>
            )}
          />
        </div>
      </Section>


      <Section
        title="Phone"
        note="The parts the phone layout is built from. Open this page at phone width (390) to see their phone form: the tiles, the bottom sheets, the swipe. In the frames the bars sit at the bottom of a pretend screen."
      >
        <h3 className={styles.h3}>TabBar · PlayerBar</h3>
        <div className={styles.screen}>
          <img className={styles.screenArt} src="/api/images/interstellar-backdrop.jpg" alt="" />
          <PlayerBar label="Сейчас играет" title="Child in Time" subtitle="Deep Purple" artwork="/api/images/drain-poster.jpg" fallbackIcon="music" progress={0.35} onOpen={() => {}} openLabel="Открыть плеер">
            <PlayerBarButton icon="previous" label="Предыдущий трек" onClick={() => {}} />
            <PlayerBarButton icon="pause" label="Пауза" prominent onClick={() => {}} />
            <PlayerBarButton icon="next" label="Следующий трек" onClick={() => {}} />
          </PlayerBar>
          <TabBar
            label="Разделы (пример)"
            items={[
              { to: '/dev/gallery', label: 'Главная', icon: 'homeFill' },
              { to: '/movies', label: 'Фильмы', icon: 'filmFrame' },
              { to: '/series', label: 'Сериалы', icon: 'playTv' },
              { to: '/tv', label: 'ТВ', icon: 'antennaRadiowaves' },
              { to: '/music', label: 'Медиа', icon: 'squareStackFill', roots: ['/music', '/books'] },
            ]}
            accessory={{ to: '/search', label: 'Поиск', icon: 'magnifyingglass' }}
            expandLabel="Показать вкладки"
          />
        </div>

        <h3 className={styles.h3}>ActionTiles</h3>
        <p className={styles.note}>Round buttons in the action row on a wide screen; equal tiles with their labels on a phone. Four fit a 375 pt phone: a destructive action (the admin’s delete) is the last item of «Ещё» there.</p>
        <div className={styles.actionRow}>
          <Button size="large" icon="play">
            Смотреть
          </Button>
          <ActionTiles>
            <IconButton size="lg" icon="plus" label="Добавить сезон…" tile="Добавить" />
            <IconButton size="lg" icon="eye" label="Следить" tile toggled={following} onClick={() => setFollowing(!following)} />
            <IconButton size="lg" icon="watchlist" label="Хочу посмотреть" tile toggled={wl} onClick={() => setWl(!wl)} />
            <IconButton size="lg" icon="ellipsis" label="Ещё" tile />
          </ActionTiles>
        </div>

        <h3 className={styles.h3}>SwipeRow</h3>
        <p className={styles.note}>Swipe a row to the left with a finger (touch emulation on a desktop). The same actions are always in the row’s «⋯» too.</p>
        <ul className={styles.swipeList}>
          {['Интерстеллар', 'Форс-мажоры', 'Deep Purple in Rock'].map((name) => (
            <SwipeRow
              key={name}
              className={styles.swipeItem}
              contentClassName={styles.swipeContent}
              actions={[
                { key: 'pause', label: 'Пауза', icon: 'pause', tone: 'accent', onSelect: () => setToast(`«${name}»: пауза`) },
                { key: 'delete', label: 'Удалить', icon: 'trash', tone: 'destructive', onSelect: () => setToast(`«${name}» удалён`) },
              ]}
            >
              <span className={styles.leading}>
                <Icon name="film" size={18} />
              </span>
              <span className={styles.swipeName}>{name}</span>
              <IconButton size="sm" variant="plain" icon="ellipsis" label="Ещё" />
            </SwipeRow>
          ))}
        </ul>

        <h3 className={styles.h3}>Sheet · Toast</h3>
        <p className={styles.note}>A centred alert or panel on a wide screen; a bottom sheet across the width on a phone, its buttons at the thumb (three or more stacked). A toast stays clear of the tab bar and the player bar.</p>
        <div className={styles.row}>
          <Button size="small" variant="secondary" onClick={() => setSheet('alert')}>
            Alert
          </Button>
          <Button size="small" variant="secondary" onClick={() => setSheet('three')}>
            Three buttons
          </Button>
          <Button size="small" variant="secondary" onClick={() => setSheet('busy')}>
            Busy (a request is out)
          </Button>
          <Button size="small" variant="secondary" onClick={() => setSheet('wide')}>
            Wide with a field
          </Button>
          <Button size="small" variant="secondary" onClick={() => setSheet('large')}>
            Large
          </Button>
          <Button size="small" variant="secondary" onClick={() => setToast('Добавлено в очередь')}>
            Toast
          </Button>
        </div>
        <Toast messageKey={toast} action={toast ? { label: 'Отменить', onClick: () => setToast('') } : undefined}>
          {toast}
        </Toast>
        {sheet === 'alert' && (
          <Sheet
            title="Удалить «Интерстеллар»?"
            message="Файлы фильма уйдут в Корзину."
            icon={<Icon name="trash" size={34} strokeWidth={1.6} />}
            onCancel={() => setSheet('')}
            actions={[
              { label: 'Отменить', kind: 'cancel', run: () => setSheet('') },
              { label: 'Удалить', kind: 'destructive', run: () => setSheet('') },
            ]}
          />
        )}
        {sheet === 'busy' && (
          <Sheet
            title="Удалить «Интерстеллар»?"
            message="Файлы фильма уйдут в Корзину."
            busy
            onCancel={() => setSheet('')}
            actions={[
              { label: 'Отменить', kind: 'cancel', run: () => setSheet('') },
              { label: 'Удалить', kind: 'destructive', run: () => setSheet('') },
            ]}
          />
        )}
        {sheet === 'three' && (
          <Sheet
            title="Остановить загрузку?"
            message="Уже загруженное можно оставить."
            onCancel={() => setSheet('')}
            actions={[
              { label: 'Не останавливать', kind: 'cancel', run: () => setSheet('') },
              { label: 'Оставить загруженное', run: () => setSheet('') },
              { label: 'Удалить всё', kind: 'destructive', run: () => setSheet('') },
            ]}
          />
        )}
        {sheet === 'wide' && (
          <Sheet
            title="Новый плейлист"
            role="dialog"
            wide
            onCancel={() => setSheet('')}
            actions={[
              { label: 'Отменить', kind: 'cancel', run: () => setSheet('') },
              { label: 'Создать', kind: 'primary', run: () => setSheet('') },
            ]}
          >
            <TextField label="Название" placeholder="Мой плейлист" />
          </Sheet>
        )}
        {sheet === 'large' && (
          <Sheet title="Фильмы" message="252 ГБ" role="dialog" size="large" dismissOnOutside onCancel={() => setSheet('')}>
            <GroupedSection>
              {['Interstellar (2014)', 'Catch Me If You Can (2002)', 'The Wolf of Wall Street (2013)', 'Garlic (2021)', 'Elections (2020)'].map((n) => (
                <ListRow key={n} label={n} hint="18,4 ГБ" />
              ))}
            </GroupedSection>
          </Sheet>
        )}
      </Section>

      <Section
        title="SegmentedControl · row"
        note="A page's filter under its title (Поиск): centred in the column; when it does not fit, the whole pill scrolls out to the window's edges (--segmented-bleed) and the edge with more segments past it fades. Below it, the same in a phone-wide column."
      >
        <SegmentedControl label="Что искать" value={scope} onChange={setScope} segments={SCOPES} row />
        <div className={styles.phoneColumn}>
          <SegmentedControl label="Что искать" value={scope} onChange={setScope} segments={SCOPES} row />
        </div>
        <p className={styles.note}>align="start": a filter over a list that starts at the column's edge («ТВ»: Все · Избранное · темы).</p>
        <SegmentedControl label="Что искать" value={scope} onChange={setScope} segments={SCOPES} row align="start" />
      </Section>

      <Section title="Spinner" note="A busy ring in the colour of the text around it. Sizes are the glyph sizes; the ring turns by the global .spin class (design/motion.css) — one keyframes, Reduce motion slows it to 2.4 s. Button, IconButton and Sheet draw this one when busy.">
        <div className={styles.row}>
          {(['xs', 'sm', 'md', 'lg'] as const).map((z) => (
            <span key={z} className={styles.spinnerCell}>
              <Spinner size={z} />
              <code>{z}</code>
            </span>
          ))}
          <span className={styles.spinnerCell}>
            <Spinner />
            <code>default</code>
          </span>
          <span className={`${styles.spinnerCell} ${styles.spinnerArt}`}>
            <Spinner size="lg" track />
            <code>track</code>
          </span>
          <span className={styles.spinnerCell}>
            <span className={styles.spinnerAccent}>
              <Spinner size="md" label="Загрузка" />
            </span>
            <code>label</code>
          </span>
        </div>
      </Section>

      <Section title="EmptyState · Skeleton" note="EmptyState: nothing here yet, or the load failed (role alert). Skeleton: a quiet placeholder, no shimmer; the caller gives it size and shape.">
        <div className={styles.emptyStage}>
          <EmptyState icon={<Icon name="film" size={48} strokeWidth={1.4} />} title="Здесь пока пусто" hint="Добавьте фильмы в папку «Фильмы» на сервере и запустите сканирование." actions={<Button variant="secondary">Сканировать в настройках</Button>} />
        </div>
        <div className={styles.emptyStage}>
          <EmptyState role="alert" icon={<Icon name="cloud" size={44} strokeWidth={1.5} />} title="Не удалось загрузить" hint="Сервер не ответил." actions={<Button variant="secondary">Повторить</Button>} />
        </div>
        <div className={styles.row}>
          <Skeleton className={styles.skeletonPoster} />
          <Skeleton className={styles.skeletonPoster} />
          <Skeleton tone="soft" className={styles.skeletonTitle} />
        </div>
      </Section>

      <Section title="Slider" note="One value out of a range, the video bars' volume slider as a kit part: a thin track filled in white, a white thumb under the pointer and the focus (thumb=&quot;always&quot;: always there), the accent ring round the thumb for the keyboard, a 44 pt strip to the finger.">
        <div className={styles.sliders}>
          <Slider label="Громкость" value={volume} onChange={setVolume} valueText={`${Math.round(volume * 100)}%`} />
          <Slider label="Громкость" value={volume} onChange={setVolume} thumb="always" />
          <Slider label="Громкость" value={0.3} onChange={() => {}} disabled />
        </div>
      </Section>

      <Section title="ListSelectRow" note="A row that chooses: the link row's button with the check of the choice made at the trailing edge. Without selected it only acts and has no check.">
        <GroupedSection title="Источник">
          {[
            ['hd', 'Источник A · 1080p', 'RU · субтитры'],
            ['sd', 'Источник A · 720p', 'RU'],
            ['off', 'Недоступный источник', 'Нет ответа'],
          ].map(([v, label, hint]) => (
            <ListSelectRow key={v} label={label} hint={hint} selected={source === v} disabled={v === 'off'} onSelect={() => setSource(v!)} />
          ))}
          <ListSelectRow label="Другой источник…" onSelect={() => setToast('Другой источник')} />
        </GroupedSection>
        <GroupedSection title="tone" footer="An action's word in the accent (Add, Check) or the destructive red (Delete); the hint keeps its colour.">
          <ListSelectRow tone="accent" label="Проверить все" hint="Каждой включённой модели задаётся короткий вопрос." onSelect={() => {}} />
          <ListSelectRow tone="destructive" label="Удалить" onSelect={() => {}} />
        </GroupedSection>
      </Section>

      <Section title="Checkbox" note="A choice that changes what an action will do. A description under the label; indeterminate for a group's box over its rows; no label for a box in a table row (aria-label names it); card: the whole card is the choice (its surface and padding are the caller's).">
        <div className={styles.fields}>
          <Checkbox label="Удалить навсегда, минуя корзину" tone="destructive" checked={checks.a} onChange={(a) => setChecks({ ...checks, a })} />
          <Checkbox label="Сохранить список" description="Список останется в профиле и после выхода." checked={checks.b} onChange={(b) => setChecks({ ...checks, b })} />
          <Checkbox label="Все файлы" indeterminate={checks.b !== checks.c} checked={checks.b && checks.c} onChange={(on) => setChecks({ ...checks, b: on, c: on })} />
          <Checkbox aria-label="Выбрать строку" checked={checks.c} onChange={(c) => setChecks({ ...checks, c })} />
          <Checkbox label="Недоступно" checked disabled onChange={() => {}} />
        </div>
        <div className={styles.cards}>
          {(['a', 'b'] as const).map((k) => (
            <Checkbox
              key={k}
              card
              className={styles.checkCard}
              checked={checks[k]}
              onChange={(on) => setChecks({ ...checks, [k]: on })}
              label={
                <span className={styles.checkCardBody}>
                  <span className={styles.leading} aria-hidden>
                    <Icon name="plug" size={18} strokeWidth={2} />
                  </span>
                  <span className={styles.checkCardText}>
                    <span className={styles.checkCardName}>{k === 'a' ? 'Старый сервис' : 'Другой сервис'}</span>
                    <span>Найдена база: 12 профилей</span>
                  </span>
                </span>
              }
            />
          ))}
        </div>
      </Section>

      <Section title="TextArea · time" note="TextField's field for several lines (the corner stretches it down) and a time of day.">
        <div className={styles.fields}>
          <TextArea label="Заметка" placeholder="Что вы думаете об этой главе?" hint="Видна только вам." />
          <TextArea label="Адреса" error="Один адрес в строке" rows={3} defaultValue="http://" />
          <TextField label="С" type="time" size="small" defaultValue="22:00" />
        </div>
      </Section>

      <Section title="Chips · RankedList" note="Chips: FilterChips (one choice, radio buttons) and ToggleChips (several, toggles) are this one component, the capsules of one look. RankedList: an ordered list of names with «+ Добавить…» (built on SortableList).">
        <div className={styles.fields}>
          <Chips
            multiple
            label="Жанры"
            value={genres}
            onChange={(v, on) => setGenres(on ? [...genres, v] : genres.filter((g) => g !== v))}
            chips={[
              { value: 'drama', label: 'Драма', count: 214 },
              { value: 'comedy', label: 'Комедия', count: 98 },
              { value: 'scifi', label: 'Фантастика' },
            ]}
          />
          <RankedList
            label="Мои языки"
            items={ranked}
            itemLabel={(v) => LANGS[v] ?? v}
            onChange={setRanked}
            choices={() => Object.keys(LANGS).filter((c) => !ranked.includes(c)).map((c) => ({ value: c, label: LANGS[c]! }))}
            onAdd={(v) => setRanked([...ranked, v])}
            addLabel="Добавить язык"
          />
        </div>
      </Section>
    </main>
  )
}
