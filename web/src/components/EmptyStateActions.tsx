// The buttons addons put on an empty library page (their
// `emptyStateActions` of the page's scope), beside the core's own way on
// (RescanLink). Each is drawn in its own boundary; kids get none.
import { AddonBoundary, resolveAddonPath, useAddonLabel, useAddonSlot } from '../addons/slots'
import type { Contribution, EmptyStateAction } from '../addons/registry'
import { ButtonLink } from '../ui/Button'

function Action({ entry, variant }: { entry: Contribution<'emptyStateActions'>; variant?: 'secondary' }) {
  const label = useAddonLabel(entry.addonId)
  return (
    <ButtonLink to={resolveAddonPath(entry.addonId, entry.to)} icon={entry.icon ?? 'search'} variant={variant}>
      {label(entry.label)}
    </ButtonLink>
  )
}

export function EmptyStateActions({ scope, variant }: { scope: EmptyStateAction['scope']; variant?: 'secondary' }) {
  const entries = useAddonSlot('emptyStateActions')
  return (
    <>
      {entries.map((e, i) =>
        e.scope === scope ? (
          <AddonBoundary key={`${e.addonId}:${i}`} addonId={e.addonId} fallback={null}>
            <Action entry={e} variant={variant} />
          </AddonBoundary>
        ) : null,
      )}
    </>
  )
}
