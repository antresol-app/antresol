// An addon's own page: /addons/<addonId>/<path> draws the route the addon
// registered under that path (inside the app's shell; not for kids), with the
// values of its `:name` segments (the element's `params`, and the addon's
// useParams()). While the addons are still loading it waits; an addon
// without such a page gets the plain «not here» screen, and one that throws
// a notice.
import { useEffect, useMemo } from 'react'
import { useLocation, useParams } from 'react-router'
import { APP_NAME } from '../brand'
import { useT } from '../i18n'
import { useAddonsLoading } from '../addons/registry'
import { AddonRouteParams } from '../addons/routeParams'
import { AddonBoundary, addonRest, useAddonLabel, useAddonRoute } from '../addons/slots'
import { ComingSoon } from './ComingSoon'

export default function AddonPage() {
  const t = useT()
  const { addonId = '' } = useParams()
  const rest = addonRest(useLocation().pathname, addonId)
  const found = useAddonRoute(addonId, rest)
  const loading = useAddonsLoading()
  const label = useAddonLabel(addonId)
  // the title is a key of the addon's strings
  const title = found ? label(found.route.title) : ''
  // the values as one object that stays while they do (the addons' registry
  // changing re-matches the route, but a page may keep its params in effect deps)
  const key = JSON.stringify(found?.params ?? {})
  const params = useMemo<Readonly<Record<string, string>>>(() => JSON.parse(key), [key])
  useEffect(() => {
    if (title) document.title = `${title} — ${APP_NAME}`
  }, [title])
  if (!found) {
    if (loading) return <div aria-busy="true" aria-label={t('common.loading')} />
    return <ComingSoon title={t('app.notFound.title')} icon="info" text={t('addons.pageMissing')} />
  }
  const Element = found.route.element
  // a new page of the addon starts with a fresh boundary
  return (
    <AddonBoundary key={`${addonId}/${rest}`} addonId={addonId}>
      <AddonRouteParams.Provider value={params}>
        <Element params={params} />
      </AddonRouteParams.Provider>
    </AddonBoundary>
  )
}
