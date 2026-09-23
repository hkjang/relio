import React, { FormEvent, useEffect, useRef, useState } from 'react'
import { api } from '../api'
import { errorMessage } from '../App'
import { Customer } from '../types'
import { Modal } from './Layout'
import { parseMemberList } from '../memberList'

// A VOC workspace is one department's intake on the shared engine: its own
// request types, fields collected at intake and at resolution, its customer
// code, and — when isolated — its own data boundary.

export type VoiceField = {
  id: string; key: string; label: string; type: string; required: boolean
  options?: string[]; phase: 'INTAKE' | 'RESOLUTION'; helpText?: string
}
export type VoiceCategory = {
  id: string; code: string; name: string; voiceType: string
  responseHours: number; resolutionHours: number; slaEnabled: boolean; workspaceId?: string
}
export type VoiceWorkspace = {
  id: string; code: string; name: string; description?: string
  isolated: boolean; knowledgeGate: boolean; slaEnabled: boolean
  customerCodeLabel?: string; customerCodePattern?: string
  organizationId?: string; organizationName?: string; active: boolean; displayOrder: number
  fields: VoiceField[]; categories: VoiceCategory[]
}

// Knowledge and evidence words are the department's own. They are kept apart
// from the shared label dictionary, where IN_REVIEW and APPROVED already mean
// a request status and an approval decision.
export const knowledgeLabels: Record<string, string> = { UNREVIEWED: '미검토', IN_REVIEW: '검토중', APPROVED: '반영', EXCLUDED: '제외' }
export const knowledgeTone: Record<string, string> = { UNREVIEWED: 'NORMAL', IN_REVIEW: 'PENDING', APPROVED: 'ACTIVE', EXCLUDED: 'DISABLED' }
export const evidenceOptions = [
  { value: 'CONFIRMED', label: '확인', detail: '테스트·로그 등 객관 근거 있음' },
  { value: 'PRESUMED', label: '추정', detail: '정황상 판단' },
  { value: 'UNIDENTIFIED', label: '미특정', detail: '원인 특정 안 됨(증상은 해소)' },
]
export const evidenceLabel = (value?: string) => evidenceOptions.find(x => x.value === value)?.label || '—'

export function KnowledgeBadge({ status }: { status: string }) {
  return <span className={`knowledge-badge knowledge-${status.toLowerCase()}`}>{knowledgeLabels[status] || status}</span>
}

/** FieldInputs renders a phase's fields; values are read back with collectFields. */
export function FieldInputs({ fields, phase, values }: { fields: VoiceField[]; phase: VoiceField['phase']; values?: Record<string, unknown> }) {
  const list = fields.filter(f => f.phase === phase)
  if (!list.length) return null
  return <>{list.map(f => {
    const current = values?.[f.key]
    const name = `field.${f.key}`
    const title = <>{f.label}{f.required ? ' *' : ''}</>
    let input
    if (f.type === 'Select') {
      input = <select name={name} required={f.required} defaultValue={typeof current === 'string' ? current : ''}>
        <option value="">{f.required ? '선택하세요' : '선택 안 함'}</option>
        {(f.options || []).map(o => <option key={o} value={o}>{o}</option>)}
      </select>
    } else if (f.type === 'Textarea') {
      input = <textarea name={name} rows={3} required={f.required} defaultValue={typeof current === 'string' ? current : ''} />
    } else if (f.type === 'Number' || f.type === 'Money' || f.type === 'Percent') {
      input = <input name={name} type="number" required={f.required} defaultValue={typeof current === 'number' ? current : ''} />
    } else if (f.type === 'Date') {
      input = <input name={name} type="date" required={f.required} defaultValue={typeof current === 'string' ? current : ''} />
    } else if (f.type === 'Boolean') {
      return <label key={f.key} className="check-label"><input name={name} type="checkbox" defaultChecked={current === true} /> {f.label}{f.helpText && <small>{f.helpText}</small>}</label>
    } else {
      input = <input name={name} required={f.required} defaultValue={typeof current === 'string' ? current : ''} maxLength={2000} />
    }
    return <label key={f.key} className={f.type === 'Textarea' ? 'span-2' : ''}>{title}{input}{f.helpText && <small>{f.helpText}</small>}</label>
  })}</>
}

export function collectFields(form: FormData, fields: VoiceField[], phase: VoiceField['phase']) {
  const out: Record<string, unknown> = {}
  for (const f of fields.filter(x => x.phase === phase)) {
    const raw = form.get(`field.${f.key}`)
    if (f.type === 'Boolean') { out[f.key] = raw === 'on'; continue }
    const text = String(raw ?? '').trim()
    if (!text) continue
    out[f.key] = f.type === 'Number' || f.type === 'Money' || f.type === 'Percent' ? Number(text) : text
  }
  return out
}

