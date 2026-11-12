// The editor's words for a channel's guide match and its «почему» (the server sends codes).
import type { SourceChannel } from '../../../api/iptv'
import type { TFunction } from '../../../i18n'

export const epgLabel = (t: TFunction, c: SourceChannel) => (c.epg.manual ? t('iptv.how.manual') : c.epg.how ? t(`iptv.how.${c.epg.how}`) : t('iptv.how.none'))
export const whyLabel = (t: TFunction, c: SourceChannel) => (c.why ? t(`iptv.why.${c.why}`) : c.whyText)
