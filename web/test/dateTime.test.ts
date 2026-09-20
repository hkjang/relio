// Runs on Node's built-in runner with type stripping (Node 22.18+ / 24):
//   npm test
// It sits outside src/ so tsc, which has no Node types, does not see it, and
// so that vite never bundles it.
//
// The audit page needs seconds to order several logins on the same day;
// date() (used by every other page) deliberately stops at the day. The
// time zone is pinned so the calendar day never straddles midnight in CI,
// but the assertions only check that a time of day is present, not its value.
process.env.TZ = 'Asia/Seoul'
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { date, dateTime } from '../src/api.ts'

const sample = '2026-09-20T07:05:09Z'
const clock = /\d{2}:\d{2}:\d{2}/

test('dateTime renders the day and a 24h clock down to the second', () => {
  const out = dateTime(sample)
  assert.match(out, clock)
  assert.match(out, /2026/)
  assert.doesNotMatch(out, /오전|오후|AM|PM/)
})

test('date keeps stopping at the day so the other pages are unchanged', () => {
  assert.doesNotMatch(date(sample), clock)
  assert.match(date(sample), /2026/)
})

test('dateTime falls back to the same em dash as date for a missing value', () => {
  assert.equal(dateTime(undefined), '—')
  assert.equal(dateTime(''), '—')
  assert.equal(dateTime(undefined), date(undefined))
})
