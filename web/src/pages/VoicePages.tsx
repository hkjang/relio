import { FormEvent, useEffect, useState } from 'react'
import { api, date, relative } from '../api'
import { Customer, User, Version } from '../types'
import Layout, { Empty, Modal, Spinner, Status, navigate, rowProps, useDialog } from '../components/Layout'
import { initials, label } from '../labels'
import { errorMessage } from '../App'
import { SavedViews } from '../components/SavedViews'
import { ContactSelect, useContacts } from '../components/Contacts'
import { CustomerPicker, FieldInputs, KnowledgeBadge, MemberImportModal, ResolveModal, ReviewPanel, VoiceField, VoiceWorkspace, collectFields, evidenceLabel, knowledgeLabels } from '../components/VoiceWorkspace'

type Props = { path: string; user: User; version: Version; approvalEnabled: boolean; onLogout: () => void; notify: (m: string, e?: boolean) => void }

export type Voice = {
  id: string; voiceNo: string; customerId: string; customerName: string
  contactId?: string; contactName?: string; categoryId?: string; categoryName?: string
  voiceType: string; channel: string; title: string; body?: string
  severity: string; status: string; ownerId: string; ownerName: string
  occurredAt: string; responseDueAt?: string; resolutionDueAt?: string
  firstRespondedAt?: string; resolvedAt?: string; closedAt?: string
  resolution?: string; rootCause?: string; preventiveAction?: string
  satisfactionScore?: number; satisfactionComment?: string
  version: number; responseOverdue: boolean; resolutionOverdue: boolean; openDays: number
  customFields?: Record<string, unknown>; workspaceId?: string; workspaceName?: string; customerCode?: string
  causeEvidence?: string; knowledgeStatus: string; knowledgeReviewer?: string; knowledgeReviewedAt?: string; slaApplied: boolean
}
export type VoiceEvent = { id: string; eventType: string; fromStatus?: string; toStatus?: string; note?: string; actorName: string; occurredAt: string }
export type VoiceSummary = {
  open: number; overdue: number; critical: number; resolvedLast30: number; churnRisk: number
  averageResolutionHours: number; satisfactionAverage?: number; knowledgePending: number; byType: { key: string; count: number }[]
}
type Category = { id: string; name: string; voiceType: string; responseHours: number; resolutionHours: number; slaEnabled: boolean; workspaceId?: string }

const voiceTypes = ['COMPLAINT', 'REQUEST', 'INQUIRY', 'DEFECT', 'CHURN_RISK', 'PRAISE']
const channelTypes = ['PHONE', 'EMAIL', 'VISIT', 'PORTAL', 'CHAT', 'PARTNER', 'OTHER']
const severityTypes = ['LOW', 'NORMAL', 'HIGH', 'CRITICAL']
// The next states the API will accept from each state, mirroring the server's
// transition table so the UI never offers a move that will be rejected.
const nextStatuses: Record<string, string[]> = {
  RECEIVED: ['IN_REVIEW', 'IN_PROGRESS', 'REJECTED'],
  IN_REVIEW: ['IN_PROGRESS', 'PENDING_CUSTOMER', 'RESOLVED', 'REJECTED'],
  IN_PROGRESS: ['PENDING_CUSTOMER', 'RESOLVED', 'REJECTED'],
  PENDING_CUSTOMER: ['IN_PROGRESS', 'RESOLVED', 'REJECTED'],
  RESOLVED: ['CLOSED', 'IN_PROGRESS'],
  CLOSED: ['IN_PROGRESS'],
  REJECTED: ['IN_PROGRESS'],
}

type Filter = { workspace: string; voiceType: string; severity: string; status: string; view: string; knowledge: string; minAge: string }
const emptyFilter = (workspace = ''): Filter => ({ workspace, voiceType: '', severity: '', status: '', view: 'open', knowledge: '', minAge: '' })

// The list, the export and saved views all read the same query, so a download
// always reflects exactly what the user is looking at.
const listQuery = (f: Filter, limit: string) => {
  const q = new URLSearchParams({ limit })
  if (f.workspace) q.set('workspaceId', f.workspace)
  if (f.voiceType) q.set('voiceType', f.voiceType)
  if (f.severity) q.set('severity', f.severity)
  if (f.status) q.set('status', f.status)
  if (f.view === 'open') q.set('open', 'true')
  if (f.view === 'overdue') q.set('overdue', 'true')
  if (f.knowledge === 'PENDING') q.set('reviewPending', 'true')
  else if (f.knowledge) q.set('knowledgeStatus', f.knowledge)
  if (f.minAge) q.set('minAgeDays', f.minAge)
  return q.toString()
}

