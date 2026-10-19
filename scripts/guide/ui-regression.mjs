#!/usr/bin/env node
// Real browser regression against an explicitly disposable deployment only.
// Uses scripts/guide's pinned puppeteer-core. Credentials are never captured.
// RELIO_GUIDE_URL=... RELIO_GUIDE_DISPOSABLE=1 RELIO_GUIDE_ADMIN=...
// RELIO_GUIDE_PASSWORD=... [RELIO_GUIDE_NEW_PASSWORD=...] node ui-regression.mjs
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import puppeteer from 'puppeteer-core'

const base = process.env.RELIO_GUIDE_URL?.replace(/\/$/, '')
assert.ok(base && process.env.RELIO_GUIDE_DISPOSABLE === '1', 'An explicitly disposable Relio deployment is required')
const cookies = new Map()
let csrf = ''
async function api(route, method = 'GET', body) {
  const response = await fetch(base + route, { method, headers: {
    'Content-Type': 'application/json', 'X-CSRF-Token': csrf,
    Cookie: [...cookies].map(([k, v]) => `${k}=${v}`).join('; '),
  }, body: body === undefined ? undefined : JSON.stringify(body) })
  for (const line of response.headers.getSetCookie()) {
    const pair = line.split(';')[0], i = pair.indexOf('=')
    cookies.set(pair.slice(0, i), pair.slice(i + 1))
  }
  const text = await response.text()
  assert.ok(response.ok, `${method} ${route}: ${response.status} ${text.slice(0, 200)}`)
  return text ? JSON.parse(text) : null
}
const login = await api('/api/v1/auth/login', 'POST', { username: process.env.RELIO_GUIDE_ADMIN, password: process.env.RELIO_GUIDE_PASSWORD })
csrf = login.user.csrfToken
if (login.user.mustChangePassword) {
  assert.ok(process.env.RELIO_GUIDE_NEW_PASSWORD, 'First login needs RELIO_GUIDE_NEW_PASSWORD')
  await api('/api/v1/me/password', 'POST', { currentPassword: process.env.RELIO_GUIDE_PASSWORD, newPassword: process.env.RELIO_GUIDE_NEW_PASSWORD })
}

const prefix = `UI검증${Date.now()}`
const customers = []
for (let i = 1; i <= 31; i++) customers.push(await api('/api/v1/customers', 'POST', { name: `${prefix} ${i}`, customerType: 'CUSTOMER' }))
const pipeline = (await api('/api/v1/pipeline')).items[0]
for (const amount of [900, 20000, 1000]) await api('/api/v1/opportunities', 'POST', {
  name: `${prefix} 금액 ${amount}`, customerId: customers[0].id, stageId: pipeline.stages[0].id, expectedAmount: amount,
})
// These are artificial test codes, never operational department defaults.
const org = await api('/api/v1/admin/organizations', 'POST', { name: `${prefix} 부서`, code: `UI_${Date.now()}`, type: 'DEPARTMENT' })
await api('/api/v1/admin/voice-presets/MEMBER_SUPPORT/apply', 'POST', {
  organizationId: org.id, customerCodeUniqueScope: 'WORKSPACE', customerCodePattern: '',
  categories: Array.from({ length: 8 }, (_, i) => ({ code: `TEST_${i}`, name: `검증 유형 ${i}`, voiceType: 'INQUIRY' })),
  serviceType: { required: true, defaultValue: '', values: [{ code: 'TEST', name: '검증 서비스' }] },
  developmentMethod: { required: false, defaultValue: 'TEST', values: [{ code: 'TEST', name: '검증 방식' }] },
  errorCode: { required: false, defaultValue: '', values: [] },
  resolver: { required: true, defaultValue: '', values: Array.from({ length: 4 }, (_, i) => ({ code: `TEST_${i}`, name: `검증 주체 ${i}` })) },
  agentUserIds: [], reviewerUserIds: [],
})
const workspace = (await api('/api/v1/voices/workspaces')).items.find(w => w.code === 'MEMBER_SUPPORT')
const member = await api(`/api/v1/voices/workspaces/${workspace.id}/customers`, 'POST', { name: `${prefix} 회원사`, customerCode: prefix })
const voice = await api('/api/v1/voices', 'POST', { customerId: member.id, categoryId: workspace.categories[0].id,
  title: `${prefix} 접수`, customFields: { service_type: 'TEST', error_code: 'B124' },
})

