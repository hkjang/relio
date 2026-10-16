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
import { date, dateTime, relative } from '../src/api.ts'

const sample = '2026-09-20T07:05:09Z'
const clock = /\d{2}:\d{2}:\d{2}/

// relative() labels calendar days, so every fixture below is counted back from
// the local midnight of the run instead of a frozen literal that would drift
// into another branch as time passes.
const midnight = () => { const d = new Date(); d.setHours(0, 0, 0, 0); return d }
const daysAgoAtNoon = (days: number) => { const d = midnight(); d.setDate(d.getDate() - days); d.setHours(12); return d.toISOString() }

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

test('relative calls the last second of yesterday 어제, not 오늘', () => {
  assert.equal(relative(new Date(midnight().getTime() - 1000).toISOString()), '어제')
})

test('relative still calls the first moment of today 오늘', () => {
  assert.equal(relative(new Date(midnight().getTime() + 1).toISOString()), '오늘')
  assert.equal(relative(new Date().toISOString()), '오늘')
})

test('relative treats a future timestamp from a skewed clock as 오늘', () => {
  assert.equal(relative(new Date(Date.now() + 86400000).toISOString()), '오늘')
})

test('relative counts whole calendar days between 2 and 29', () => {
  assert.equal(relative(daysAgoAtNoon(2)), '2일 전')
  assert.equal(relative(daysAgoAtNoon(29)), '29일 전')
})

test('relative hands anything 30 days or older to date', () => {
  const old = daysAgoAtNoon(30)
  assert.equal(relative(old), date(old))
  assert.doesNotMatch(relative(old), /일 전|오늘|어제/)
})

test('relative reports 기록 없음 for a missing value', () => {
  assert.equal(relative(undefined), '기록 없음')
  assert.equal(relative(''), '기록 없음')
})

test('relative reports 기록 없음 for an unparseable value instead of throwing', () => {
  assert.equal(relative('not-a-date'), '기록 없음')
})
