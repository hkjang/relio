#!/usr/bin/env node
// Destructive fixtures are permitted ONLY on an explicitly disposable deployment.
// Uses the same RELIO_GUIDE_* environment contract as ui-regression.mjs.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import puppeteer from 'puppeteer-core'
import { adminNav } from '../../web/src/nav.ts'

const base = process.env.RELIO_GUIDE_URL?.replace(/\/$/, '')
assert.ok(base && process.env.RELIO_GUIDE_DISPOSABLE === '1', 'An explicitly disposable deployment is required')
function client() {
  const cookies = new Map(); let csrf = ''
  async function request(route, method = 'GET', body, expected = 200) {
    const response = await fetch(base + route, { method, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf, Cookie: [...cookies].map(([k, v]) => `${k}=${v}`).join('; ') }, body: body === undefined ? undefined : JSON.stringify(body) })
    for (const line of response.headers.getSetCookie()) { const pair = line.split(';')[0], i = pair.indexOf('='); cookies.set(pair.slice(0, i), pair.slice(i + 1)) }
    const text = await response.text()
    assert.ok(expected === 200 ? response.ok : response.status === expected, `${method} ${route}: ${response.status} ${text.slice(0, 200)}`)
    const value = text ? JSON.parse(text) : null
    if (route === '/api/v1/auth/login' && value?.user) csrf = value.user.csrfToken
    return value
  }
  return { request, cookies }
}
const admin = client(), api = admin.request
const login = await api('/api/v1/auth/login', 'POST', { username: process.env.RELIO_GUIDE_ADMIN, password: process.env.RELIO_GUIDE_PASSWORD })
if (login.user.mustChangePassword) {
  assert.ok(process.env.RELIO_GUIDE_NEW_PASSWORD)
  await api('/api/v1/me/password', 'POST', { currentPassword: process.env.RELIO_GUIDE_PASSWORD, newPassword: process.env.RELIO_GUIDE_NEW_PASSWORD })
}
const rate = (await api('/api/v1/admin/settings?namespace=api')).items.find(x => x.key === 'rate_limit_per_minute')
await api('/api/v1/admin/settings/api/rate_limit_per_minute', 'PUT', { value: 0, valueType: 'number' })
const browser = await puppeteer.launch({ executablePath: process.env.RELIO_GUIDE_CHROME || '/usr/bin/google-chrome', headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] })
const shots = process.env.RELIO_UI_SCREENSHOTS || '/tmp/relio-admin-review'
await mkdir(shots, { recursive: true })
const errors = [], overflows = []
let activePage
try {
  const fixture = `Admin UI ${Date.now()}`
  for (const priority of [20, 3, 100]) await api('/api/v1/admin/approval-policies', 'POST', { name: `${fixture} ${priority}`, entityType: 'OPPORTUNITY', conditionField: 'expected_amount', conditionOperator: 'GTE', conditionValue: 100000, approverMethod: 'MANAGER', approvalSteps: 1, allowReject: true, allowResubmit: true, active: false, priority })
  await api('/api/v1/me/keys', 'POST', { name: fixture, channels: ['MCP'], scopes: ['mcp:use', 'customer:read'] })
  await api('/api/v1/admin/analytics', 'POST', { provider: 'MATOMO', name: fixture, enabled: false, siteId: '1', scriptOrigin: 'https://analytics.test.invalid', scriptPath: '/matomo.js', collectOrigins: [], scriptAttributes: {}, respectDnt: true, authenticatedOnly: true, sameOriginProxy: false, displayOrder: 100 })
  const page = await browser.newPage()
  activePage = page
  page.on('pageerror', e => errors.push(e.message))
  await page.setCookie(...[...admin.cookies].map(([name, value]) => ({ name, value, url: base })))
  await page.setViewport({ width: 1440, height: 1000 })
  const goto = async route => { await page.goto(base + route, { waitUntil: 'networkidle0' }); await page.waitForSelector('.shell-admin .page-heading') }
  const input = async (selector, value) => { await page.click(selector, { clickCount: 3 }); await page.keyboard.press('Backspace'); await page.type(selector, value) }
  const clickText = async (selector, text) => {
    for (const element of await page.$$(selector)) if ((await element.evaluate(e => e.textContent)).includes(text)) { await element.click(); return }
    throw Error(`Missing ${selector}: ${text}`)
  }
  for (const width of process.env.RELIO_ADMIN_SKIP_ROUTES ? [] : [1440, 390]) {
    await page.setViewport({ width, height: 1000 })
    for (const menu of adminNav) {
      await goto(menu.to)
      assert.equal(await page.$$('.spinner-wrap').then(x => x.length), 0, `${menu.to} never loaded`)
      const size = await page.evaluate(() => ({ full: document.documentElement.scrollWidth, view: innerWidth }))
      if (size.full > size.view + 1) overflows.push({ route: menu.to, width, ...size })
      if (['overview', 'system', 'roles', 'keys', 'voice-categories', 'sales-execution'].includes(menu.to.split('/').pop())) await page.screenshot({ path: `${shots}/${menu.to.split('/').pop()}-${width}.png`, fullPage: true })
      console.log(`Rendered ${width}px ${menu.to}`)
    }
  }
  assert.deepEqual(overflows, [], 'Admin pages must scroll inside tables, not overflow the viewport')
  await page.setViewport({ width: 1440, height: 1000 })
  await goto('/admin/approval')
  await input('input[aria-label="승인 정책에서 검색"]', fixture)
  await page.click('button[aria-label="우선순위 정렬 변경"]')
  assert.deepEqual(await page.$$eval('.data-table tbody tr', rows => rows.map(row => Number(row.cells[5].textContent))), [3, 20, 100])
  await goto('/admin/keys')
  await input('input[aria-label="연동 키에서 검색"]', fixture)
  assert.equal(await page.$$('.data-table tbody tr').then(rows => rows.length), 1)
  await goto('/admin/analytics')
  await input('input[aria-label="방문자 분석 공급자에서 검색"]', fixture)
  assert.equal(await page.$$('.data-table tbody tr').then(rows => rows.length), 1)
  console.log('Checking menu discovery and settings failure recovery')
  await page.setViewport({ width: 1440, height: 1000 })
  await goto('/admin/overview')
  assert.equal(await page.$$('.admin-menu-card').then(x => x.length), 19)
  await input('input[aria-label="전체 관리 메뉴 검색"]', 'MCP 키')
  assert.equal(await page.$$('.admin-menu-card').then(x => x.length), 1)
  assert.equal(await page.$eval('.admin-menu-card', e => e.getAttribute('href')), '/admin/keys')
  await input('input[aria-label="관리자 메뉴 검색"]', '기본 SSO')
  assert.equal(await page.$$('.side-nav a').then(x => x.length), 1)
  await page.click('button[aria-label="관리 메뉴 검색 초기화"]')

  // Failed GET must not expose default values or allow editing.
  let failGet = true, delayPut = false
  const writes = []
  await page.setRequestInterception(true)
  page.on('request', async request => {
    if (failGet && request.method() === 'GET' && request.url() === base + '/api/v1/admin/settings') {
      return request.respond({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { message: 'Admin test: unavailable' } }) })
    }
    if (request.method() === 'PUT' && request.url().includes('/admin/settings/')) {
      writes.push({ url: request.url(), body: JSON.parse(request.postData()) })
      if (delayPut) await new Promise(resolve => setTimeout(resolve, 200))
    }
    await request.continue()
  })
  await goto('/admin/system')
  await page.waitForSelector('.admin-load-error')
  assert.equal(await page.$$('input[name="name"]').then(x => x.length), 0)
  failGet = false
  await page.click('.admin-load-error button')
  await page.waitForSelector('input[name="name"]')
  const originalName = await page.$eval('input[name="name"]', e => e.value)
  assert.equal(await page.$eval('.settings-save-actions .btn-primary', e => e.disabled), true)
  await input('input[name="name"]', '관리 화면 검증')
  assert.equal(await page.$eval('.settings-save-actions .btn-primary', e => e.disabled), false)
  page.once('dialog', dialog => dialog.dismiss())
  await page.click('.side-nav a[href="/admin/security"]')
  assert.equal(new URL(page.url()).pathname, '/admin/system', 'Cancel preserves unsaved input')
  delayPut = true
  await page.evaluate(() => { const f = document.querySelector('.admin-settings-form'); f.requestSubmit(); f.requestSubmit() })
  await page.waitForFunction(() => document.querySelector('input[name="name"]')?.value === '관리 화면 검증' && document.querySelector('.settings-save-actions .btn-primary')?.disabled && !document.querySelector('.sticky-save.dirty'))
  assert.equal(writes.filter(x => x.url.endsWith('/system/service_name')).length, 1)
  assert.ok(writes.find(x => x.url.endsWith('/system/service_name')).body.version > 0)
  assert.equal((await api('/api/v1/admin/settings?namespace=system')).items.find(x => x.key === 'service_name').value, '관리 화면 검증')
  delayPut = false

  // A second administrator's write must win, never be silently overwritten.
  console.log('Checking concurrent administrator edits')
  await input('input[name="name"]', '저장하면 안 되는 오래된 입력')
  await api('/api/v1/admin/settings/system/service_name', 'PUT', { value: '다른 관리자 변경', valueType: 'string' })
  await page.click('.settings-save-actions .btn-primary')
  await page.waitForSelector('.settings-failure')
  assert.match(await page.$eval('.settings-failure', e => e.textContent), /다른|another|실패/)
  assert.equal(await page.$eval('input[name="name"]', e => e.value), '저장하면 안 되는 오래된 입력')
  assert.equal((await api('/api/v1/admin/settings?namespace=system')).items.find(x => x.key === 'service_name').value, '다른 관리자 변경')
  page.once('dialog', dialog => dialog.accept())
  await clickText('.settings-save-actions button', '저장된 값 다시 불러오기')
  await page.waitForFunction(() => document.querySelector('input[name="name"]')?.value === '다른 관리자 변경')
  await api('/api/v1/admin/settings/system/service_name', 'PUT', { value: originalName, valueType: 'string' })

  console.log('Checking repeat mail saves and secret preservation')
  await goto('/admin/mail')
  await page.evaluate(() => { window.__adminSubmits = 0; document.addEventListener('submit', () => window.__adminSubmits++, true) })
  await input('input[name="from_name"]', '관리 검증 발신자')
  await input('input[name="password"]', 'Disposable-Admin-Test-Secret')
  await page.locator('.settings-save-actions .btn-primary').click()
  await page.waitForFunction(() => document.querySelector('input[name="password"]')?.value === '' && !document.querySelector('.sticky-save.dirty'))
  assert.equal(await page.$eval('input[name="from_name"]', e => e.value), '관리 검증 발신자', 'Saving must not reset the form to OLD defaults')
  const mail = (await api('/api/v1/admin/settings?namespace=mail')).items
  assert.equal(mail.find(x => x.key === 'password').configured, true)
  assert.notEqual(mail.find(x => x.key === 'password').value, 'Disposable-Admin-Test-Secret')
  const secretVersion = mail.find(x => x.key === 'password').version
  await input('input[name="from_name"]', '관리 검증 발신자 2')
  await page.locator('.settings-save-actions .btn-primary').click()
  await page.waitForFunction(() => !document.querySelector('.sticky-save.dirty'))
  assert.equal(await page.evaluate(() => window.__adminSubmits), 2, 'The first success toast must not intercept the next save')
  assert.equal((await api('/api/v1/admin/settings?namespace=mail')).items.find(x => x.key === 'password').version, secretVersion)

  // SSO edits preserve the saved Secret and never test an unsaved connection.
  console.log('Checking SSO edits and permission selection')
  const cfg = await api('/api/v1/admin/oidc')
  await api('/api/v1/admin/oidc', 'PUT', { ...cfg, enabled: false, issuerUrl: 'https://sso.admin-test.invalid/realms/relio', clientId: 'admin-regression', clientSecret: 'Disposable-OIDC-Test-Secret', scopes: ['openid', 'profile', 'email'] })
  await goto('/admin/oidc')
  await input('input[name="clientId"]', 'admin-regression-updated')
  assert.equal(await page.$eval('.settings-save-actions .btn-secondary', e => e.disabled), true)
  await page.click('.settings-save-actions .btn-primary')
  await page.waitForFunction(() => !document.querySelector('.sticky-save.dirty'))
  assert.equal(await page.$eval('input[name="secret"]', e => e.value), '')
  assert.equal((await api('/api/v1/admin/oidc')).clientSecretConfigured, true)

  await goto('/admin/roles')
  await clickText('.heading-actions button', '권한 생성')
  await page.waitForSelector('input[aria-label="기능 권한 검색"]')
  await input('input[aria-label="기능 권한 검색"]', 'customer:read')
  assert.equal(await page.$$('.permission-picker .permission-grid input').then(x => x.length), 1)
  await page.click('.permission-picker .permission-grid input')
  await input('input[aria-label="기능 권한 검색"]', '존재하지않음')
  assert.match(await page.$eval('.permission-picker', e => e.textContent), /1개 선택/)
  await page.keyboard.press('Escape')

  // Real restricted identity, not a front-end-only permission mock.
  console.log('Checking actual read-only account')
  const suffix = Date.now(), read = client()
  const role = await api('/api/v1/admin/roles', 'POST', { code: `ADMIN_UI_READ_${suffix}`, name: 'UI 조회 관리자', dataScope: 'COMPANY', permissions: ['admin:read'] })
  await api('/api/v1/admin/users', 'POST', { username: `ui-read-${suffix}`, displayName: '조회 검증', password: 'Read-Only-Test-2026', roleIds: [role.id] })
  await read.request('/api/v1/auth/login', 'POST', { username: `ui-read-${suffix}`, password: 'Read-Only-Test-2026' })
  await read.request('/api/v1/me/password', 'POST', { currentPassword: 'Read-Only-Test-2026', newPassword: 'Read-Only-Changed-2026' })
  await read.request('/api/v1/admin/settings/system/service_name', 'PUT', { value: 'Denied', valueType: 'string' }, 403)
  const context = await browser.createBrowserContext(), readPage = await context.newPage()
  readPage.on('pageerror', e => errors.push(e.message))
  await readPage.setCookie(...[...read.cookies].map(([name, value]) => ({ name, value, url: base })))
  await readPage.goto(base + '/admin/system', { waitUntil: 'networkidle0' })
  assert.equal(await readPage.$eval('.settings-fields', e => e.disabled), true)
  assert.equal(await readPage.$eval('.settings-save-actions .btn-primary', e => e.disabled), true)
  await readPage.goto(base + '/admin/users', { waitUntil: 'networkidle0' })
  assert.equal(await readPage.$eval('.heading-actions .btn-primary', e => e.disabled), true)
  await readPage.goto(base + '/admin/voice-categories', { waitUntil: 'networkidle0' })
  assert.ok(await readPage.$$eval('.heading-actions button', buttons => buttons.every(b => b.disabled)))
  await context.close()
  assert.deepEqual(errors, [])
  console.log(`PASS: ${process.env.RELIO_ADMIN_SKIP_ROUTES ? 'targeted checks' : '19 admin routes × desktop/mobile'}, search, safe saves, conflicts, secrets, permission picker, and real read-only access`)
} catch (error) {
  if (activePage) {
    await activePage.screenshot({ path: `${shots}/failure.png`, fullPage: true })
    console.error('Page errors:', errors)
    console.error('Visible validation:', await activePage.$$eval('.settings-failure, .toast, input:invalid', nodes => nodes.map(e => e.validationMessage || e.textContent)))
    console.error('Save state:', await activePage.evaluate(() => ({ submits: window.__adminSubmits, busy: document.querySelector('.admin-settings-form')?.getAttribute('aria-busy'), buttonDisabled: document.querySelector('.settings-save-actions .btn-primary')?.disabled })))
  }
  throw error
} finally {
  await browser.close()
  if (rate) await api('/api/v1/admin/settings/api/rate_limit_per_minute', 'PUT', { value: rate.value, valueType: rate.valueType })
  else await api('/api/v1/admin/settings/api/rate_limit_per_minute', 'DELETE')
}