const dueLabel = (value?: string) => {
  if (!value) return '기한 없음'
  const hours = Math.round((new Date(value).getTime() - Date.now()) / 3600000)
  if (hours < 0) return `${Math.abs(hours)}시간 초과`
  if (hours < 24) return `${hours}시간 남음`
  return `${Math.floor(hours / 24)}일 남음`
}

export default function VoicePages(props: Props) {
  const params = new URLSearchParams(location.search)
  const prefill = { customerId: params.get('customerId') || '', title: params.get('title') || '', body: params.get('body') || '' }
  const [items, setItems] = useState<Voice[] | null>(null)
  const [summary, setSummary] = useState<VoiceSummary | null>(null)
  const [categories, setCategories] = useState<Category[]>([])
  const [workspaces, setWorkspaces] = useState<VoiceWorkspace[] | null>(null)
  const [filter, setFilter] = useState<Filter>(emptyFilter())
  const [modal, setModal] = useState(new URLSearchParams(location.search).has('new'))
  const [importing, setImporting] = useState(false)
  const [selected, setSelected] = useState<Voice | null>(null)
  const permissions = props.user.permissions || []
  const has = (p: string) => props.user.isBootstrap || permissions.includes('admin:*') || permissions.includes(p)
  const canWrite = has('voice:write')
  const canReview = has('voice:knowledge-review')
  const workspace = workspaces?.find(w => w.id === filter.workspace)
  // A workspace without SLA shows elapsed days where deadlines would be.
  const noSla = Boolean(workspace && !workspace.slaEnabled)
  const gate = Boolean(workspace?.knowledgeGate)

  const load = (next = filter) => Promise.all([
    api<{ items: Voice[] }>('/api/v1/voices?' + listQuery(next, '100')),
    api<VoiceSummary>('/api/v1/voices/summary' + (next.workspace ? '?workspaceId=' + next.workspace : '')),
  ]).then(([list, sum]) => { setItems(list.items); setSummary(sum) })
    .catch(e => props.notify(errorMessage(e), true))
  useEffect(() => {
    api<{ items: Category[] }>('/api/v1/voices/categories').then(v => setCategories(v.items)).catch(() => {})
    // A department member lands on their own workspace; everyone else on all.
    api<{ items: VoiceWorkspace[] }>('/api/v1/voices/workspaces').then(v => {
      setWorkspaces(v.items)
      const start = emptyFilter(v.items.length === 1 ? v.items[0].id : '')
      setFilter(start); void load(start)
    }).catch(() => { setWorkspaces([]); void load() })
  }, [])
  const change = (patch: Partial<Filter>) => { const next = { ...filter, ...patch }; setFilter(next); void load(next) }
  const switchWorkspace = (id: string) => { const next = emptyFilter(id); setFilter(next); void load(next) }

  return <Layout area="app" {...props} title={workspace ? workspace.name : '고객의 목소리'}
    subtitle={workspace ? (workspace.description || '업무 영역의 접수·처리 이력입니다.') : '불만, 요청, 문의와 이탈 징후를 접수부터 해결·만족도까지 추적합니다.'}
    actions={<>
      {workspace && has('customer:write') && canWrite && <button className="btn btn-secondary" onClick={() => setImporting(true)}>{workspace.customerCodeLabel ? '회원사' : '고객'} 일괄 등록</button>}
      <a className="btn btn-secondary" href={'/api/v1/voices/export?' + listQuery(filter, '200')} download>CSV 내보내기</a>
      {canWrite && <button className="btn btn-primary" onClick={() => setModal(true)}>＋ 요청 접수</button>}
    </>}>
    {!items || !summary || !workspaces ? <Spinner /> : <>
      {workspaces.length > 0 && <div className="segmented workspace-tabs" role="tablist" aria-label="업무 영역">
        <button role="tab" aria-selected={!filter.workspace} className={!filter.workspace ? 'active' : ''} onClick={() => switchWorkspace('')}>전체</button>
        {workspaces.map(w => <button key={w.id} role="tab" aria-selected={filter.workspace === w.id} className={filter.workspace === w.id ? 'active' : ''} onClick={() => switchWorkspace(w.id)}>
          {w.name}{w.isolated && <span className="workspace-lock" title="부서 전용 · 타 부서 비노출">🔒</span>}</button>)}
      </div>}
      <div className="voice-kpi-grid">
        <button className={filter.view === 'open' && !filter.minAge && !filter.knowledge ? 'active' : ''} onClick={() => change({ view: 'open', status: '', minAge: '', knowledge: '' })}>
          <span>처리 중</span><strong>{summary.open}</strong><small>미해결 건</small></button>
        {noSla
          ? <button className={filter.minAge === '14' ? 'active' : ''} onClick={() => change({ view: 'open', minAge: filter.minAge === '14' ? '' : '14', knowledge: '' })}>
            <span>14일 이상 경과</span><strong>{filter.minAge === '14' ? items.length : '보기'}</strong><small>SLA 미적용 · 경과일 기준</small></button>
          : <button className={filter.view === 'overdue' ? 'active' : ''} onClick={() => change({ view: 'overdue', status: '', minAge: '', knowledge: '' })}>
            <span>기한 초과</span><strong className={summary.overdue ? 'danger-text' : ''}>{summary.overdue}</strong><small>응답·해결 지연</small></button>}
        <button className={filter.severity === 'CRITICAL' ? 'active' : ''} onClick={() => change({ severity: filter.severity === 'CRITICAL' ? '' : 'CRITICAL', view: 'open', knowledge: '' })}>
          <span>긴급</span><strong className={summary.critical ? 'danger-text' : ''}>{summary.critical}</strong><small>최우선 대응</small></button>
        {gate
          ? <button className={filter.knowledge === 'PENDING' ? 'active' : ''} onClick={() => change({ knowledge: filter.knowledge === 'PENDING' ? '' : 'PENDING', view: 'all', minAge: '' })}>
            <span>지식 검토 대기</span><strong className={summary.knowledgePending ? 'warn-text' : ''}>{summary.knowledgePending}</strong><small>종결 · 미검토</small></button>
          : <button className={filter.voiceType === 'CHURN_RISK' ? 'active' : ''} onClick={() => change({ voiceType: filter.voiceType === 'CHURN_RISK' ? '' : 'CHURN_RISK', view: 'open' })}>
            <span>이탈 징후</span><strong className={summary.churnRisk ? 'danger-text' : ''}>{summary.churnRisk}</strong><small>갱신 위험</small></button>}
        <div><span>최근 30일 해결</span><strong>{summary.resolvedLast30}</strong><small>평균 {Math.round(summary.averageResolutionHours)}시간</small></div>
        <div><span>만족도</span><strong>{summary.satisfactionAverage ? `${summary.satisfactionAverage.toFixed(1)}` : '—'}</strong><small>5점 만점</small></div>
      </div>

      <SavedViews resource="VOICE" current={listQuery(filter, '200')} notify={props.notify}
        onApply={q => { const p = new URLSearchParams(q)
          const next: Filter = { workspace: p.get('workspaceId') || '', voiceType: p.get('voiceType') || '', severity: p.get('severity') || '', status: p.get('status') || '',
            view: p.get('overdue') === 'true' ? 'overdue' : p.get('open') === 'true' ? 'open' : 'all',
            knowledge: p.get('reviewPending') === 'true' ? 'PENDING' : p.get('knowledgeStatus') || '', minAge: p.get('minAgeDays') || '' }
          setFilter(next); void load(next) }} />
      <div className="toolbar">
        <select value={filter.view} onChange={e => change({ view: e.target.value })} aria-label="처리 상태 보기">
          <option value="open">처리 중만</option>{!noSla && <option value="overdue">기한 초과만</option>}<option value="all">전체</option>
        </select>
        {!workspace && <select value={filter.voiceType} onChange={e => change({ voiceType: e.target.value })} aria-label="요청 유형">
          <option value="">전체 유형</option>{voiceTypes.map(x => <option key={x} value={x}>{label(x)}</option>)}
        </select>}
        <select value={filter.severity} onChange={e => change({ severity: e.target.value })} aria-label="심각도">
          <option value="">전체 심각도</option>{severityTypes.map(x => <option key={x} value={x}>{label(x)}</option>)}
        </select>
        <select value={filter.minAge} onChange={e => change({ minAge: e.target.value, view: e.target.value ? 'open' : filter.view })} aria-label="경과일">
          <option value="">경과일 전체</option>{['7', '14', '30', '60'].map(d => <option key={d} value={d}>{d}일 이상 경과</option>)}
        </select>
        {gate && <select value={filter.knowledge} onChange={e => change({ knowledge: e.target.value, view: e.target.value ? 'all' : filter.view })} aria-label="지식 반영 상태">
          <option value="">지식 반영 전체</option><option value="PENDING">검토 대기(종결·미검토)</option>
          {Object.entries(knowledgeLabels).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>}
        {(filter.voiceType || filter.severity || filter.view !== 'open' || filter.minAge || filter.knowledge) &&
          <button className="btn btn-ghost" onClick={() => change({ ...emptyFilter(filter.workspace) })}>필터 초기화</button>}
        <span className="toolbar-count">{items.length}건</span>
      </div>

      <section className="panel table-panel">
        {items.length ? <table><thead><tr>
          <th>접수번호 · 제목</th><th>고객</th><th>유형</th><th>심각도</th><th>상태</th>
          {noSla ? <th>경과</th> : <><th>응답 기한</th><th>해결 기한</th></>}
          {gate && <th>지식 반영</th>}<th>담당</th>
        </tr></thead><tbody>{items.map(v => <tr key={v.id} {...rowProps(() => setSelected(v))} aria-label={`${v.title} 처리 상세 열기`} className={v.resolutionOverdue ? 'row-danger' : ''}>
          <td><b>{v.title}</b><small className="table-sub">{v.voiceNo} · {relative(v.occurredAt)} 접수{v.openDays > 0 ? ` · ${v.openDays}일 경과` : ''}</small></td>
          <td><div className="entity-cell"><span className="customer-logo">{initials(v.customerName)}</span><span><b>{v.customerName}</b><small>{v.customerCode ? `${workspace?.customerCodeLabel || '코드'} ${v.customerCode}` : v.contactName || '담당자 미지정'}</small></span></div></td>
          <td>{workspace ? <b className="category-cell">{v.categoryName || '—'}</b> : <><Status value={v.voiceType} /><small className="table-sub">{v.categoryName || label(v.channel)}</small></>}</td>
          <td><Status value={v.severity} /></td>
          <td><Status value={v.status} /></td>
          {noSla
            ? <td className={v.openDays >= 14 && !v.resolvedAt ? 'warn-text' : ''}>{v.resolvedAt ? `해결 · ${v.openDays}일` : `${v.openDays}일`}</td>
            : <><td className={v.responseOverdue ? 'danger-text' : ''}>{!v.slaApplied ? '미적용' : v.firstRespondedAt ? '응답 완료' : dueLabel(v.responseDueAt)}</td>
              <td className={v.resolutionOverdue ? 'danger-text' : ''}>{v.resolvedAt ? date(v.resolvedAt) : !v.slaApplied ? '미적용' : dueLabel(v.resolutionDueAt)}</td></>}
          {gate && <td>{v.status === 'RESOLVED' || v.status === 'CLOSED' || v.knowledgeStatus !== 'UNREVIEWED' ? <KnowledgeBadge status={v.knowledgeStatus} /> : <span className="muted-copy">—</span>}</td>}
          <td>{v.ownerName}</td>
        </tr>)}</tbody></table>
          : <Empty icon="◷" title={filter.knowledge === 'PENDING' ? '검토할 종결 건이 없습니다' : filter.view === 'open' ? '처리 중인 요청이 없습니다' : '조건에 맞는 요청이 없습니다'}
            description={noSla ? '이 업무 영역은 응답·해결 기한을 적용하지 않습니다. 경과일로 묵은 건을 관리합니다.' : '고객이 제기한 불만, 요청, 문의를 접수하면 응답·해결 기한이 자동으로 계산됩니다.'}
            action={canWrite ? <button className="btn btn-primary" onClick={() => setModal(true)}>첫 요청 접수</button> : undefined} />}
      </section>
    </>}
    {modal && workspaces && <VoiceModal prefill={prefill} categories={categories} workspaces={workspaces} defaultWorkspace={filter.workspace}
      canRegister={has('customer:write')} onClose={() => setModal(false)} onSaved={() => { setModal(false); void load() }} notify={props.notify} />}
    {importing && workspace && <MemberImportModal workspace={workspace} onClose={() => setImporting(false)} notify={props.notify} />}
    {selected && <VoiceDrawer voice={selected} workspaces={workspaces || []} canWrite={canWrite} canReview={canReview}
      onClose={() => setSelected(null)} onSaved={() => void load()} notify={props.notify} />}
  </Layout>
}

function VoiceModal({ prefill, categories, workspaces, defaultWorkspace, canRegister, onClose, onSaved, notify }: {
  prefill?: { customerId: string; title: string; body: string }; categories: Category[]; workspaces: VoiceWorkspace[]; defaultWorkspace: string
  canRegister: boolean; onClose: () => void; onSaved: () => void; notify: Props['notify']
}) {
  const [busy, setBusy] = useState(false)
  const [workspaceId, setWorkspaceId] = useState(defaultWorkspace || (workspaces.length === 1 ? workspaces[0].id : ''))
  const workspace = workspaces.find(w => w.id === workspaceId)
  const [voiceType, setVoiceType] = useState('COMPLAINT')
  const [categoryId, setCategoryId] = useState('')
  const [customer, setCustomer] = useState<Customer | null>(null)
  const [contactId, setContactId] = useState('')
  const contacts = useContacts(customer?.id || '', notify)
  useEffect(() => { setContactId('') }, [customer?.id])
  useEffect(() => { setCategoryId('') }, [workspaceId])
  useEffect(() => {
    if (prefill?.customerId) api<Customer>('/api/v1/customers/' + prefill.customerId).then(setCustomer).catch(() => {})
  }, [prefill?.customerId])
  const general = categories.filter(c => !c.workspaceId && c.voiceType === voiceType)
  const types = workspace ? workspace.categories : general
  const chosen = types.find(c => c.id === categoryId)
  const fields: VoiceField[] = workspace?.fields || []

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const f = new FormData(e.currentTarget)
    if (!customer) { notify('고객을 선택하거나 등록하세요.', true); return }
    setBusy(true)
    try {
      await api('/api/v1/voices', { method: 'POST', body: JSON.stringify({
        customerId: customer.id, contactId, categoryId,
        voiceType: workspace ? '' : voiceType, channel: f.get('channel'), title: f.get('title'), body: f.get('body'),
        severity: f.get('severity'), customFields: collectFields(f, fields, 'INTAKE'),
      }) })
      notify(chosen && !chosen.slaEnabled ? '고객 요청을 접수했습니다.' : '고객 요청을 접수했습니다. 응답·해결 기한이 자동으로 설정되었습니다.'); onSaved()
    } catch (err) { notify(errorMessage(err), true) } finally { setBusy(false) }
  }
  return <Modal title="고객 요청 접수" onClose={onClose} wide><form className="form" onSubmit={submit}>
    <div className="form-grid">
      {workspaces.length > 0 && <label className="span-2">업무 영역<select value={workspaceId} onChange={e => setWorkspaceId(e.target.value)}>
        <option value="">일반 고객 요청</option>{workspaces.map(w => <option key={w.id} value={w.id}>{w.name}</option>)}</select></label>}
      <div className="span-2 field-block"><span className="field-title">{workspace?.customerCodeLabel ? '회원사' : '고객'} *</span>
        <CustomerPicker workspace={workspace} value={customer} onChange={setCustomer} notify={notify} canRegister={canRegister && Boolean(workspace)} /></div>
      <ContactSelect label="요청 담당자" name="contactId" customerId={customer?.id || ''} contacts={contacts}
        value={contactId} onChange={setContactId} disabled={!customer} disabledHint="고객을 먼저 선택하세요"
        placeholder="미지정" notify={notify} />
      {workspace
        ? <label>유형 *<select value={categoryId} required onChange={e => setCategoryId(e.target.value)}>
          <option value="">유형 선택</option>{types.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select>
          <small>{chosen && !chosen.slaEnabled ? '이 유형은 응답·해결 기한을 적용하지 않습니다.' : '회원사가 말한 내용만으로 고를 수 있는 상위 유형입니다.'}</small></label>
        : <>
          <label>유형 *<select value={voiceType} onChange={e => setVoiceType(e.target.value)}>{voiceTypes.map(x => <option key={x} value={x}>{label(x)}</option>)}</select></label>
          <label>세부 분류<select value={categoryId} onChange={e => setCategoryId(e.target.value)}>
            <option value="">미지정</option>{general.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select>
            <small>{chosen ? (chosen.slaEnabled ? `기본 응답 ${chosen.responseHours}시간 · 해결 ${chosen.resolutionHours}시간` : '기한 미적용 유형입니다') : '분류를 고르면 기한이 자동 설정됩니다'}</small></label>
        </>}
      <label>접수 경로<select name="channel">{channelTypes.map(x => <option key={x} value={x}>{label(x)}</option>)}</select></label>
      <label>심각도<select name="severity" defaultValue="NORMAL">{severityTypes.map(x => <option key={x} value={x}>{label(x)}</option>)}</select>
        {!(chosen && !chosen.slaEnabled) && !workspace && <small>긴급·높음은 기한이 더 짧게 적용됩니다.</small>}</label>
      <FieldInputs fields={fields} phase="INTAKE" />
      <label className="span-2">제목 *<input name="title" required defaultValue={prefill?.title || ''} placeholder={workspace ? '예: 휴대폰본인확인 팝업 E1003 오류' : '예: 3차 납품 지연으로 생산 라인 중단'} /></label>
      <label className="span-2">고객이 말한 내용<textarea name="body" rows={4} defaultValue={prefill?.body || ''} placeholder="고객의 표현을 그대로 남기면 이후 원인 분석에 도움이 됩니다." /></label>
    </div>
    <div className="modal-actions"><button type="button" className="btn btn-ghost" onClick={onClose}>취소</button>
      <button className="btn btn-primary" disabled={busy}>{busy ? '접수 중…' : '접수'}</button></div>
  </form></Modal>
}

function fieldText(value: unknown) {
  if (Array.isArray(value)) return value.join(', ')
  if (typeof value === 'boolean') return value ? '예' : '아니오'
  return value === undefined || value === null || value === '' ? '—' : String(value)
}

function VoiceDrawer({ voice, workspaces, canWrite, canReview, onClose, onSaved, notify }: {
  voice: Voice; workspaces: VoiceWorkspace[]; canWrite: boolean; canReview: boolean; onClose: () => void; onSaved: () => void; notify: Props['notify']
}) {
  const surface = useDialog(onClose)
  const [detail, setDetail] = useState<{ voice: Voice; events: VoiceEvent[] } | null>(null)
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState('')
  const [resolving, setResolving] = useState(false)
  const load = () => api<{ voice: Voice; events: VoiceEvent[] }>(`/api/v1/voices/${voice.id}`).then(setDetail).catch(e => notify(errorMessage(e), true))
  useEffect(() => { void load() }, [voice.id])
  const v = detail?.voice ?? voice
  const workspace = workspaces.find(w => w.id === v.workspaceId)
  const fields = workspace?.fields || []
  const closed = v.status === 'RESOLVED' || v.status === 'CLOSED'
  const refresh = async () => { await load(); onSaved() }

  async function transition(status: string) {
    // Resolving collects the structured resolution in a form of its own.
    if (status === 'RESOLVED') { setResolving(true); return }
    setBusy(true)
    try {
      const updated = await api<Voice>(`/api/v1/voices/${v.id}`, { method: 'PUT', body: JSON.stringify({ status, note, version: v.version }) })
      notify(`상태를 ${label(status)}로 변경했습니다.${updated.knowledgeStatus === 'UNREVIEWED' && v.knowledgeStatus !== 'UNREVIEWED' ? ' 지식 반영 상태가 미검토로 돌아갔습니다.' : ''}`)
      setNote(''); await refresh()
    } catch (e) { notify(errorMessage(e), true) } finally { setBusy(false) }
  }
  async function addEvent(eventType: string) {
    if (!note.trim()) { notify('내용을 입력하세요.', true); return }
    setBusy(true)
    try {
      await api(`/api/v1/voices/${v.id}/events`, { method: 'POST', body: JSON.stringify({ eventType, note }) })
      notify(eventType === 'CUSTOMER_CONTACT' ? '고객 응대를 기록했습니다.' : eventType === 'ESCALATED' ? '상위 보고를 기록했습니다.' : '메모를 남겼습니다.'); setNote(''); await load()
    } catch (e) { notify(errorMessage(e), true) } finally { setBusy(false) }
  }
  async function saveAnalysis(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const f = new FormData(e.currentTarget); setBusy(true)
    const score = String(f.get('satisfactionScore') || '')
    try {
      await api(`/api/v1/voices/${v.id}`, { method: 'PUT', body: JSON.stringify({
        rootCause: f.get('rootCause'), preventiveAction: f.get('preventiveAction'),
        causeEvidence: f.get('causeEvidence') || '',
        satisfactionScore: score ? Number(score) : null, satisfactionComment: f.get('satisfactionComment'),
        version: v.version,
      }) })
      notify('원인과 재발 방지 내용을 저장했습니다.'); await refresh()
    } catch (err) { notify(errorMessage(err), true) } finally { setBusy(false) }
  }

  const intake = fields.filter(f => f.phase === 'INTAKE')
  const resolution = fields.filter(f => f.phase === 'RESOLUTION')
  return <div className="drawer-backdrop" onMouseDown={e => e.target === e.currentTarget && onClose()}>
    <aside ref={surface} className="drawer drawer-wide" role="dialog" aria-modal="true">
      <div className="drawer-head">
        <div><p className="eyebrow">{v.workspaceName || '고객의 목소리'} · {v.voiceNo}</p><h2>{v.title}</h2>
          <p>{v.customerName}{v.customerCode ? ` (${workspace?.customerCodeLabel || '코드'} ${v.customerCode})` : ''}{v.contactName ? ` · ${v.contactName}` : ''} · 담당 {v.ownerName}</p></div>
        <button className="icon-btn" onClick={onClose}>×</button>
      </div>
      <div className="drawer-body">
        <div className="voice-status-row">
          {workspace ? <b className="category-cell">{v.categoryName}</b> : <Status value={v.voiceType} />}<Status value={v.severity} /><Status value={v.status} />
          {(workspace?.knowledgeGate || v.knowledgeStatus !== 'UNREVIEWED') && <KnowledgeBadge status={v.knowledgeStatus} />}
          {v.responseOverdue && <span className="voice-breach">응답 기한 초과</span>}
          {v.resolutionOverdue && <span className="voice-breach">해결 기한 초과</span>}
        </div>
        {v.body && <div className="voice-quote">{v.body}</div>}
        <div className="info-list">
          <div><span>접수 경로</span><b>{label(v.channel)}</b></div>
          <div><span>{workspace ? '유형' : '세부 분류'}</span><b>{v.categoryName || '미지정'}</b></div>
          {intake.map(f => <div key={f.key}><span>{f.label}</span><b>{fieldText(v.customFields?.[f.key])}</b></div>)}
          <div><span>접수 시각</span><b>{date(v.occurredAt)}</b></div>
          {v.slaApplied
            ? <><div><span>응답 기한</span><b>{v.firstRespondedAt ? `응답 완료 (${date(v.firstRespondedAt)})` : dueLabel(v.responseDueAt)}</b></div>
              <div><span>해결 기한</span><b>{v.resolvedAt ? `해결 (${date(v.resolvedAt)})` : dueLabel(v.resolutionDueAt)}</b></div></>
            : <div><span>기한</span><b>미적용 유형{v.resolvedAt ? ` · 해결 ${date(v.resolvedAt)}` : ''}</b></div>}
          <div><span>경과</span><b>{v.openDays}일</b></div>
        </div>
        {(v.resolution || v.causeEvidence || resolution.some(f => v.customFields?.[f.key] !== undefined)) && <div className="voice-resolution">
          <b>해결 내용</b><p>{v.resolution || '—'}</p>
          <div className="info-list compact">
            {v.rootCause && <div><span>근본 원인</span><b>{v.rootCause}</b></div>}
            <div><span>원인 근거</span><b>{evidenceLabel(v.causeEvidence)}</b></div>
            {resolution.map(f => <div key={f.key}><span>{f.label}</span><b>{fieldText(v.customFields?.[f.key])}</b></div>)}
            {v.knowledgeReviewer && <div><span>지식 판정</span><b>{knowledgeLabels[v.knowledgeStatus]} · {v.knowledgeReviewer}{v.knowledgeReviewedAt ? ` · ${date(v.knowledgeReviewedAt)}` : ''}</b></div>}
          </div>
        </div>}

        {canReview && (workspace?.knowledgeGate || closed) && <ReviewPanel voice={v} onSaved={refresh} notify={notify} />}

        {canWrite && <div className="drawer-section">
          <h3>처리 기록 남기기</h3>
          <textarea rows={3} value={note} onChange={e => setNote(e.target.value)}
            placeholder={v.slaApplied ? '고객에게 안내한 내용이나 내부 확인 사항을 남기세요. 고객 응대로 기록하면 응답 기한이 충족됩니다.' : '고객에게 안내한 내용이나 내부 확인 사항을 남기세요.'} />
          <div className="voice-actions">
            <button className="btn btn-secondary" disabled={busy} onClick={() => addEvent('CUSTOMER_CONTACT')}>고객 응대 기록</button>
            <button className="btn btn-ghost" disabled={busy} onClick={() => addEvent('COMMENT')}>내부 메모</button>
            <button className="btn btn-ghost" disabled={busy} onClick={() => addEvent('ESCALATED')}>상위 보고</button>
          </div>
          <div className="voice-actions">
            {(nextStatuses[v.status] || []).map(s => <button key={s} className={s === 'RESOLVED' ? 'btn btn-primary' : 'btn btn-secondary'}
              disabled={busy} onClick={() => transition(s)}>{label(s)}로 변경</button>)}
          </div>
          {closed && v.knowledgeStatus !== 'UNREVIEWED' && <small className="muted-copy">재처리하면 지식 반영 상태가 미검토로 돌아갑니다.</small>}
        </div>}

        {canWrite && closed && <form className="drawer-section form" onSubmit={saveAnalysis}>
          <h3>원인 분석과 재발 방지</h3>
          <div className="form-grid">
            <label className="span-2">근본 원인<textarea name="rootCause" rows={2} defaultValue={v.rootCause || ''} placeholder="왜 발생했는지 사실 기준으로 기록합니다." /></label>
            {workspace?.knowledgeGate && <label>원인 근거<select name="causeEvidence" defaultValue={v.causeEvidence || ''}>
              <option value="">변경 안 함</option><option value="CONFIRMED">확인 — 객관 근거 있음</option><option value="PRESUMED">추정 — 정황상 판단</option><option value="UNIDENTIFIED">미특정 — 원인 특정 안 됨</option></select></label>}
            <label className="span-2">재발 방지 조치<textarea name="preventiveAction" rows={2} defaultValue={v.preventiveAction || ''} placeholder="같은 문제가 반복되지 않도록 바꾼 것을 기록합니다." /></label>
            <label>고객 만족도<select name="satisfactionScore" defaultValue={v.satisfactionScore ? String(v.satisfactionScore) : ''}>
              <option value="">미조사</option>{[5, 4, 3, 2, 1].map(n => <option key={n} value={n}>{n}점</option>)}</select></label>
            <label>만족도 의견<input name="satisfactionComment" defaultValue={v.satisfactionComment || ''} /></label>
          </div>
          <button className="btn btn-primary" disabled={busy}>분석 내용 저장</button>
        </form>}

        <div className="drawer-section">
          <h3>처리 이력</h3>
          {detail ? <div className="timeline large">{detail.events.map(e => <div className="timeline-item" key={e.id}>
            <span className="timeline-dot">{e.eventType === 'CUSTOMER_CONTACT' ? '☎' : e.eventType === 'RESOLVED' ? '✓' : e.eventType === 'ESCALATED' ? '!' : e.eventType === 'KNOWLEDGE_REVIEW' ? '◆' : '▤'}</span>
            <div className="timeline-card"><header><div><Status value={e.eventType} />
              {e.fromStatus && e.toStatus && <b>{label(e.fromStatus)} → {label(e.toStatus)}</b>}</div>
              <time>{date(e.occurredAt)}</time></header>
              {e.note && <p>{e.note}</p>}
              <footer><span className="avatar tiny">{initials(e.actorName, 1)}</span>{e.actorName}</footer></div>
          </div>)}</div> : <Spinner />}
        </div>
      </div>
      <div className="drawer-actions">
        <button className="btn btn-secondary" onClick={() => navigate('/app/customers/' + v.customerId)}>고객 360 열기</button>
      </div>
    </aside>
    {resolving && <ResolveModal voice={v} workspace={workspace} fields={fields} onClose={() => setResolving(false)}
      onSaved={async () => { setResolving(false); setNote(''); await refresh() }} notify={notify} />}
  </div>
}

/** VoicePanel is the customer 360 section: the post-sale half of the account. */
export function VoicePanel({ customerId, canWrite, notify }: { customerId: string; canWrite: boolean; notify: Props['notify'] }) {
  const [items, setItems] = useState<Voice[] | null>(null)
  const [summary, setSummary] = useState<VoiceSummary | null>(null)
  const load = () => Promise.all([
    api<{ items: Voice[] }>(`/api/v1/voices?customerId=${customerId}&limit=8`),
    api<VoiceSummary>(`/api/v1/voices/summary?customerId=${customerId}`),
  ]).then(([l, s]) => { setItems(l.items); setSummary(s) }).catch(() => setItems([]))
  useEffect(() => { void load() }, [customerId])
  return <section className="panel voice-panel">
    <div className="panel-head"><div><h2>고객의 목소리</h2><p>불만, 요청, 문의와 처리 상태</p></div>
      {canWrite && <button className="btn btn-sm btn-secondary" onClick={() => navigate('/app/voices?new=1')}>＋ 접수</button>}</div>
    {!items || !summary ? <Spinner /> : <>
      <div className="voice-mini-kpi">
        <div><small>처리 중</small><b>{summary.open}건</b></div>
        <div><small>기한 초과</small><b className={summary.overdue ? 'danger-text' : ''}>{summary.overdue}건</b></div>
        <div><small>이탈 징후</small><b className={summary.churnRisk ? 'danger-text' : ''}>{summary.churnRisk}건</b></div>
        <div><small>만족도</small><b>{summary.satisfactionAverage ? summary.satisfactionAverage.toFixed(1) : '—'}</b></div>
      </div>
      {items.length ? <div className="voice-list">{items.map(v => <button key={v.id} className="voice-row" onClick={() => navigate('/app/voices')}>
        <span className="voice-row-main"><b>{v.title}</b><small>{v.voiceNo} · {relative(v.occurredAt)}</small></span>
        <Status value={v.voiceType} /><Status value={v.status} />
        {(v.responseOverdue || v.resolutionOverdue) && <span className="voice-breach">기한 초과</span>}
      </button>)}</div>
        : <Empty icon="♡" title="접수된 요청이 없습니다" description="고객이 제기한 불만이나 요청을 접수하면 이곳에 이력이 쌓입니다." />}
    </>}
  </section>
}
