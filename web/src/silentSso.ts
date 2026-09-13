import type { AuthStatus } from './types'

// Silent SSO: when the administrator has turned auto login on, a visitor who
// already holds a Keycloak session is sent through prompt=none before the
// login screen is drawn. prompt=none never renders anything — the provider
// either answers with a code at once or comes back with login_required. The
// second answer is ordinary, and retrying on it would bounce the browser
// between the provider and Relio forever. Everything in this file exists to
// make sure that retry never happens.
//
// The decision is kept free of DOM globals so it can be tested with Node.

// sessionStorage rather than localStorage: an attempt is scoped to this tab's
// browsing session, so a fresh tab tries again while a reload after a refusal
// does not.
export const ATTEMPTED_KEY = 'relio.sso.silentAttempted'
export const SIGNED_OUT_KEY = 'relio.sso.signedOut'

export type FlagStore = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>

// Storage is resolved lazily: in private modes and with site data blocked the
// property access itself throws.
const sessionStore = (): FlagStore => window.sessionStorage

export function readFlag(key: string, storage: () => FlagStore = sessionStore): boolean {
  try {
    return storage().getItem(key) === 'true'
  } catch {
    // Unreadable storage counts as "already attempted". Reading it as "not
    // yet" is exactly the loop this file prevents, so failure goes the
    // blocking way.
    return true
  }
}

export function writeFlag(key: string, value: boolean, storage: () => FlagStore = sessionStore) {
  try {
    if (value) storage().setItem(key, 'true')
    else storage().removeItem(key)
  } catch {
    /* the read side already fails closed */
  }
}

/** Records that the user signed out on purpose, which suppresses auto login. */
export function markSignedOut(storage: () => FlagStore = sessionStore) {
  writeFlag(SIGNED_OUT_KEY, true, storage)
  writeFlag(ATTEMPTED_KEY, true, storage)
}

/** Lifts the suppression once a session exists again. */
export function clearSilentSsoState(storage: () => FlagStore = sessionStore) {
  writeFlag(SIGNED_OUT_KEY, false, storage)
  writeFlag(ATTEMPTED_KEY, false, storage)
}

/**
 * Paths on which a silent attempt must never start. The login, callback and
 * error screens are where loops come from; the API, MCP and discovery paths
 * belong to machines, not to a browser navigation.
 */
export function silentSsoAllowedOn(pathname: string): boolean {
  const under = (prefix: string) => pathname === prefix || pathname.startsWith(prefix + '/')
  if (under('/login') || under('/api') || under('/mcp') || pathname.startsWith('/.well-known/')) return false
  return true
}

export type PageLocation = { pathname: string; search: string }

/**
 * Decides whether to try signing in without showing a login screen. Every
 * guard here is a separate layer: the setting, the path, the marker the
 * callback leaves in the address, the sign-out suppression, and the
 * once-per-tab-session flag.
 */
export function shouldAttemptSilentSso(status: AuthStatus | null | undefined, page: PageLocation, storage: () => FlagStore = sessionStore): boolean {
  if (!status?.sso?.enabled || !status.sso.autoLogin) return false
  if (!silentSsoAllowedOn(page.pathname)) return false
  // The callback lands on /login?sso=none when the provider had no session
  // and on /login?sso_error=… when something failed. Either marker means a
  // refusal is being shown right now, even if sessionStorage was cleared.
  const params = new URLSearchParams(page.search)
  if (params.has('sso') || params.has('sso_error')) return false
  if (readFlag(SIGNED_OUT_KEY, storage)) return false
  if (readFlag(ATTEMPTED_KEY, storage)) return false
  return true
}

/** The same-origin path a login returns to; anything else goes to the dashboard. */
export function safeReturnTo(returnTo: string): string {
  return returnTo.startsWith('/') && !returnTo.startsWith('//') ? returnTo : '/app/dashboard'
}

export function silentSsoStartURL(returnTo: string): string {
  return `/api/v1/auth/oidc/start?prompt=none&return_to=${encodeURIComponent(safeReturnTo(returnTo))}`
}

/**
 * Sends the browser to the provider for a silent attempt as a top-level
 * navigation — not a hidden iframe, so it works with third-party cookies
 * blocked and without caring whether the provider allows framing.
 *
 * Returns false, without navigating, when the attempted flag could not be
 * confirmed in storage: an attempt that cannot be remembered would be
 * repeated on the next load.
 */
export function beginSilentSso(returnTo: string, storage: () => FlagStore = sessionStore, go: (url: string) => void = url => window.location.assign(url)): boolean {
  try {
    storage().setItem(ATTEMPTED_KEY, 'true')
    if (storage().getItem(ATTEMPTED_KEY) !== 'true') return false
  } catch {
    return false
  }
  go(silentSsoStartURL(returnTo))
  return true
}
