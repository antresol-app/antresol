// A kid's music or audiobook stopped by «Экранное время»: the friendly
// stop over the app (the server refused the stream, or ended it at the
// limit). «Закрыть» only hides it; playing again asks the server again.
import { useAudioSelect } from './audio/useAudio'
import { audio } from './audio/engine'
import { Sheet } from '../ui/Sheet'
import { Icon } from '../components/Icon'
import { useT } from '../i18n'

export function AudioScreenStop() {
  const t = useT()
  const message = useAudioSelect((s) => s.screenStop)
  if (!message) return null
  return (
    <Sheet
      title={t('player.screen.overTitle')}
      message={message}
      icon={<Icon name="moon" size={34} strokeWidth={1.6} />}
      onCancel={() => audio.dismissScreenStop()}
      actions={[{ label: t('common.close'), kind: 'primary', run: () => audio.dismissScreenStop() }]}
    />
  )
}
