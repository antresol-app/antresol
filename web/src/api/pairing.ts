// Sign-in on a TV by QR code: the signed-in side (the page /pair?code=…).
// The TV's own calls (start, poll) are in the apps, not here.
import { request } from './client'

export interface PairingRequest {
  code: string
  device: { name: string; model: string; version: string }
  /** the TV asked from outside the home network */
  remote: boolean
  createdAt: string
  expiresAt: string
}

export const lookupPairing = (code: string) => request<PairingRequest>('POST', '/api/auth/pair/lookup', { code })
/** userId: the profile the TV signs in to (an admin may pick any; 0 or none: the caller's). */
export const approvePairing = (code: string, userId?: number) =>
  request<{ ok: true; userId: number }>('POST', '/api/auth/pair/approve', { code, userId: userId ?? 0 })
export const denyPairing = (code: string) => request<void>('POST', '/api/auth/pair/deny', { code })
