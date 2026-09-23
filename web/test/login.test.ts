import { after, test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, rm } from 'node:fs/promises'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { build } from 'esbuild'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'

// Compile the real component and its imports; keep React shared with the renderer.
const directory = await mkdtemp(fileURLToPath(new URL('./.login-test-', import.meta.url)))
after(() => rm(directory, { recursive: true, force: true }))
const outfile = `${directory}/Login.mjs`
await build({
  entryPoints: [fileURLToPath(new URL('../src/pages/Login.tsx', import.meta.url))],
  outfile, bundle: true, platform: 'node', format: 'esm', packages: 'external', jsx: 'automatic',
})
const { default: Login } = await import(pathToFileURL(outfile).href)
const version = { name: 'Relio', version: 'test', gitCommit: 'test', buildDate: 'test', edition: 'Community' }

for (const autoLogin of [false, true]) {
  for (const scenario of ['storage access blocked', 'getItem blocked', 'invalid URL', 'empty', 'disallowed path', 'remembered path']) {
    test(`SSO login renders with ${scenario}, autoLogin=${autoLogin}`, t => {
      const target = scenario === 'invalid URL' ? 'http://['
        : scenario === 'disallowed path' ? '/me/password'
        : scenario === 'remembered path' ? '/app/customers/42?tab=plan' : null
      t.mock.getter(globalThis, 'sessionStorage', () => {
        if (scenario === 'storage access blocked') throw new DOMException('Storage blocked', 'SecurityError')
        return { getItem(key: string) {
          assert.equal(key, 'relio.returnTo')
          if (scenario === 'getItem blocked') throw new DOMException('Storage blocked', 'SecurityError')
          return target
        } }
      })
      const html = renderToStaticMarkup(createElement(Login, {
        status: { localLoginEnabled: true, sso: { enabled: true, autoLogin }, version },
        version, onLogin() {}, notify() {},
      }))
      const expected = scenario === 'remembered path' ? target! : '/app/dashboard'
      assert.ok(html.includes(`href="/api/v1/auth/oidc/start?return_to=${encodeURIComponent(expected)}"`))
      // With SSO on, the organisation account is the way in and the local
      // administrator waits behind a collapsed disclosure (v1.12.0).
      assert.ok(html.includes('조직 계정으로 SSO 로그인'))
      assert.ok(html.includes('aria-controls="local-login-form"') && html.includes('aria-expanded="false"'))
      assert.ok(!html.includes('id="local-login-form"'))
    })
  }
}

// Browser globals used by the actual render path, restored after this test file.
const originals = new Map(['location', 'sessionStorage'].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]))
Object.defineProperty(globalThis, 'location', { configurable: true, value: { search: '', origin: 'https://relio.example' } })
Object.defineProperty(globalThis, 'sessionStorage', { configurable: true, get: () => undefined })
after(() => {
  for (const [key, descriptor] of originals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})
