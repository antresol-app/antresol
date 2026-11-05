import { Browse } from './Browse'
import { useT } from '../i18n'

export function Series() {
  const t = useT()
  return <Browse kind="series" title={t('common.series')} emptyTitle={t('browse.seriesEmpty')} />
}
