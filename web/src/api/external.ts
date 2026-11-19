// «Внешние сервисы» (admin): the public services the server asks about the
// titles already in its library — Wikidata, the IMDb ratings, song words,
// the intro and credits databases — each on or off. A switch applies at once
// on the server; a service its environment sets (VAULT_*) is locked.
import { request } from './client'

export type ExternalServiceId = 'wikidata' | 'imdb' | 'lyrics' | 'segments'
export const EXTERNAL_SERVICES: ExternalServiceId[] = ['wikidata', 'imdb', 'lyrics', 'segments']

export interface ExternalService {
  id: ExternalServiceId
  /** Asked now. */
  on: boolean
  /** What it is when nobody chose. */
  default: boolean
  /** The server's environment sets it: the switch cannot change it. */
  locked: boolean
}

export interface ExternalView {
  services: ExternalService[]
  /** The admin has chosen (here or in the wizard). */
  chosen: boolean
  /** Show «Состояние»'s one-time notice: never chosen, never closed. */
  notice: boolean
}

export const getExternal = (signal?: AbortSignal) => request<ExternalView>('GET', '/api/settings/external', undefined, signal)
export const saveExternal = (body: { services?: Partial<Record<ExternalServiceId, boolean>>; dismissNotice?: boolean }) =>
  request<ExternalView>('PUT', '/api/settings/external', body)

/** Where «Внешние сервисы» is: the notices link here. */
export const EXTERNAL_PATH = '/manage/settings/external'
