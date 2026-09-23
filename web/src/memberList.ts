// A department's member list arrives as whatever its spreadsheet exports: with
// or without a header, comma or tab separated, code first or name first. Kept
// free of React so the parsing rules can be tested on their own.

/** parseMemberList reads "name,code" lines, with or without a header row. */
export function parseMemberList(text: string) {
  const rows: { name: string; customerCode: string }[] = []
  for (const raw of text.replace(/^﻿/, '').split(/\r?\n/)) {
    const line = raw.trim()
    if (!line) continue
    const cells = line.split(/[,\t]/).map(c => c.trim().replace(/^"|"$/g, ''))
    if (cells.length < 2) { rows.push({ name: cells[0], customerCode: '' }); continue }
    const [a, b] = cells
    // Accept either column order: the code is the all-digit cell.
    const [name, code] = /^\d+$/.test(a) && !/^\d+$/.test(b) ? [b, a] : [a, b]
    if (/^(회원사명|고객사명|name)$/i.test(name) || /^(회원사코드|고객 ?코드|code|customerCode)$/i.test(code)) continue
    rows.push({ name, customerCode: code })
  }
  return rows
}

