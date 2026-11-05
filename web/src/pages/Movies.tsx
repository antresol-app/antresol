import { Browse } from './Browse'
import { useT } from '../i18n'

export function Movies() {
  const t = useT()
  return <Browse kind="movie" title={t('common.movies')} emptyTitle={t('browse.moviesEmpty')} />
}
