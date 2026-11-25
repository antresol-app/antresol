// A page whose data did not load: the kit's empty state with the server's
// message and a retry. `inset`: the page has no gutters of its own.
import { Button } from '../ui/Button'
import { EmptyState } from '../ui/EmptyState'
import { Icon } from './Icon'

export function LoadFailed({ title, message, retry, onRetry, inset }: { title: string; message: string; retry: string; onRetry: () => void; inset?: boolean }) {
  return (
    <EmptyState
      role="alert"
      inset={inset}
      icon={<Icon name="cloud" size={44} strokeWidth={1.5} />}
      title={title}
      hint={message}
      actions={
        <Button variant="secondary" onClick={onRetry}>
          {retry}
        </Button>
      }
    />
  )
}