const browser = await puppeteer.launch({ executablePath: process.env.RELIO_GUIDE_CHROME || '/usr/bin/google-chrome', headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] })
const errors = []
const screenshots = process.env.RELIO_UI_SCREENSHOTS || '/tmp/relio-ui-review'
await mkdir(screenshots, { recursive: true })
const settings = (await api('/api/v1/admin/settings?namespace=api')).items
const rateLimit = settings.find(s => s.key === 'rate_limit_per_minute')
if (rateLimit) await api('/api/v1/admin/settings/api/rate_limit_per_minute', 'PUT', { value: 0, valueType: rateLimit.valueType })
try {
  const page = await browser.newPage()
  page.on('pageerror', e => errors.push(e.message))
  await page.setViewport({ width: 1440, height: 1000 })
  await page.setCookie(...[...cookies].map(([name, value]) => ({ name, value, url: base })))
  const goto = async route => { console.log(`Checking ${route.split('?')[0]}`); await page.goto(base + route); await page.waitForSelector('.data-table tbody tr') }
  const clickText = async (selector, text) => {
    const elements = await page.$$(selector)
    for (const element of elements) if ((await element.evaluate(e => e.textContent)).includes(text)) { await element.click(); return }
    throw Error(`Missing ${selector}: ${text}`)
  }
  const names = () => page.$$eval('.data-table tbody tr', rows => rows.map(r => r.cells[1].querySelector('b')?.textContent))
  await goto(`/app/customers?q=${encodeURIComponent(prefix)}`)
  await page.click('button[aria-label="고객 정렬 변경"]')
  await page.waitForFunction(() => document.querySelector('th[aria-sort="ascending"]'))
  assert.equal((await names())[0], `${prefix} 1`)
  assert.equal((await names()).length, 25)
  await page.click('button[aria-label="다음 페이지"]')
  assert.equal((await names())[0], `${prefix} 26`)
  await page.select('select[aria-label="고객 페이지당 표시 건수"]', '50')
  assert.equal((await names()).length, 32) // Includes the workspace member visible to the bootstrap admin.
  await page.click('button[aria-label="고객 정렬 변경"]')
  assert.match((await page.$eval('.data-table-order', e => e.textContent)), /내림차순/)
  await page.click('.table-reset')
  assert.equal(await page.$$eval('th[aria-sort="ascending"],th[aria-sort="descending"]', els => els.length), 0)
  await page.click('.table-density')
  assert.equal(await page.$eval('.table-density', e => e.getAttribute('aria-pressed')), 'true')
  const before = page.url()
  await page.click('.data-table tbody tr .row-menu button')
  await page.waitForSelector('[role="dialog"]')
  assert.equal(page.url(), before, 'Edit must not also open customer 360')
  await page.keyboard.press('Escape')
  await page.focus('.data-table tbody tr .row-menu button')
  await page.keyboard.press('Enter')
  await page.waitForSelector('[role="dialog"]')
  assert.equal(page.url(), before, 'Keyboard edit must not also open customer 360')
  await page.keyboard.press('Escape')
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.screenshot({ path: `${screenshots}/customers-desktop.png` })
  const resetRequest = page.waitForRequest(r => r.url().includes('/api/v1/customers?') && new URL(r.url()).searchParams.get('q') === '')
  await clickText('.toolbar button', '필터 초기화')
  await resetRequest
  await goto(`/app/opportunities?q=${encodeURIComponent(prefix)}`)
  await page.click('button[aria-label="거래 금액 · 기준금액 정렬 변경"]')
  const amounts = await page.$$eval('.data-table tbody tr', rows => rows.map(r => Number(r.cells[3].querySelector('b').textContent.replace(/[^\d]/g, ''))))
  assert.deepEqual(amounts, [...amounts].sort((a, b) => b - a))
  assert.equal(await page.$eval('.data-table tbody tr td:nth-child(4)', e => getComputedStyle(e).textAlign), 'right')
  await page.screenshot({ path: `${screenshots}/opportunities-desktop.png` })

  await goto('/app/voices')
  await page.type('input[aria-label="고객의 목소리에서 검색"]', prefix)
  await page.click('.data-table tbody tr')
  await page.waitForSelector('form input[name="field.error_code"]')
  await page.waitForFunction(() => document.querySelector('.drawer').getBoundingClientRect().right <= innerWidth + 1)
  assert.equal(await page.$('form select[name="field.resolver"]'), null, 'Intake editing must not require resolution values')
  await page.$eval('input[name="field.error_code"]', e => { e.value = '' })
  const saved = page.waitForResponse(r => r.url().endsWith(`/api/v1/voices/${voice.id}`) && r.request().method() === 'PUT')
  await clickText('.drawer form button', '항목 저장')
  assert.equal((await saved).status(), 200)
  const actual = (await api(`/api/v1/voices/${voice.id}`)).voice
  assert.equal(actual.customFields.error_code, null)
  assert.equal(actual.customFields.service_type, 'TEST')
  await page.screenshot({ path: `${screenshots}/voice-detail-desktop.png` })
  await page.keyboard.press('Escape')
  await goto('/app/intelligence')
  const gaps = await page.$$eval('.intelligence-table-head', els => els.map(e => {
    const title = e.querySelector('.panel-title-row'), description = e.querySelector('p')
    return title && description ? description.getBoundingClientRect().top - title.getBoundingClientRect().bottom : 99
  }))
  assert.ok(gaps.length >= 2 && gaps.every(g => g >= 8), `Heading gaps too small: ${gaps}`)
  await page.screenshot({ path: `${screenshots}/intelligence-desktop.png` })

  for (const route of ['/app/customers', '/app/voices', '/admin/users', '/admin/custom-fields', '/admin/voice-categories', '/me/keys']) {
    console.log(`Checking mobile ${route}`)
    await page.setViewport({ width: 390, height: 844 })
    await page.goto(base + route)
    await page.waitForSelector('.page-heading')
    await page.waitForFunction(() => !document.querySelector('.spinner') && (document.querySelector('.data-table') || document.querySelector('.key-security')))
    const overflow = await page.evaluate(() => ({ page: document.documentElement.scrollWidth, viewport: innerWidth }))
    assert.ok(overflow.page <= overflow.viewport + 1, `${route} leaks horizontal scroll: ${JSON.stringify(overflow)}`)
    const toolbarOverlap = await page.evaluate(() => {
      const toolbar = document.querySelector('.toolbar'), next = toolbar?.nextElementSibling
      return toolbar && next ? toolbar.getBoundingClientRect().bottom - next.getBoundingClientRect().top : 0
    })
    assert.ok(toolbarOverlap <= 1, `${route} toolbar overlaps following content by ${toolbarOverlap}px`)
    const buttonsFit = await page.$$eval('.settings-title .heading-actions button', buttons => buttons.every(b => b.getBoundingClientRect().width >= 80 && b.getBoundingClientRect().height < 100))
    assert.ok(buttonsFit, `${route} has squeezed vertical action buttons`)
    await page.click('.mobile-menu-btn')
    await page.waitForSelector('.sidebar-open')
    assert.equal(await page.$eval('.sidebar', e => e.inert), false)
    await page.$$eval('.sidebar a[href],.sidebar button', controls => controls.filter(e => e.offsetParent !== null).at(-1).focus())
    await page.keyboard.press('Tab')
    assert.equal(await page.$eval('.sidebar', e => e.contains(document.activeElement)), true, 'Tab stays inside the mobile menu')
    await page.keyboard.press('Escape')
    await page.waitForSelector('.sidebar[inert]')
    assert.equal(await page.$eval('.mobile-menu-btn', e => e === document.activeElement), true, 'Menu close restores focus')
    await page.waitForFunction(() => document.querySelector('.sidebar').getBoundingClientRect().right < 0)
    await page.screenshot({ path: `${screenshots}/${route.split('/').pop()}-mobile.png` })
  }
  // A failed request must offer a retry instead of an endless spinner.
  await page.setRequestInterception(true)
  page.on('request', r => r.url().includes('/api/v1/voices?') ? r.respond({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'UI test unavailable' }) }) : r.continue())
  await page.goto(base + '/app/voices')
  await page.waitForSelector('.list-load-error button')
  assert.equal(await page.$('.spinner'), null)
  assert.deepEqual(errors, [], 'No browser runtime errors')
  console.log(`UI regression passed: sorting, pagination, numeric alignment, nested controls, filter reset, VOC field clearing, heading spacing, mobile layouts and retry. Screenshots: ${screenshots}`)
} finally {
  await browser.close()
  if (rateLimit) await api('/api/v1/admin/settings/api/rate_limit_per_minute', 'PUT', { value: rateLimit.value, valueType: rateLimit.valueType })
}