/**
 * CustomerPicker searches customers by name or code as the handler types, and
 * registers a new member with just a name and a code without leaving the
 * intake. A plain dropdown of the first 200 customers could not reach most of
 * a member list thousands long.
 */
export function CustomerPicker({ workspace, value, onChange, notify, canRegister }: {
  workspace?: VoiceWorkspace; value?: Customer | null; onChange: (c: Customer | null) => void
  notify: (m: string, e?: boolean) => void; canRegister: boolean
}) {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<Customer[]>([])
  const [open, setOpen] = useState(false)
  const [registering, setRegistering] = useState(false)
  const [busy, setBusy] = useState(false)
  const codeLabel = workspace?.customerCodeLabel || '고객 코드'
  const seq = useRef(0)
  useEffect(() => {
    const q = query.trim()
    if (!open) return
    const mine = ++seq.current
    const timer = setTimeout(() => {
      api<{ items: Customer[] }>('/api/v1/customers?limit=20' + (q ? '&q=' + encodeURIComponent(q) : ''))
        .then(v => { if (mine === seq.current) setResults(v.items) }).catch(() => {})
    }, q ? 200 : 0)
    return () => clearTimeout(timer)
  }, [query, open])

  // The picker lives inside the intake form, and a form cannot contain
  // another form: the browser drops the inner one and "register" would submit
  // the intake. So the quick registration is plain controlled inputs.
  const [draft, setDraft] = useState({ name: '', customerCode: '' })
  const startRegister = () => {
    const q = query.trim()
    setDraft(/^\d+$/.test(q) ? { name: '', customerCode: q } : { name: q, customerCode: '' })
    setRegistering(true)
  }
  const pattern = workspace?.customerCodePattern ? new RegExp(workspace.customerCodePattern) : null
  const codeInvalid = Boolean(draft.customerCode && pattern && !pattern.test(draft.customerCode))
  async function register() {
    if (!draft.name.trim() || !draft.customerCode.trim() || codeInvalid) return
    setBusy(true)
    try {
      const created = await api<Customer>(`/api/v1/voices/workspaces/${workspace!.id}/customers`, { method: 'POST', body: JSON.stringify(draft) })
      notify(`${created.name}을(를) 등록했습니다.`); onChange(created); setRegistering(false); setOpen(false)
    } catch (err) { notify(errorMessage(err), true) } finally { setBusy(false) }
  }
  const submitOnEnter = (e: React.KeyboardEvent) => { if (e.key === 'Enter') { e.preventDefault(); void register() } }

  if (value) return <div className="customer-picked">
    <span><b>{value.name}</b>{value.customerCode && <small>{codeLabel} {value.customerCode}</small>}</span>
    <button type="button" className="btn btn-sm btn-ghost" onClick={() => { onChange(null); setOpen(true) }}>변경</button>
  </div>

  return <div className="customer-picker">
    <input value={query} onChange={e => { setQuery(e.target.value); setOpen(true) }} onFocus={() => setOpen(true)}
      placeholder={`고객사명 또는 ${codeLabel}로 검색`} aria-label="고객 검색" role="combobox" aria-expanded={open} />
    {open && !registering && <div className="customer-results" role="listbox">
      {results.map(c => <button type="button" role="option" aria-selected="false" key={c.id} onClick={() => { onChange(c); setOpen(false) }}>
        <b>{c.name}</b><small>{c.customerCode ? `${codeLabel} ${c.customerCode}` : c.industry || ''}</small></button>)}
      {!results.length && <p className="customer-empty">{query.trim() ? '일치하는 고객이 없습니다.' : '검색어를 입력하세요.'}</p>}
      {canRegister && workspace && <button type="button" className="customer-register" onClick={startRegister}>＋ 새 {workspace.customerCodeLabel ? '회원사' : '고객'} 간이 등록</button>}
    </div>}
    {registering && workspace && <div className="customer-quick" role="group" aria-label="간이 등록">
      <b>간이 등록</b><small>고객사명과 {codeLabel}만 입력합니다. 나머지 정보는 필요할 때 고객 화면에서 보완하세요.</small>
      <div className="form-grid">
        <label>고객사명 *<input value={draft.name} autoFocus onKeyDown={submitOnEnter} onChange={e => setDraft({ ...draft, name: e.target.value })} aria-label="고객사명" /></label>
        <label>{codeLabel} *<input value={draft.customerCode} inputMode="numeric" onKeyDown={submitOnEnter} aria-invalid={codeInvalid}
          onChange={e => setDraft({ ...draft, customerCode: e.target.value.trim() })} aria-label={codeLabel} />
          <small className={codeInvalid ? 'danger-text' : ''}>{workspace.customerCodePattern === '^[0-9]{12}$' ? '숫자 12자리' : codeInvalid ? '형식이 맞지 않습니다' : ' '}</small></label>
      </div>
      <div className="voice-actions"><button type="button" className="btn btn-ghost" onClick={() => setRegistering(false)}>취소</button>
        <button type="button" className="btn btn-primary" disabled={busy || !draft.name.trim() || !draft.customerCode.trim() || codeInvalid} onClick={() => void register()}>{busy ? '등록 중…' : '등록하고 선택'}</button></div>
    </div>}
  </div>
}

