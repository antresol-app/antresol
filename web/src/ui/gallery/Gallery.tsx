// /dev/gallery (dev server only): every token and every component of the
// design system in every state, on the app's real background — the page to
// check a change against before it ships. Developer tooling: its own
// headings are plain English, never translated; the components' strings
// come from the catalogs as everywhere else.
import { useState, type ReactNode } from 'react'
import type { Torrent } from '../../api/types'
import { Icon } from '../../components/Icon'
import { DownloadRow } from '../../pages/manage/DownloadRow'
import { ActionTiles } from '../ActionTiles'
import { Badge } from '../Badge'
import { Button } from '../Button'
import { Card } from '../Card'
import { IconButton } from '../IconButton'
import { GroupedSection, ListRow } from '../List'
import { Menu, MenuHeading, MenuItem, MenuSeparator } from '../Menu'
import { NumberField } from '../NumberField'
import { PlayerBar, PlayerBarButton } from '../PlayerBar'
import { ProgressBar } from '../ProgressBar'
import { SegmentedControl } from '../SegmentedControl'
import { Sheet } from '../Sheet'
import { SwipeRow } from '../SwipeRow'
import { TabBar } from '../TabBar'
import { MultiSelect, Select } from '../Select'
import { ProgressRing, StatusRing } from '../StatusRing'
import { TextField } from '../TextField'
import { Toast } from '../Toast'
import { Toggle } from '../Toggle'
import { DownloadTile } from './DownloadTile'
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

const GB = 1024 ** 3
const tor = (o: Partial<Torrent>): Torrent => ({
  infoHash: Math.random().toString(16).slice(2).padEnd(40, '0'),
  name: '',
  mode: 'download',
  status: 'downloading',
  size: 30 * GB,
  done: 0,
  downRate: 0,
  upRate: 0,
  uploaded: 0,
  downloaded: 0,
  peers: 0,
  seeds: 0,
  ratio: 0,
  itemId: null,
  addedAt: new Date().toISOString(),
  saveDir: '/t',
  organized: false,
  kind: 'movie',
  addedByName: 'admin',
  ...o,
})
const TORRENTS: Torrent[] = [
  tor({ title: 'Интерстеллар', poster: 'interstellar-poster.jpg', size: 29.2 * GB, done: 6.7 * GB, downRate: 3.3e6, peers: 15 }),
  tor({ title: 'Форс-мажоры', kind: 'series', poster: 'catchme-poster.jpg', status: 'paused', size: 1100 * GB, done: 4.3 * GB }),
  tor({ title: 'Planet Earth II', kind: 'series', poster: 'pe2-poster.jpg', status: 'waiting_space', size: 128 * GB }),
  tor({ title: 'Seven Worlds, One Planet', poster: 'sevenworlds-poster.jpg', status: 'error', error: 'The files are the same as another download’s' }),
  tor({ title: 'Deep Purple in Rock', kind: 'music', poster: 'drain-poster.jpg', status: 'done', direct: true, size: 362e6, done: 362e6, uploaded: 367e6 }),
  tor({ title: 'Countdown to Extinction', kind: 'music', poster: 'tiny-poster.jpg', status: 'done', direct: true, size: 900e6, done: 900e6, uploaded: 23.9e6, upRate: 1.2e6 }),
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
  const [scope, setScope] = useState('all')
  const [on, setOn] = useState(true)
  const [size, setSize] = useState('0')
  const [langs, setLangs] = useState(['ru', 'en'])
  const [mbs, setMbs] = useState(2)
  const [gb, setGb] = useState(10)
  const [wl, setWl] = useState(false)
  const [sheet, setSheet] = useState<'' | 'alert' | 'wide' | 'large' | 'three'>('')
  const [toast, setToast] = useState('')
  const [following, setFollowing] = useState(false)
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

      <Section title="IconButton" note="Every round button. One size = one diameter, one glass, one glyph size and colour; only the tone (red) and the toggled state (white) differ.">
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

      <Section title="Select · MultiSelect · Menu" note="One floating primitive: a portal, flipped by the room above and below, clamped in the window, the height capped with the chosen item scrolled into view.">
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
                <MenuItem icon={<Icon name="pause" size={16} />} label="Приостановить загрузку" onSelect={() => close()} />
                <MenuItem icon={<Icon name="restart" size={16} />} label="Очищено" badge={{ text: 'Новое' }} onSelect={() => close()} />
                <MenuItem icon={<Icon name="trash" size={16} />} label="Удалить…" destructive onSelect={() => close()} />
                <MenuItem label="Недоступно" disabled />
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
          <Toggle label="Вкл." checked={on} onChange={setOn} />
          <Toggle label="Выкл." checked={false} onChange={() => {}} />
          <Toggle label="Недоступно" checked disabled onChange={() => {}} />
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
              { to: '/dev/gallery', label: 'Главная', icon: 'home', end: true },
              { to: '/movies', label: 'Фильмы', icon: 'film' },
              { to: '/series', label: 'Сериалы', icon: 'tv' },
              { to: '/music', label: 'Музыка', icon: 'music' },
              { to: '/books', label: 'Книги', icon: 'book' },
            ]}
            accessory={{ to: '/search', label: 'Поиск', icon: 'search' }}
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
      </Section>

      <Section
        title="Downloads · option A (chosen): list rows"
        note="One list, one row height, one trailing column: the row's action (pause, resume, «На сервер»; an empty slot when there is none) and «⋯». A download has a bar; a finished one is quiet."
      >
        <GroupedSection title="Загрузки">
          {TORRENTS.filter((x) => x.status !== 'done').map((x) => (
            <DownloadRow key={x.infoHash} t={x} onChanged={() => {}} />
          ))}
        </GroupedSection>
        <GroupedSection title="Готово">
          {TORRENTS.filter((x) => x.status === 'done').map((x) => (
            <DownloadRow key={x.infoHash} t={x} onChanged={() => {}} />
          ))}
        </GroupedSection>
      </Section>

      <Section title="Downloads · option B: cards" note="A grid of cards with the poster at full height and the App Store ring as the action. Kept for comparison: two columns of cards with a 122-item list read as a wall.">
        <ul className={styles.tiles}>
          {TORRENTS.map((x) => (
            <DownloadTile key={x.infoHash} t={x} />
          ))}
        </ul>
      </Section>
    </main>
  )
}
