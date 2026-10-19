import { test } from 'node:test'
import assert from 'node:assert/strict'
import { nextSort, sortRows, timestamp } from '../src/table.ts'

test('numeric sorting uses real amounts, stays stable and never mutates server order', () => {
  const rows = [{ id: 1, amount: 100 }, { id: 2, amount: 9 }, { id: 3, amount: 20 }, { id: 4, amount: 9 }]
  const columns = [{ key: 'amount', value: (r: typeof rows[number]) => r.amount }]
  assert.deepEqual(sortRows(rows, columns, { key: 'amount', direction: 'asc' }).map(r => r.id), [2, 4, 3, 1])
  assert.deepEqual(sortRows(rows, columns, { key: 'amount', direction: 'desc' }).map(r => r.id), [1, 3, 2, 4])
  assert.deepEqual(rows.map(r => r.id), [1, 2, 3, 4])
  assert.deepEqual(sortRows(rows, columns, null), rows)
})

test('Korean names and numeric codes use natural order', () => {
  const rows = ['나래', '가람10', '가람2', '가람1']
  assert.deepEqual(sortRows(rows, [{ key: 'name', value: r => r }], { key: 'name', direction: 'asc' }), ['가람1', '가람2', '가람10', '나래'])
})

test('missing values and invalid dates remain last in either direction', () => {
  const rows = ['invalid', '2026-10-19', null, '2026-01-02', '']
  const columns = [{ key: 'date', value: timestamp }]
  assert.deepEqual(sortRows(rows, columns, { key: 'date', direction: 'asc' }), ['2026-01-02', '2026-10-19', 'invalid', null, ''])
  assert.deepEqual(sortRows(rows, columns, { key: 'date', direction: 'desc' }), ['2026-10-19', '2026-01-02', 'invalid', null, ''])
  assert.equal(timestamp('not-a-date'), null)
  assert.equal(timestamp(undefined), null)
})

test('header cycling restores default server order and respects preferred direction', () => {
  const column = { key: 'amount', initialDirection: 'desc' as const }
  const first = nextSort(null, column)
  assert.deepEqual(first, { key: 'amount', direction: 'desc' })
  const second = nextSort(first, column)
  assert.deepEqual(second, { key: 'amount', direction: 'asc' })
  assert.equal(nextSort(second, column), null)
  assert.deepEqual(nextSort(first, { key: 'name' }), { key: 'name', direction: 'asc' })
})

test('removed/unsortable columns preserve order; booleans and zero are real values', () => {
  const rows = [false, true, false]
  assert.deepEqual(sortRows(rows, [{ key: 'active', value: r => r }], { key: 'active', direction: 'desc' }), [true, false, false])
  assert.deepEqual(sortRows([0, null, -1], [{ key: 'n', value: r => r }], { key: 'n', direction: 'desc' }), [0, -1, null])
  assert.deepEqual(sortRows(rows, [{ key: 'actions' }], { key: 'actions', direction: 'asc' }), rows)
  assert.deepEqual(sortRows(rows, [], { key: 'removed', direction: 'asc' }), rows)
})