/**
 * ResolveModal replaces the browser prompt the old "해결로 변경" used. The two
 * choice fields are what the knowledge gate rests on, so they are collected
 * here, in the same step, instead of being left for later.
 */
export function ResolveModal({ voice, workspace, fields, onClose, onSaved, notify }: {
  voice: { id: string; version: number; resolution?: string; rootCause?: string; preventiveAction?: string; causeEvidence?: string; customFields?: Record<string, unknown> }
  workspace?: VoiceWorkspace; fields: VoiceField[]; onClose: () => void; onSaved: () => void; notify: (m: string, e?: boolean) => void
}) {
  const [busy, setBusy] = useState(false)
  const gate = Boolean(workspace?.knowledgeGate)
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const f = new FormData(e.currentTarget); setBusy(true)
    try {
      await api(`/api/v1/voices/${voice.id}`, { method: 'PUT', body: JSON.stringify({
        status: 'RESOLVED', resolution: f.get('resolution'), rootCause: f.get('rootCause'), preventiveAction: f.get('preventiveAction'),
        causeEvidence: f.get('causeEvidence') || '', customFields: collectFields(f, fields, 'RESOLUTION'), note: f.get('note'), version: voice.version,
      }) })
      notify('해결로 변경했습니다.'); onSaved()
    } catch (err) { notify(errorMessage(err), true) } finally { setBusy(false) }
  }
  return <Modal title="해결 처리" onClose={onClose} wide><form className="form" onSubmit={submit}>
    <div className="form-grid">
      <label className="span-2">해결 내용 *<textarea name="resolution" rows={3} required autoFocus defaultValue={voice.resolution || ''} placeholder="무엇을 어떻게 처리했는지 남깁니다." /></label>
      <label className="span-2">근본 원인<textarea name="rootCause" rows={2} defaultValue={voice.rootCause || ''} placeholder="판명된 원인만 사실대로 기록합니다." /></label>
      <fieldset className="span-2 evidence-choice">
        <legend>원인 근거{gate ? ' *' : ''}</legend>
        <p className="evidence-help">원인이 특정되지 않은 해결도 정상적인 종결입니다. 실제 근거 수준 그대로 기록하세요.</p>
        <div>{evidenceOptions.map(o => <label key={o.value} className="evidence-option">
          <input type="radio" name="causeEvidence" value={o.value} required={gate} defaultChecked={voice.causeEvidence === o.value} />
          <span><b>{o.label}</b><small>{o.detail}</small></span></label>)}</div>
      </fieldset>
      <FieldInputs fields={fields} phase="RESOLUTION" values={voice.customFields} />
      <label className="span-2">재발 방지 조치<textarea name="preventiveAction" rows={2} defaultValue={voice.preventiveAction || ''} /></label>
      <label className="span-2">처리 이력 메모<input name="note" placeholder="선택 · 처리 이력에 함께 남깁니다." /></label>
    </div>
    <div className="modal-actions"><button type="button" className="btn btn-ghost" onClick={onClose}>취소</button>
      <button className="btn btn-primary" disabled={busy}>{busy ? '저장 중…' : '해결로 변경'}</button></div>
  </form></Modal>
}

/** ReviewPanel is the knowledge gate as the reviewer sees it. */
export function ReviewPanel({ voice, onSaved, notify }: { voice: { id: string; status: string; knowledgeStatus: string }; onSaved: () => void; notify: (m: string, e?: boolean) => void }) {
  const [status, setStatus] = useState(voice.knowledgeStatus)
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => { setStatus(voice.knowledgeStatus) }, [voice.knowledgeStatus])
  const closed = voice.status === 'RESOLVED' || voice.status === 'CLOSED'
  async function save() {
    setBusy(true)
    try {
      await api(`/api/v1/voices/${voice.id}/knowledge`, { method: 'PUT', body: JSON.stringify({ knowledgeStatus: status, note }) })
      notify(`지식 반영 상태를 ${knowledgeLabels[status]}(으)로 저장했습니다.`); setNote(''); onSaved()
    } catch (e) { notify(errorMessage(e), true) } finally { setBusy(false) }
  }
  return <div className="drawer-section review-panel">
    <h3>지식 반영 판정 <small>검토자 전용</small></h3>
    <p className="muted-copy">'반영'으로 판정한 건만 에이전트의 유사 사례 검색에 진단 근거로 나옵니다. 원인 근거와 처리 결과가 내용과 맞는지 확인하세요.</p>
    <div className="segmented" role="radiogroup" aria-label="지식 반영 상태">
      {Object.entries(knowledgeLabels).map(([value, text]) => <button key={value} type="button" role="radio" aria-checked={status === value}
        className={status === value ? 'active' : ''} disabled={value === 'APPROVED' && !closed} onClick={() => setStatus(value)}>{text}</button>)}
    </div>
    {!closed && <small className="muted-copy">해결 또는 종결된 건만 '반영'할 수 있습니다.</small>}
    <input value={note} onChange={e => setNote(e.target.value)} placeholder="판정 메모 (선택)" aria-label="판정 메모" />
    <button className="btn btn-primary" disabled={busy || (status === voice.knowledgeStatus && !note.trim())} onClick={save}>{busy ? '저장 중…' : '판정 저장'}</button>
  </div>
}

