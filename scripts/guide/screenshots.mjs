#!/usr/bin/env node
// Captures the screens that docs/USER_GUIDE.md and docs/ADMIN_GUIDE.md embed.
//
// It logs in to a *disposable* Relio deployment, fills it with obviously fake
// demo data through the public REST API (find-or-create, so re-running is
// safe) and photographs each screen at 1440x900 with headless Chrome. It never
// changes a global setting, so there is nothing to restore afterwards.
//
//   cd scripts/guide && npm install --no-audit --no-fund
//   RELIO_GUIDE_URL=http://127.0.0.1:18080 RELIO_GUIDE_DISPOSABLE=1 \
//   RELIO_GUIDE_ADMIN=admin RELIO_GUIDE_PASSWORD=... [RELIO_GUIDE_NEW_PASSWORD=...] \
//   [RELIO_GUIDE_SEED_PASSWORD=...] \
//   node screenshots.mjs [--out ../../docs/assets/guide] [--only login,dashboard]
//
// Every credential comes from its own environment variable; nothing is written
// in this file. RELIO_GUIDE_NEW_PASSWORD is only needed when the account still
// has to change its bootstrap password on first login; RELIO_GUIDE_SEED_PASSWORD
// is the password given to the demo colleagues (hong, kim, lee) and, when
// absent, they are simply not created.
import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import puppeteer from 'puppeteer-core'

const here = path.dirname(fileURLToPath(import.meta.url))
const args = process.argv.slice(2)
const flag = (name, fallback) => { const i = args.indexOf(name); return i >= 0 ? args[i + 1] : fallback }
const outDir = path.resolve(here, flag('--out', '../../docs/assets/guide'))
const only = flag('--only', '') ? new Set(flag('--only', '').split(',')) : null

function need(name) {
  const v = process.env[name]
  if (!v) { console.error(`${name} is required`); process.exit(2) }
  return v
}
// The seed writes customers, deals and tickets into whatever it is pointed at.
// A dedicated variable plus an explicit "this deployment is disposable" flag
// keeps it from ever running against a shared instance by accident.
const baseURL = need('RELIO_GUIDE_URL').replace(/\/$/, '')
if (process.env.RELIO_GUIDE_DISPOSABLE !== '1') {
  console.error('Refusing to seed: set RELIO_GUIDE_DISPOSABLE=1 only for a deployment you can throw away')
  process.exit(2)
}
const adminUser = need('RELIO_GUIDE_ADMIN')
const adminPassword = need('RELIO_GUIDE_PASSWORD')
const chromePath = process.env.RELIO_GUIDE_CHROME || ['/usr/bin/google-chrome', '/usr/bin/chromium-browser', '/usr/bin/chromium'].find(Boolean)

