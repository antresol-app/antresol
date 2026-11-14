// «Отметка» of a title (tracking): the profile's state on a series or movie
// («Хочу посмотреть», «Смотрю», «Просмотрено», «Отложено», «Брошено»; a movie
// has the first and third only), «Убрать отметку». The button shows the
// current state. `large`: a round button of the action bar (a tile on
// phones); `small`: a text button.
import type { MarkState } from '../api/types'
import { useT, type Key } from '../i18n'
import { useMark } from '../state/useMark'
import { Button } from '../ui/Button'
import { IconButton } from '../ui/IconButton'
import { Menu, MenuItem, MenuSeparator } from '../ui/Menu'

export interface MarkButtonProps {
  kind: 'series' | 'movie'
  tmdbId: number
  size?: 'large' | 'small'
  /** A title off the library: its name and poster, kept by the server with the mark. */
  name?: string
  poster?: string
}

const SERIES_STATES: MarkState[] = ['planned', 'watching', 'completed', 'on_hold', 'dropped']
const MOVIE_STATES: MarkState[] = ['planned', 'completed']

export function MarkButton({ kind, tmdbId, size = 'large', name: titleName, poster }: MarkButtonProps) {
  const t = useT()
  const { mark, failed, set, clear } = useMark(kind, tmdbId, { name: titleName, poster })
  const states = kind === 'movie' ? MOVIE_STATES : SERIES_STATES
  const stateLabel = (s: MarkState) => t(`tracking.mark.${s}` as Key)
  const shown = mark ? stateLabel(mark.state) : t('tracking.mark.none')
  const name = mark ? `${t('tracking.mark.aria')}: ${shown}` : t('tracking.mark.aria')

  return (
    <>
      <Menu
        label={t('tracking.mark.aria')}
        trigger={(p) =>
          size === 'small' ? (
            <Button {...p} variant="secondary" size="small" icon={mark ? 'bookmarkFill' : 'bookmark'} aria-label={name}>
              {shown}
            </Button>
          ) : (
            <IconButton {...p} size="lg" icon={mark ? 'bookmarkFill' : 'bookmark'} toggled={!!mark} label={name} tile={shown} />
          )
        }
      >
        {(close) => (
          <>
            {states.map((s) => (
              <MenuItem key={s} label={stateLabel(s)} checked={mark?.state === s} onSelect={() => (set(s), close())} />
            ))}
            {mark && (
              <>
                <MenuSeparator />
                <MenuItem label={t('tracking.mark.clear')} onSelect={() => (clear(), close())} />
              </>
            )}
          </>
        )}
      </Menu>
      {failed && (
        <span className="visually-hidden" role="alert">
          {t('tracking.mark.failed')}
        </span>
      )}
    </>
  )
}