type ImportOutcome = { line: number; name: string; customerCode: string; result: string; message?: string }
type ImportResult = { created: number; updated: number; unchanged: number; failed: number; items: ImportOutcome[] }

export function MemberImportModal({ workspace, onClose, notify }: { workspace: VoiceWorkspace; onClose: () => void; notify: (m: string, e?: boolean) => void }) {
  const [text, setText] = useState('')
  const [updateNames, setUpdateNames] = useState(false)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<ImportResult | null>(null)
  const rows = parseMemberList(text)
  const codeLabel = workspace.customerCodeLabel || '고객 코드'
  const template = `data:text/csv;charset=utf-8,${encodeURIComponent('﻿회원사명,' + codeLabel + '\n예시회원사,000000000000\n')}`
  async function run() {
    setBusy(true)
    try {
      const r = await api<ImportResult>(`/api/v1/voices/workspaces/${workspace.id}/customers/import`, { method: 'POST', body: JSON.stringify({ rows, updateNames }) })
      setResult(r); notify(`등록 ${r.created} · 갱신 ${r.updated} · 기존 ${r.unchanged} · 오류 ${r.failed}`)
    } catch (e) { notify(errorMessage(e), true) } finally { setBusy(false) }
  }
  return <Modal title={`${workspace.name} · 회원사 일괄 등록`} onClose={onClose} wide><div className="form">
    {!result ? <>
      <p className="muted-copy">회원사명과 {codeLabel} 두 열의 CSV 파일을 고르거나 붙여 넣으세요. 이미 등록된 코드는 건너뛰므로 같은 목록을 주기적으로 다시 올려도 됩니다.</p>
      <div className="voice-actions">
        <label className="btn btn-secondary file-button">CSV 파일 선택<input type="file" accept=".csv,text/csv,text/plain" onChange={async e => { const file = e.target.files?.[0]; if (file) setText(await file.text()) }} /></label>
        <a className="btn btn-ghost" href={template} download="member-template.csv">양식 내려받기</a>
      </div>
      <textarea rows={8} value={text} onChange={e => setText(e.target.value)} placeholder={`회원사명,${codeLabel}\n㈜예시,123456789012`} aria-label="회원사 목록" />
      <label className="check-label"><input type="checkbox" checked={updateNames} onChange={e => setUpdateNames(e.target.checked)} /> 이미 등록된 코드의 회원사명을 이 목록 값으로 갱신</label>
      <div className="modal-actions"><span className="toolbar-count">{rows.length}행 인식</span>
        <button type="button" className="btn btn-ghost" onClick={onClose}>취소</button>
        <button type="button" className="btn btn-primary" disabled={busy || !rows.length} onClick={run}>{busy ? '등록 중…' : `${rows.length}행 등록`}</button></div>
    </> : <>
      <div className="import-summary">
        <div><small>신규 등록</small><b>{result.created}</b></div><div><small>이름 갱신</small><b>{result.updated}</b></div>
        <div><small>기존 유지</small><b>{result.unchanged}</b></div><div><small>오류</small><b className={result.failed ? 'danger-text' : ''}>{result.failed}</b></div>
      </div>
      {result.failed > 0 && <div className="table-panel import-errors"><table><thead><tr><th>행</th><th>회원사명</th><th>{codeLabel}</th><th>사유</th></tr></thead>
        <tbody>{result.items.filter(x => x.result === 'ERROR').map(x => <tr key={x.line}><td>{x.line}</td><td>{x.name}</td><td>{x.customerCode}</td><td>{x.message}</td></tr>)}</tbody></table></div>}
      <div className="modal-actions"><button type="button" className="btn btn-primary" onClick={onClose}>닫기</button></div>
    </>}
  </div></Modal>
}
