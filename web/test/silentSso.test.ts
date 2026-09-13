// Runs on Node's built-in runner with type stripping (Node 22.18+ / 24):
//   npm test
// It sits outside src/ so tsc, which has no Node types, does not see it, and
// so that vite never bundles it.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  ATTEMPTED_KEY, SIGNED_OUT_KEY, beginSilentSso, clearSilentSsoState, markSignedOut,
  readFlag, shouldAttemptSilentSso, silentSsoAllowedOn, silentSsoStartURL,
} from '../src/silentSso.ts'
import type { FlagStore } from '../src/silentSso.ts'
import type { AuthStatus } from '../src/types.ts'

const version = { name: 'Relio', version: 't', gitCommit: 't', buildDate: 't', edition: 'Community' }
const on: AuthStatus = { localLoginEnabled: true, sso: { enabled: true, autoLogin: true }, version }
const off: AuthStatus = { localLoginEnabled: true, sso: { enabled: true, autoLogin: false }, version }
const page = (pathname: string, search = '') => ({ pathname, search })

function memory(): { store: () => FlagStore; data: Map<string, string> } {
  const data = new Map<string, string>()
  const store: FlagStore = {
    getItem: key => data.get(key) ?? null,
    setItem: (key, value) => { data.set(key, value) },
    removeItem: key => { data.delete(key) },
  }
  return { store: () => store, data }
}

// Private modes throw on the property access itself, not only on the call.
const unreadable = (): FlagStore => { throw new Error('SecurityError: sessionStorage is blocked') }

test('a default installation never tries: auto login is off', () => {
  const { store } = memory()
  assert.equal(shouldAttemptSilentSso(off, page('/app/dashboard'), store), false)
  assert.equal(shouldAttemptSilentSso({ ...off, sso: { enabled: false, autoLogin: true } }, page('/app/dashboard'), store), false)
  assert.equal(shouldAttemptSilentSso(null, page('/app/dashboard'), store), false)
})

test('with auto login on, a fresh tab on an application path tries once', () => {
  const { store, data } = memory()
  assert.equal(shouldAttemptSilentSso(on, page('/app/customers/42', '?tab=plan'), store), true)
  const visited: string[] = []
  assert.equal(beginSilentSso('/app/customers/42?tab=plan', store, url => visited.push(url)), true)
  assert.deepEqual(visited, ['/api/v1/auth/oidc/start?prompt=none&return_to=%2Fapp%2Fcustomers%2F42%3Ftab%3Dplan'])
  assert.equal(data.get(ATTEMPTED_KEY), 'true')
  // The flag is written before the browser leaves, so a reload after the
  // refusal does not go around again.
  assert.equal(shouldAttemptSilentSso(on, page('/app/customers/42'), store), false)
})

test('the refusal marker in the address stops a retry even with empty storage', () => {
  const { store } = memory()
  assert.equal(shouldAttemptSilentSso(on, page('/login', '?sso=none'), store), false)
  // The marker matters on its own, not only because /login is excluded.
  assert.equal(shouldAttemptSilentSso(on, page('/app/dashboard', '?sso=none'), store), false)
  assert.equal(shouldAttemptSilentSso(on, page('/app/dashboard', '?sso_error=not_provisioned'), store), false)
})

test('the login, callback, API, MCP and discovery paths never start an attempt', () => {
  const { store } = memory()
  for (const path of ['/login', '/login/', '/api/v1/auth/oidc/callback', '/api', '/mcp', '/mcp/', '/.well-known/openid-configuration']) {
    assert.equal(silentSsoAllowedOn(path), false, path)
    assert.equal(shouldAttemptSilentSso(on, page(path), store), false, path)
  }
  for (const path of ['/', '/app', '/app/dashboard', '/admin/oidc', '/me/keys', '/loginhistory']) {
    assert.equal(silentSsoAllowedOn(path), true, path)
  }
})

test('signing out suppresses auto login until a session exists again', () => {
  const { store, data } = memory()
  markSignedOut(store)
  assert.equal(data.get(SIGNED_OUT_KEY), 'true')
  assert.equal(shouldAttemptSilentSso(on, page('/app/dashboard'), store), false)
  clearSilentSsoState(store)
  assert.equal(data.has(SIGNED_OUT_KEY), false)
  assert.equal(data.has(ATTEMPTED_KEY), false)
  assert.equal(shouldAttemptSilentSso(on, page('/app/dashboard'), store), true)
})

test('unreadable storage counts as already attempted', () => {
  assert.equal(readFlag(ATTEMPTED_KEY, unreadable), true)
  assert.equal(shouldAttemptSilentSso(on, page('/app/dashboard'), unreadable), false)
  // And even if the decision were forced, leaving without a remembered
  // attempt is refused.
  const visited: string[] = []
  assert.equal(beginSilentSso('/app/dashboard', unreadable, url => visited.push(url)), false)
  assert.deepEqual(visited, [])
  assert.doesNotThrow(() => markSignedOut(unreadable))
  assert.doesNotThrow(() => clearSilentSsoState(unreadable))
})

test('a write that does not stick also refuses to leave', () => {
  const silent: FlagStore = { getItem: () => null, setItem: () => {}, removeItem: () => {} }
  const visited: string[] = []
  assert.equal(beginSilentSso('/app/dashboard', () => silent, url => visited.push(url)), false)
  assert.deepEqual(visited, [])
})

test('return_to only carries a same-origin path', () => {
  assert.equal(silentSsoStartURL('/app/opportunities/7'), '/api/v1/auth/oidc/start?prompt=none&return_to=%2Fapp%2Fopportunities%2F7')
  for (const outside of ['//evil.example/app', 'https://evil.example', 'app/dashboard', '']) {
    assert.equal(silentSsoStartURL(outside), '/api/v1/auth/oidc/start?prompt=none&return_to=%2Fapp%2Fdashboard', outside)
  }
})
