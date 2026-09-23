// Runs on Node's built-in runner with type stripping (Node 22.18+ / 24):
//   npm test
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { parseMemberList } from '../src/memberList.ts'

test('a header row is skipped, whatever it is called', () => {
  for (const header of ['회원사명,회원사코드', '고객사명,고객 코드', 'name,code']) {
    assert.deepEqual(parseMemberList(`${header}\n㈜가나,123456789012`), [{ name: '㈜가나', customerCode: '123456789012' }])
  }
})

test('either column order works: the all-digit cell is the code', () => {
  assert.deepEqual(parseMemberList('123456789012,㈜가나'), [{ name: '㈜가나', customerCode: '123456789012' }])
})

test('tabs, quotes, a BOM and blank lines are tolerated', () => {
  const rows = parseMemberList('﻿"㈜가나"\t123456789012\r\n\r\n㈜다라,210987654321\n')
  assert.deepEqual(rows, [{ name: '㈜가나', customerCode: '123456789012' }, { name: '㈜다라', customerCode: '210987654321' }])
})

test('a line with one cell is kept so the server can report it', () => {
  // Dropping it silently would make a short import look complete.
  assert.deepEqual(parseMemberList('이름만있음'), [{ name: '이름만있음', customerCode: '' }])
})
