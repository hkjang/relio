import { test } from 'node:test'
import assert from 'node:assert/strict'
import { changedFields, collectFields } from '../src/voiceFields.ts'
import type { VoiceField } from '../src/components/VoiceWorkspace.tsx'

const fields: VoiceField[] = [
  { id: '1', key: 'error_code', label: '오류코드', type: 'Text', phase: 'INTAKE', required: false },
  { id: '2', key: 'count', label: '횟수', type: 'Number', phase: 'INTAKE', required: false },
  { id: '3', key: 'resolver', label: '해결 주체', type: 'Select', phase: 'RESOLUTION', required: true },
  { id: '4', key: 'tags', label: '분류', type: 'Multi Select', phase: 'INTAKE', required: false },
]

test('intake does not collect resolution fields, and blank optional fields can be cleared only on edit', () => {
  const form = new FormData()
  form.set('field.error_code', '  ')
  form.set('field.count', '0')
  form.set('field.resolver', 'OPS')
  assert.deepEqual(collectFields(form, fields, 'INTAKE'), { count: 0 })
  assert.deepEqual(collectFields(form, fields, 'INTAKE', true), { error_code: null, count: 0, tags: null })
  assert.deepEqual(collectFields(form, fields, 'RESOLUTION'), { resolver: 'OPS' })
})

test('field patches preserve unmodified retired codes and send only changed values', () => {
  assert.deepEqual(changedFields({ resolver: 'RETIRED', error_code: null, count: 1 }, { resolver: 'RETIRED', error_code: 'B124', count: 1 }), { error_code: null })
  assert.deepEqual(changedFields({ error_code: null, count: null }, { error_code: null }), {})
})

test('multiple selections survive form serialization; reordering is not an edit', () => {
  const form = new FormData()
  form.append('field.tags', 'A'); form.append('field.tags', 'B')
  assert.deepEqual(collectFields(form, fields, 'INTAKE'), { tags: ['A', 'B'] })
  assert.deepEqual(changedFields({ tags: ['B', 'A'] }, { tags: ['A', 'B'] }), {})
})
