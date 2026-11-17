// An addon's own page: /addons/<addonId>/<path> draws the route the addon
// registered under that path (inside the app's shell; not for kids). While
// the addons are still loading it waits; an addon without such a page gets
// the plain «not here» screen, and one that throws a notice.
import { useEffect } from 'react'
import { useParams } from 'react-router'
import { APP_NAME } from '../brand'
import { useT } from '../i18n'
import { useAddonsLoading } from '../addons/registry'
import { AddonBoundary, useAddonLabel, useAddonSlot } from '../addons/slots'
import { ComingSoon } from './ComingSoon'

const trim = (p: string) => p.replace(/^\/+|\/+$/g, '')

export default function AddonPage() {
  const t = useT()
  const { addonId = '', '*': rest = '' } = useParams()
  const routes = useAddonSlot('routes')
  const loading = useAddonsLoading()
  const label = useAddonLabel(addonId)
  const route = routes.find((r) => r.addonId === addonId && trim(r.path) === trim(rest))
  // the title is a key of the addon's strings
  const title = route ? label(route.title) : ''
  useEffect(() => {
    if (title) document.title = `${title} — ${APP_NAME}`
  }, [title])
  if (!route) {
    if (loading) return <div aria-busy="true" aria-label={t('common.loading')} />
    return <ComingSoon title={t('app.notFound.title')} icon="info" text={t('addons.pageMissing')} />
  }
  const Element = route.element
  // a new page of the addon starts with a fresh boundary
  return (
    <AddonBoundary key={`${addonId}/${trim(rest)}`} addonId={addonId}>
      <Element />
    </AddonBoundary>
  )
}
