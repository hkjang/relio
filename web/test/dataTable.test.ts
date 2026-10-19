import { after, test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, rm } from 'node:fs/promises'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { build } from 'esbuild'
import { createElement as h, Fragment } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'

const directory = await mkdtemp(fileURLToPath(new URL('./.table-test-', import.meta.url)))
after(() => rm(directory, { recursive: true, force: true }))
await build({
  entryPoints: ['DataTable', 'Layout'].map(name => fileURLToPath(new URL(`../src/components/${name}.tsx`, import.meta.url))),
  outdir: directory, outExtension: { '.js': '.mjs' }, bundle: true, platform: 'node', format: 'esm', packages: 'external', jsx: 'automatic', loader: { '.css': 'empty' },
})
const { DataTable } = await import(pathToFileURL(`${directory}/DataTable.mjs`).href)
const { rowProps } = await import(pathToFileURL(`${directory}/Layout.mjs`).href)

test('table pagination limits DOM rows and numeric cell alignment survives conditional fragments', () => {
  const html = renderToStaticMarkup(h(DataTable, {
    rows: Array.from({ length: 30 }, (_, id) => ({ id })), label: '테스트', loadedLimit: 30,
    columns: [{ key: 'id', label: '번호', value: (r: { id: number }) => r.id, align: 'right' }, { key: 'action', label: '작업' }],
    renderRow: (r: { id: number }) => h('tr', { key: r.id }, h(Fragment, null, h('td', null, r.id), h('td', null, h('button', null, '편집')))),
  }))
  assert.equal((html.match(/<tr/g) || []).length, 26)
  assert.equal((html.match(/<td class="cell-number"/g) || []).length, 25)
  assert.ok(html.includes('aria-sort="none"') && html.includes('scope="col"'))
  assert.ok(html.includes('1–25 / 30건') && html.includes('불러온 30건 안에서 정렬'))
  assert.ok(html.includes('aria-label="다음 페이지"') && !html.includes('<div><td'))
})

test('empty table is a valid spanning cell, not a misleading page of results', () => {
  const html = renderToStaticMarkup(h(DataTable, { rows: [], columns: [{ key: 'name', label: '이름' }], label: '빈 목록', renderRow: () => null }))
  assert.ok(html.includes('colSpan="1"') && html.includes('0 / 0건'))
  assert.equal((html.match(/disabled=""/g) || []).length, 2)
})

test('row actions do not swallow nested button clicks or keyboard activation', () => {
  let opened = 0, prevented = 0
  const props = rowProps(() => opened++)
  const row = {}, button = {}
  props.onClick({ currentTarget: row, target: { closest: () => button } })
  props.onKeyDown({ currentTarget: row, target: button, key: 'Enter', preventDefault: () => prevented++ })
  assert.equal(opened, 0); assert.equal(prevented, 0)
  props.onClick({ currentTarget: row, target: { closest: () => row } })
  props.onKeyDown({ currentTarget: row, target: row, key: ' ', preventDefault: () => prevented++ })
  assert.equal(opened, 2); assert.equal(prevented, 1)
})
