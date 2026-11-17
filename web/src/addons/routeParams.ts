// The `:name` values of the addon page being drawn, for the `useParams()` an
// addon gets: the addon's bundle uses the app's react-router, whose useParams
// answers for the core's own route (/addons/:addonId/*), not for the
// addon's path. AddonPage provides the matched values; the shared
// useParams puts them over the router's.
import { createContext, useContext, useMemo } from 'react'
import { useParams } from 'react-router'

export const AddonRouteParams = createContext<Readonly<Record<string, string>> | null>(null)

/**
 * react-router's useParams, and inside an addon's page also the values of
 * that page's `:name` segments (the router's own `addonId` and `*` stay).
 * The same object while neither changes.
 */
export function useAddonParams(): ReturnType<typeof useParams> {
  const core = useParams()
  const page = useContext(AddonRouteParams)
  return useMemo(() => (page ? { ...core, ...page } : core), [core, page])
}
