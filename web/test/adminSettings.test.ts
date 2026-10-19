import { test } from 'node:test'
import assert from 'node:assert/strict'
import { persistSettings, settingChanges } from '../src/adminSettings.ts'
import type { Setting } from '../src/adminSettings.ts'
import { adminNav, matchesNav } from '../src/nav.ts'

const item = (extra: Partial<Setting> = {}): Setting => ({ namespace: 'system', key: 'service_name', value: 'Relio', valueType: 'string', secret: false, configured: true, restartRequired: false, version: 8, ...extra })
test('unchanged settings do not write; edits carry the loaded version and restart metadata', () => {
  assert.deepEqual(settingChanges([item()], [{ namespace: 'system', key: 'service_name', value: 'Relio' }]), [])
  const changes = settingChanges([item({ restartRequired: true })], [{ namespace: 'system', key: 'service_name', value: 'New', version: 0 }])
  assert.equal(changes[0].version, 8)
  assert.equal(changes[0].restartRequired, true)
})
test('blank secrets keep existing credentials and edits cannot downgrade encryption', () => {
  const secret = item({ key: 'password', value: null, secret: true })
  assert.deepEqual(settingChanges([secret], [{ namespace: 'system', key: 'password', value: '', secret: false }]), [])
  const [change] = settingChanges([secret], [{ namespace: 'system', key: 'password', value: 'new-secret', secret: false }])
  assert.equal(change.secret, true); assert.equal(change.version, 8)
})
test('new values infer JSON types and begin with create version zero', () => {
  const [change] = settingChanges([], [{ namespace: 'mcp', key: 'tool_allowlist', value: ['search_customers'] }])
  assert.equal(change.valueType, 'json'); assert.equal(change.version, 0)
})
test('partial failure waits for remaining writes and identifies only failed setting keys', async () => {
  let complete = false
  const changes = [{ namespace: 'system', key: 'service_name', value: 'Updated' }, { namespace: 'mail', key: 'enabled', value: true }]
  await assert.rejects(persistSettings([item()], changes, async change => {
    if (change.namespace === 'system') throw new Error('setting was changed by another user')
    await new Promise(resolve => setTimeout(resolve, 15)); complete = true
  }), error => {
    assert.equal(complete, true)
    assert.match(String(error), /1\/2개 저장, 1개 실패/)
    assert.match(String(error), /system.service_name: setting was changed by another user/)
    assert.ok(!String(error).includes('Updated'))
    return true
  })
})
test('management menus have unique routes, descriptions, and multi-word Korean/English search', () => {
  assert.equal(new Set(adminNav.map(x => x.to)).size, 19)
  assert.ok(adminNav.every(x => x.description && x.group))
  assert.deepEqual(adminNav.filter(x => matchesNav(x, 'ＭＣＰ  키')).map(x => x.to), ['/admin/keys'])
  assert.deepEqual(adminNav.filter(x => matchesNav(x, '기본 SSO')).map(x => x.to), ['/admin/oidc'])
  assert.equal(adminNav.filter(x => matchesNav(x, '존재하지않는메뉴')).length, 0)
  assert.equal(adminNav.filter(x => matchesNav(x, '   ')).length, 19)
})
