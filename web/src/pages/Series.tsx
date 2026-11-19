import { Browse } from './Browse'
import { ButtonLink } from '../ui/Button'
import { useT } from '../i18n'

export function Series() {
  const t = useT()
  return (
    <Browse
      kind="series"
      title={t('common.series')}
      emptyTitle={t('browse.seriesEmpty')}
      // what comes out of the series this profile follows
      tools={
        <ButtonLink to="/calendar" variant="secondary" icon="calendar">
          {t('tracking.calendar.title')}
        </ButtonLink>
      }
    />
  )
}
