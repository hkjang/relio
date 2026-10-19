import type { VoiceField } from './components/VoiceWorkspace'

/** Omitted on intake; explicit null on edit clears an optional field. */
export function collectFields(form: FormData, fields: VoiceField[], phase: VoiceField['phase'], includeEmpty = false) {
  const out: Record<string, unknown> = {}
  for (const f of fields.filter(x => x.phase === phase)) {
    const name = `field.${f.key}`
    if (f.type === 'Boolean') { out[f.key] = form.get(name) === 'on'; continue }
    if (f.type === 'Multi Select') {
      const values = form.getAll(name).map(String).filter(Boolean)
      if (values.length || includeEmpty) out[f.key] = values.length ? values : null
      continue
    }
    const text = String(form.get(name) ?? '').trim()
    if (!text) { if (includeEmpty) out[f.key] = null; continue }
    out[f.key] = ['Number', 'Money', 'Percent'].includes(f.type) ? Number(text) : text
  }
  return out
}

/** Do not re-submit unchanged legacy codes that may since have been retired. */
export function changedFields(values: Record<string, unknown>, stored: Record<string, unknown> = {}) {
  return Object.fromEntries(Object.entries(values).filter(([key, value]) => {
    const before = stored[key]
    if (value == null && (before == null || before === '')) return false
    if (Array.isArray(value) && Array.isArray(before)) return JSON.stringify([...value].sort()) !== JSON.stringify([...before].sort())
    return JSON.stringify(value) !== JSON.stringify(before)
  }))
}