// ---------------------------------------------------------------- API client
const cookies = new Map()
let csrf = ''
async function api(method, route, body, headers = {}) {
  const res = await fetch(baseURL + route, {
    method,
    headers: {
      ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
      ...(csrf ? { 'X-CSRF-Token': csrf } : {}),
      ...(cookies.size ? { Cookie: [...cookies].map(([k, v]) => `${k}=${v}`).join('; ') } : {}),
      ...headers,
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  for (const line of res.headers.getSetCookie?.() ?? []) {
    const [pair] = line.split(';')
    const eq = pair.indexOf('=')
    cookies.set(pair.slice(0, eq).trim(), pair.slice(eq + 1).trim())
  }
  const text = await res.text()
  if (!res.ok) throw new Error(`${method} ${route} -> HTTP ${res.status}: ${text.slice(0, 300)}`)
  return text ? JSON.parse(text) : null
}
const get = (route) => api('GET', route)
const post = (route, body) => api('POST', route, body)
const put = (route, body) => api('PUT', route, body)

async function login() {
  const login = await post('/api/v1/auth/login', { username: adminUser, password: adminPassword })
  csrf = login.user.csrfToken
  if (login.user.mustChangePassword) {
    const next = process.env.RELIO_GUIDE_NEW_PASSWORD
    if (!next) { console.error('The account must change its password first: set RELIO_GUIDE_NEW_PASSWORD'); process.exit(2) }
    await post('/api/v1/me/password', { currentPassword: adminPassword, newPassword: next })
    console.log('changed the bootstrap password (first login)')
  }
  return login.user
}

// ---------------------------------------------------------------- demo data
const day = 86400000
const today = new Date()
const isoDate = (offsetDays) => new Date(today.getTime() + offsetDays * day).toISOString().slice(0, 10)
const isoTime = (offsetDays, hour = 10) => { const d = new Date(today.getTime() + offsetDays * day); d.setHours(hour, 0, 0, 0); return d.toISOString() }

async function findOrCreate(listRoute, matchKey, matchValue, createRoute, body) {
  const page = await get(listRoute)
  const hit = (page.items ?? []).find((it) => it[matchKey] === matchValue)
  if (hit) return hit
  return post(createRoute, body)
}

async function seed(me) {
  const stages = (await get('/api/v1/pipeline')).items[0].stages
  const stage = (i) => stages[Math.min(i, stages.length - 1)].id

  const customerSpecs = [
    { name: '데모전자(주)', customerType: 'CUSTOMER', industry: '전자·제조', health: 'NORMAL' },
    { name: '한빛소프트웨어', customerType: 'PROSPECT', industry: 'IT·소프트웨어', health: 'NORMAL' },
    { name: '나래물류', customerType: 'CUSTOMER', industry: '물류·유통', health: 'AT_RISK' },
    { name: '청우제약', customerType: 'PROSPECT', industry: '제약·바이오', health: 'NORMAL' },
    { name: '미래금융그룹', customerType: 'CUSTOMER', industry: '금융', health: 'NORMAL' },
  ]
  const customers = {}
  for (const spec of customerSpecs) {
    customers[spec.name] = await findOrCreate(`/api/v1/customers?q=${encodeURIComponent(spec.name)}&limit=50`, 'name', spec.name,
      '/api/v1/customers', { ...spec, customFields: {} })
  }
  const demo = customers['데모전자(주)']

  const contactSpecs = [
    { name: '홍길동', title: '구매본부장', email: 'hong@example.com', decisionMaker: true, relationshipRole: 'DECISION_MAKER', influence: 'HIGH', sentiment: 'NEUTRAL', relationshipStrength: 55, decisionPower: 95 },
    { name: '김영희', title: 'IT기획팀장', email: 'kim@example.com', primaryContact: true, relationshipRole: 'CHAMPION', influence: 'HIGH', sentiment: 'SUPPORT', relationshipStrength: 85, decisionPower: 70 },
    { name: '이철수', title: '보안아키텍트', email: 'lee@example.com', relationshipRole: 'INFLUENCER', influence: 'MEDIUM', sentiment: 'SUPPORT', relationshipStrength: 70, decisionPower: 55 },
    { name: '박민수', title: '재무팀장', email: 'park@example.com', relationshipRole: 'PROCUREMENT', influence: 'MEDIUM', sentiment: 'NEUTRAL', relationshipStrength: 40, decisionPower: 60 },
  ]
  const contacts = {}
  for (const spec of contactSpecs) {
    contacts[spec.name] = await findOrCreate(`/api/v1/contacts?customerId=${demo.id}&limit=50`, 'name', spec.name,
      '/api/v1/contacts', { customerId: demo.id, ...spec })
  }
  const graph = await get(`/api/v1/customers/${demo.id}/relationships`)
  if ((graph.edges ?? []).length === 0) {
    await post(`/api/v1/customers/${demo.id}/relationships`, { sourceContactId: contacts['김영희'].id, targetContactId: contacts['홍길동'].id, relationshipType: 'INFLUENCES', strength: 90, description: '내부 챔피언이 구매 결정권자에게 영향', active: true, version: 0 })
    await post(`/api/v1/customers/${demo.id}/relationships`, { sourceContactId: contacts['이철수'].id, targetContactId: contacts['김영희'].id, relationshipType: 'TRUSTS', strength: 75, description: '기술 검토 신뢰', active: true, version: 0 })
    await post(`/api/v1/customers/${demo.id}/relationships`, { sourceContactId: contacts['박민수'].id, targetContactId: contacts['홍길동'].id, relationshipType: 'REPORTS_TO', strength: 60, description: '재무 검토 보고 라인', active: true, version: 0 })
  }
  const plan = await get(`/api/v1/customers/${demo.id}/account-plan`).catch(() => null)
  if (!plan || !plan.status || plan.status === 'NONE') {
    await put(`/api/v1/customers/${demo.id}/account-plan`, { planYear: today.getFullYear(), status: 'ACTIVE', strategy: 'CRM 도입 성공을 발판으로 매출 분석 모듈까지 확장', customerGoals: ['영업 전망 정확도 개선', '사내 AI 도입'], strategicInitiatives: ['전사 CRM 현대화'], ourObjectives: ['Relio를 실행 시스템으로 정착'], whiteSpaces: [{ productName: 'Revenue Intelligence', status: 'NOT_OFFERED', potentialAmount: 250000000, notes: '4분기 검증' }, { productName: 'CRM Core', status: 'CUSTOMER', potentialAmount: 0 }], competitors: ['레거시 CRM'], risks: ['예산 시기'], targetRevenue: 500000000, potentialRevenue: 750000000, version: plan?.version ?? 0 })
  }

  const dealSpecs = [
    { name: '데모전자 CRM 전사 확산', customer: '데모전자(주)', stage: 3, expectedAmount: 320000000, probability: 70, expectedCloseDate: isoDate(20), forecastCategory: 'COMMIT', nextAction: '본부장 최종 제안 발표', nextActionDate: isoDate(3) },
    { name: '한빛소프트웨어 영업관리 도입', customer: '한빛소프트웨어', stage: 1, expectedAmount: 85000000, probability: 30, expectedCloseDate: isoDate(45), forecastCategory: 'PIPELINE', nextAction: '요구사항 워크숍', nextActionDate: isoDate(7) },
    { name: '나래물류 갱신 및 라이선스 증설', customer: '나래물류', stage: 2, expectedAmount: 120000000, probability: 50, expectedCloseDate: isoDate(-5), forecastCategory: 'BEST_CASE', nextAction: '갱신 견적 재발송', nextActionDate: isoDate(-4) },
    { name: '청우제약 MCP 연동 PoC', customer: '청우제약', stage: 0, expectedAmount: 40000000, probability: 20, expectedCloseDate: isoDate(60), forecastCategory: 'PIPELINE', nextAction: 'PoC 범위 합의', nextActionDate: isoDate(10) },
    { name: '미래금융 고객 360 구축', customer: '미래금융그룹', stage: 2, expectedAmount: 210000000, probability: 55, expectedCloseDate: isoDate(30), forecastCategory: 'BEST_CASE', nextAction: '보안 심사 자료 제출', nextActionDate: isoDate(2) },
    { name: '데모전자 교육 패키지', customer: '데모전자(주)', stage: 1, expectedAmount: 15000000, probability: 40, expectedCloseDate: isoDate(25), forecastCategory: 'PIPELINE', nextAction: '', nextActionDate: null },
  ]
  const deals = {}
  for (const spec of dealSpecs) {
    const { customer, stage: idx, ...rest } = spec
    deals[spec.name] = await findOrCreate(`/api/v1/opportunities?q=${encodeURIComponent(spec.name)}&limit=50`, 'name', spec.name,
      '/api/v1/opportunities', { ...rest, customerId: customers[customer].id, stageId: stage(idx), currencyCode: 'KRW', customFields: {} })
  }

  const existingActivities = (await get('/api/v1/activities?limit=100')).items ?? []
  const activitySpecs = [
    { customer: '데모전자(주)', deal: '데모전자 CRM 전사 확산', activityType: 'MEETING', subject: '제안 발표 리허설', description: '구매본부장 참석 예정, 데모 시나리오 3개 확정', occurredAt: isoTime(-1, 14), nextAction: '본부장 최종 제안 발표', nextActionDate: isoDate(3) },
    { customer: '데모전자(주)', deal: '데모전자 CRM 전사 확산', activityType: 'CALL', subject: '보안 검토 질의 응답', description: 'SSO 연동 방식과 데이터 보관 위치 확인', occurredAt: isoTime(-3, 11), nextAction: '', nextActionDate: null },
    { customer: '한빛소프트웨어', deal: '한빛소프트웨어 영업관리 도입', activityType: 'VISIT', subject: '현장 방문 및 요구사항 청취', description: '영업팀 12명 인터뷰', occurredAt: isoTime(-6, 10), nextAction: '요구사항 워크숍', nextActionDate: isoDate(7) },
    { customer: '미래금융그룹', deal: '미래금융 고객 360 구축', activityType: 'EMAIL', subject: '보안 심사 체크리스트 송부', description: '항목 42개 중 38개 답변 완료', occurredAt: isoTime(-2, 16), nextAction: '보안 심사 자료 제출', nextActionDate: isoDate(2) },
    { customer: '나래물류', deal: '나래물류 갱신 및 라이선스 증설', activityType: 'CALL', subject: '갱신 조건 협의', description: '단가 인상 폭에 이견, 재견적 요청', occurredAt: isoTime(-12, 15), nextAction: '갱신 견적 재발송', nextActionDate: isoDate(-4) },
  ]
  for (const spec of activitySpecs) {
    if (existingActivities.some((a) => a.subject === spec.subject)) continue
    const { customer, deal, ...rest } = spec
    await post('/api/v1/activities', { ...rest, customerId: customers[customer].id, opportunityId: deals[deal].id })
  }

  const existingVoices = (await get('/api/v1/voices?limit=100')).items ?? []
  const voiceSpecs = [
    { customer: '나래물류', voiceType: 'COMPLAINT', channel: 'PHONE', severity: 'HIGH', title: '월말 정산 리포트 지연', body: '3개월 연속 정산 리포트가 이틀 늦게 도착. 재무 마감에 차질.', occurredAt: isoTime(-4, 9) },
    { customer: '데모전자(주)', voiceType: 'REQUEST', channel: 'EMAIL', severity: 'NORMAL', title: '대시보드 부서별 필터 추가 요청', body: '사업부 단위로 파이프라인을 나눠 보고 싶다는 요청.', occurredAt: isoTime(-2, 13) },
    { customer: '나래물류', voiceType: 'CHURN_RISK', channel: 'VISIT', severity: 'CRITICAL', title: '경쟁사 제안서 검토 중', body: '갱신 시점에 경쟁사와 비교 검토 중이라는 언급.', occurredAt: isoTime(-1, 11) },
    { customer: '미래금융그룹', voiceType: 'INQUIRY', channel: 'PORTAL', severity: 'LOW', title: '감사 로그 보관 기간 문의', body: '내부 감사 요건에 맞는 보관 기간 확인 요청.', occurredAt: isoTime(-5, 10) },
  ]
  for (const spec of voiceSpecs) {
    if (existingVoices.some((v) => v.title === spec.title)) continue
    const { customer, ...rest } = spec
    await post('/api/v1/voices', { ...rest, customerId: customers[customer].id, contactId: customer === '데모전자(주)' ? contacts['김영희'].id : '', customFields: {} })
  }

  const contractsPage = (await get('/api/v1/contracts?limit=100')).items ?? []
  const year = today.getFullYear()
  if (!contractsPage.some((c) => c.title === '데모전자 CRM 연간 구독')) {
    const c = await post('/api/v1/contracts', { customerId: demo.id, opportunityId: deals['데모전자 CRM 전사 확산'].id, title: '데모전자 CRM 연간 구독', amount: 120000000, currencyCode: 'KRW', startDate: `${year}-01-01`, endDate: `${year}-12-31`, status: 'DRAFT', autoRenew: true, revenueScheduleType: 'MONTHLY', renewalNoticeDays: 90, renewalAction: '갱신 QBR 진행', customFields: {} })
    await post(`/api/v1/contracts/${c.id}/activate`, { version: c.version })
  }
  if (!contractsPage.some((c) => c.title === '나래물류 물류관리 연동 유지보수')) {
    await post('/api/v1/contracts', { customerId: customers['나래물류'].id, opportunityId: deals['나래물류 갱신 및 라이선스 증설'].id, title: '나래물류 물류관리 연동 유지보수', amount: 36000000, currencyCode: 'KRW', startDate: isoDate(-340), endDate: isoDate(25), status: 'ACTIVE', autoRenew: false, revenueScheduleType: 'QUARTERLY', renewalNoticeDays: 60, renewalAction: '갱신 제안서 발송', customFields: {} })
  }

  // Demo colleagues so the user and role screens are not a single row. Their
  // password is only ever read from the environment.
  const seedPassword = process.env.RELIO_GUIDE_SEED_PASSWORD
  if (seedPassword) {
    const roles = (await get('/api/v1/admin/roles')).items ?? []
    const roleId = (code) => roles.find((r) => r.code === code)?.id
    const users = (await get('/api/v1/admin/users')).items ?? []
    const userSpecs = [
      { username: 'hong', displayName: '홍길동', email: 'hong@example.com', title: '영업대표', roleIds: [roleId('SALES_USER')].filter(Boolean) },
      { username: 'kim', displayName: '김영희', email: 'kim@example.com', title: '영업팀장', roleIds: [roleId('SALES_MANAGER')].filter(Boolean) },
      { username: 'lee', displayName: '이철수', email: 'lee@example.com', title: 'Presales', roleIds: [roleId('SALES_USER')].filter(Boolean) },
    ]
    for (const spec of userSpecs) {
      if (users.some((u) => u.username === spec.username)) continue
      await post('/api/v1/admin/users', { ...spec, password: seedPassword }).catch((e) => console.warn('user seed skipped:', e.message))
    }
  } else {
    console.warn('RELIO_GUIDE_SEED_PASSWORD not set: demo users are not created, the user screen will show the admin only')
  }

  const keys = (await get('/api/v1/me/keys')).items ?? []
  if (!keys.some((k) => k.name === '가이드 데모 MCP 키')) {
    await post('/api/v1/me/keys', { name: '가이드 데모 MCP 키', scopes: ['mcp:use', 'customer:read', 'opportunity:read', 'forecast:read'], channels: ['REST', 'MCP'] })
  }
  // POST toggles, so check first or a re-run would un-star the customer.
  const favorites = (await get('/api/v1/me/favorites?resource=CUSTOMER')).items ?? []
  if (!favorites.some((f) => f.resourceId === demo.id)) {
    await post('/api/v1/me/favorites', { resource: 'CUSTOMER', resourceId: demo.id }).catch((e) => console.warn('favorite skipped:', e.message))
  }
  // Signals, risks and recommendations are produced by the rule engine; run it
  // once so the analysis screens are not empty.
  await post('/api/v1/intelligence/run', {}).catch((e) => console.warn('intelligence run skipped:', e.message))
  return { demo, me }
}

// ---------------------------------------------------------------- capture
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
async function settle(page) {
  await page.waitForNetworkIdle({ idleTime: 600, timeout: 15000 }).catch(() => {})
  await page.waitForFunction(() => !document.querySelector('.spinner, .boot'), { timeout: 15000 }).catch(() => {})
  await sleep(400)
}
async function clickText(page, selector, text) {
  const handle = await page.evaluateHandle((sel, t) => [...document.querySelectorAll(sel)].find((el) => el.textContent.trim().includes(t)) || null, selector, text)
  const el = handle.asElement()
  if (!el) { console.warn(`  (no "${text}" ${selector} found — skipped)`); return false }
  await el.click()
  return true
}

async function scrollToHeading(page, text) {
  const ok = await page.evaluate((t) => { const h = [...document.querySelectorAll('h2')].find((el) => el.textContent.trim() === t); if (!h) return false; h.scrollIntoView({ block: 'start' }); window.scrollBy(0, -120); return true }, text)
  if (!ok) console.warn(`  (no "${text}" heading found — skipped)`)
}

// Every screen fires several API calls and the default API throttle is 120
// requests per minute per identity, so a straight run through thirty screens
// gets 429s halfway and the app falls back to the login page. Lift the limit
// for the run and put the original value back, whatever happens in between.
async function withRateLimitLifted(fn) {
  const items = (await get('/api/v1/admin/settings?namespace=api')).items ?? []
  const original = items.find((it) => it.key === 'rate_limit_per_minute')
  if (!original) return fn()
  await put('/api/v1/admin/settings/api/rate_limit_per_minute', { value: 0, valueType: original.valueType })
  try { return await fn() } finally {
    await put('/api/v1/admin/settings/api/rate_limit_per_minute', { value: original.value, valueType: original.valueType })
    console.log(`restored api.rate_limit_per_minute = ${JSON.stringify(original.value)}`)
  }
}

function shots({ demo }) {
  return [
    { name: 'login', route: '/login', anonymous: true, act: async (p) => { await p.type('.login-form input', 'hong'); } },
    { name: 'dashboard', route: '/app/dashboard' },
    { name: 'palette', route: '/app/dashboard', act: async (p) => { await p.keyboard.down('Control'); await p.keyboard.press('k'); await p.keyboard.up('Control'); await sleep(300); await p.keyboard.type('데모'); await sleep(600) } },
    { name: 'recommendations', route: '/app/recommendations' },
    { name: 'customers', route: '/app/customers' },
    { name: 'customer-360', route: `/app/customers/${demo.id}` },
    { name: 'customer-360-relationships', route: `/app/customers/${demo.id}`, act: async (p) => { await scrollToHeading(p, '의사결정 관계도'); await sleep(500) } },
    { name: 'opportunities', route: '/app/opportunities' },
    { name: 'opportunity-detail', route: '/app/opportunities', act: async (p) => { await clickText(p, 'tbody tr', '데모전자 CRM 전사 확산'); await settle(p) } },
    { name: 'pipeline', route: '/app/pipeline' },
    { name: 'activities', route: '/app/activities' },
    { name: 'intelligence-center', route: '/app/intelligence-center' },
    { name: 'deal-intelligence', route: '/app/intelligence' },
    { name: 'forecast', route: '/app/forecast' },
    { name: 'voices', route: '/app/voices' },
    { name: 'contracts', route: '/app/contracts' },
    { name: 'me-keys', route: '/me/keys' },
    { name: 'me-dashboard', route: '/me/dashboard' },
    { name: 'admin-overview', route: '/admin/overview' },
    { name: 'admin-operations', route: '/admin/operations' },
    { name: 'admin-audit', route: '/admin/audit' },
    { name: 'admin-system', route: '/admin/system' },
    { name: 'admin-oidc', route: '/admin/oidc' },
    { name: 'admin-security', route: '/admin/security', afterRestore: true },
    { name: 'admin-users', route: '/admin/users' },
    { name: 'admin-roles', route: '/admin/roles' },
    { name: 'admin-pipeline', route: '/admin/pipeline' },
    { name: 'admin-sales-execution', route: '/admin/sales-execution' },
    { name: 'admin-approval', route: '/admin/approval' },
    { name: 'admin-voice-categories', route: '/admin/voice-categories' },
    { name: 'admin-keys', route: '/admin/keys' },
    { name: 'admin-data', route: '/admin/data' },
  ]
}

async function main() {
  const me = await login()
  console.log(`logged in as ${me.username}; seeding demo data…`)
  const ctx = await seed(me)
  await mkdir(outDir, { recursive: true })
  const all = shots(ctx).filter((shot) => !only || only.has(shot.name))
  await withRateLimitLifted(() => capture(all.filter((s) => !s.afterRestore)))
  // The security screen displays the API rate limit itself; photograph it
  // with the real value in place, not the lifted one.
  await capture(all.filter((s) => s.afterRestore))
  console.log(`done: ${outDir}`)
}

async function capture(list) {
  if (list.length === 0) return
  const browser = await puppeteer.launch({ executablePath: chromePath, headless: true, args: ['--no-sandbox', '--disable-gpu', '--font-render-hinting=none', '--lang=ko-KR'] })
  try {
    const origin = new URL(baseURL)
    for (const shot of list) {
      const page = await browser.newPage()
      await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 })
      await page.setExtraHTTPHeaders({ 'Accept-Language': 'ko-KR,ko;q=0.9' })
      if (!shot.anonymous) {
        await page.setCookie(...[...cookies].map(([name, value]) => ({ name, value, domain: origin.hostname, path: '/' })))
      }
      await page.goto(baseURL + shot.route, { waitUntil: 'domcontentloaded' })
      await settle(page)
      if (shot.act) { await shot.act(page); await sleep(300) }
      const file = path.join(outDir, `${shot.name}.png`)
      await page.screenshot({ path: file })
      console.log(`  ${shot.name}.png`)
      await page.close()
    }
  } finally {
    await browser.close()
  }
}

main().catch((e) => { console.error(e); process.exit(1) })
