// Where the channels' management lives. An admin manages the server's
// channels in «Настройки» (/manage/channels, drawn in its frame like any
// other section: the settings are apart from the app's own pages); a
// profile that is not an admin manages its personal channels from «ТВ»
// (/tv/manage). The same pages serve both: under /manage/ they drop their
// own page frame and take the settings' heading.
import { useLocation } from 'react-router'
import m from '../manage/Manage.module.css'

/** The channels' management of the settings (admins). */
export const MANAGE_CHANNELS = '/manage/channels'
/** The channels' management from «ТВ» (a profile's own channels). */
export const TV_MANAGE = '/tv/manage'

/** Where «Управление каналами» leads this profile. */
export const channelsManagePath = (isAdmin: boolean | undefined) => (isAdmin ? MANAGE_CHANNELS : TV_MANAGE)

/**
 * The page's place: `base` its links start from, `embedded` inside the
 * settings' frame; `page`/`column`/`title`/`header` pick the frame's class
 * or the page's own ('' drops it).
 */
export function useChannelsFrame() {
  const { pathname } = useLocation()
  const embedded = pathname.startsWith(`${MANAGE_CHANNELS}`)
  const base = embedded ? MANAGE_CHANNELS : TV_MANAGE
  return {
    base,
    embedded,
    page: (own: string | undefined) => (embedded ? '' : own),
    column: (own: string | undefined) => (embedded ? '' : own),
    title: (own: string | undefined) => (embedded ? m.title : own),
    header: (own: string | undefined) => (embedded ? m.header : own),
  }
}
