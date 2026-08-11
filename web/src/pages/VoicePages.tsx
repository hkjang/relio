import { FormEvent, useEffect, useState } from 'react'
import { api, date, relative } from '../api'
import { Customer, User, Version } from '../types'
import Layout, { Empty, Modal, Spinner, Status, navigate } from '../components/Layout'
import { initials, label } from '../labels'
import { errorMessage } from '../App'

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
}
export type VoiceEvent = { id: string; eventType: string; fromStatus?: string; toStatus?: string; note?: string; actorName: string; occurredAt: string }
export type VoiceSummary = {
  open: number; overdue: number; critical: number; resolvedLast30: number; churnRisk: number
  averageResolutionHours: number; satisfactionAverage?: number; byType: { key: string; count: number }[]
}
type Category = { id: string; name: string; voiceType: string; responseHours: number; resolutionHours: number }

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

const dueLabel = (value?: string) => {
  if (!value) return '기한 없음'
  const hours = Math.round((new Date(value).getTime() - Date.now()) / 3600000)
  if (hours < 0) return `${Math.abs(hours)}시간 초과`
  if (hours < 24) return `${hours}시간 남음`
  return `${Math.floor(hours / 24)}일 남음`
}

export default function VoicePages(props: Props) {
  const [items, setItems] = useState<Voice[] | null>(null)
  const [summary, setSummary] = useState<VoiceSummary | null>(null)
  const [categories, setCategories] = useState<Category[]>([])
  const [customers, setCustomers] = useState<Customer[]>([])
  const [filter, setFilter] = useState({ voiceType: '', severity: '', status: '', view: 'open' })
  const [modal, setModal] = useState(new URLSearchParams(location.search).has('new'))
  const [selected, setSelected] = useState<Voice | null>(null)

  const load = (next = filter) => {
    const q = new URLSearchParams({ limit: '100' })
    if (next.voiceType) q.set('voiceType', next.voiceType)
    if (next.severity) q.set('severity', next.severity)
    if (next.status) q.set('status', next.status)
    if (next.view === 'open') q.set('open', 'true')
    if (next.view === 'overdue') q.set('overdue', 'true')
    return Promise.all([
      api<{ items: Voice[] }>('/api/v1/voices?' + q.toString()),
      api<VoiceSummary>('/api/v1/voices/summary'),
    ]).then(([list, sum]) => { setItems(list.items); setSummary(sum) })
      .catch(e => props.notify(errorMessage(e), true))
  }
  useEffect(() => {
    void load()
    api<{ items: Category[] }>('/api/v1/voices/categories').then(v => setCategories(v.items)).catch(() => {})
    api<{ items: Customer[] }>('/api/v1/customers?limit=200').then(v => setCustomers(v.items)).catch(() => {})
  }, [])
  const change = (patch: Partial<typeof filter>) => { const next = { ...filter, ...patch }; setFilter(next); void load(next) }

  const canWrite = props.user.isBootstrap || (props.user.permissions || []).some(p => p === 'admin:*' || p === 'voice:write')

  return <Layout area="app" {...props} title="고객의 목소리" subtitle="불만, 요청, 문의와 이탈 징후를 접수부터 해결·만족도까지 추적합니다."
    actions={canWrite ? <button className="btn btn-primary" onClick={() => setModal(true)}>＋ 요청 접수</button> : undefined}>
    {!items || !summary ? <Spinner /> : <>
      <div className="voice-kpi-grid">
        <button className={filter.view === 'open' ? 'active' : ''} onClick={() => change({ view: 'open', status: '' })}>
          <span>처리 중</span><strong>{summary.open}</strong><small>미해결 건</small></button>
        <button className={filter.view === 'overdue' ? 'active' : ''} onClick={() => change({ view: 'overdue', status: '' })}>
          <span>기한 초과</span><strong className={summary.overdue ? 'danger-text' : ''}>{summary.overdue}</strong><small>응답·해결 지연</small></button>
        <button className={filter.severity === 'CRITICAL' ? 'active' : ''} onClick={() => change({ severity: filter.severity === 'CRITICAL' ? '' : 'CRITICAL', view: 'open' })}>
          <span>긴급</span><strong className={summary.critical ? 'danger-text' : ''}>{summary.critical}</strong><small>최우선 대응</small></button>
        <button className={filter.voiceType === 'CHURN_RISK' ? 'active' : ''} onClick={() => change({ voiceType: filter.voiceType === 'CHURN_RISK' ? '' : 'CHURN_RISK', view: 'open' })}>
          <span>이탈 징후</span><strong className={summary.churnRisk ? 'danger-text' : ''}>{summary.churnRisk}</strong><small>갱신 위험</small></button>
        <div><span>최근 30일 해결</span><strong>{summary.resolvedLast30}</strong><small>평균 {Math.round(summary.averageResolutionHours)}시간</small></div>
        <div><span>만족도</span><strong>{summary.satisfactionAverage ? `${summary.satisfactionAverage.toFixed(1)}` : '—'}</strong><small>5점 만점</small></div>
      </div>

      <div className="toolbar">
        <select value={filter.view} onChange={e => change({ view: e.target.value })} aria-label="처리 상태 보기">
          <option value="open">처리 중만</option><option value="overdue">기한 초과만</option><option value="all">전체</option>
        </select>
        <select value={filter.voiceType} onChange={e => change({ voiceType: e.target.value })} aria-label="요청 유형">
          <option value="">전체 유형</option>{voiceTypes.map(x => <option key={x} value={x}>{label(x)}</option>)}
        </select>
        <select value={filter.severity} onChange={e => change({ severity: e.target.value })} aria-label="심각도">
          <option value="">전체 심각도</option>{severityTypes.map(x => <option key={x} value={x}>{label(x)}</option>)}
        </select>
        {(filter.voiceType || filter.severity || filter.view !== 'open') &&
          <button className="btn btn-ghost" onClick={() => change({ voiceType: '', severity: '', status: '', view: 'open' })}>필터 초기화</button>}
        <span className="toolbar-count">{items.length}건</span>
      </div>

      <section className="panel table-panel">
        {items.length ? <table><thead><tr>
          <th>접수번호 · 제목</th><th>고객</th><th>유형</th><th>심각도</th><th>상태</th><th>응답 기한</th><th>해결 기한</th><th>담당</th>
        </tr></thead><tbody>{items.map(v => <tr key={v.id} onClick={() => setSelected(v)} className={v.resolutionOverdue ? 'row-danger' : ''}>
          <td><b>{v.title}</b><small className="table-sub">{v.voiceNo} · {relative(v.occurredAt)} 접수{v.openDays > 0 ? ` · ${v.openDays}일 경과` : ''}</small></td>
          <td><div className="entity-cell"><span className="customer-logo">{initials(v.customerName)}</span><span><b>{v.customerName}</b><small>{v.contactName || '담당자 미지정'}</small></span></div></td>
          <td><Status value={v.voiceType} /><small className="table-sub">{v.categoryName || label(v.channel)}</small></td>
          <td><Status value={v.severity} /></td>
          <td><Status value={v.status} /></td>
          <td className={v.responseOverdue ? 'danger-text' : ''}>{v.firstRespondedAt ? '응답 완료' : dueLabel(v.responseDueAt)}</td>
          <td className={v.resolutionOverdue ? 'danger-text' : ''}>{v.resolvedAt ? date(v.resolvedAt) : dueLabel(v.resolutionDueAt)}</td>
          <td>{v.ownerName}</td>
        </tr>)}</tbody></table>
          : <Empty icon="◷" title={filter.view === 'open' ? '처리 중인 요청이 없습니다' : '조건에 맞는 요청이 없습니다'}
            description="고객이 제기한 불만, 요청, 문의를 접수하면 응답·해결 기한이 자동으로 계산됩니다."
            action={canWrite ? <button className="btn btn-primary" onClick={() => setModal(true)}>첫 요청 접수</button> : undefined} />}
      </section>
    </>}
    {modal && <VoiceModal categories={categories} customers={customers} onClose={() => setModal(false)}
      onSaved={() => { setModal(false); void load() }} notify={props.notify} />}
    {selected && <VoiceDrawer voice={selected} categories={categories} canWrite={canWrite}
      onClose={() => setSelected(null)} onSaved={() => { setSelected(null); void load() }} notify={props.notify} />}
  </Layout>
}

function VoiceModal({ categories, customers, onClose, onSaved, notify }: { categories: Category[]; customers: Customer[]; onClose: () => void; onSaved: () => void; notify: Props['notify'] }) {
  const [busy, setBusy] = useState(false)
  const [voiceType, setVoiceType] = useState('COMPLAINT')
  const [customerId, setCustomerId] = useState('')
  const [contacts, setContacts] = useState<{ id: string; name: string; title?: string }[]>([])
  const matching = categories.filter(c => c.voiceType === voiceType)
  useEffect(() => {
    if (!customerId) { setContacts([]); return }
    api<{ items: any[] }>(`/api/v1/contacts?customerId=${customerId}`).then(v => setContacts(v.items)).catch(() => setContacts([]))
  }, [customerId])
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const f = new FormData(e.currentTarget); setBusy(true)
    try {
      await api('/api/v1/voices', { method: 'POST', body: JSON.stringify({
        customerId: f.get('customerId'), contactId: f.get('contactId'), categoryId: f.get('categoryId'),
        voiceType, channel: f.get('channel'), title: f.get('title'), body: f.get('body'),
        severity: f.get('severity'), customFields: {},
      }) })
      notify('고객 요청을 접수했습니다. 응답·해결 기한이 자동으로 설정되었습니다.'); onSaved()
    } catch (err) { notify(errorMessage(err), true) } finally { setBusy(false) }
  }
  const selectedCategory = matching[0]
  return <Modal title="고객 요청 접수" onClose={onClose} wide><form className="form" onSubmit={submit}>
    <div className="form-grid">
      <label>고객 *<select name="customerId" required value={customerId} onChange={e => setCustomerId(e.target.value)}>
        <option value="">고객 선택</option>{customers.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></label>
      <label>요청 담당자<select name="contactId" disabled={!contacts.length}>
        <option value="">미지정</option>{contacts.map(c => <option key={c.id} value={c.id}>{c.name}{c.title ? ` · ${c.title}` : ''}</option>)}</select>
        <small>{customerId ? `${contacts.length}명 등록됨` : '고객을 먼저 선택하세요'}</small></label>
      <label>유형 *<select value={voiceType} onChange={e => setVoiceType(e.target.value)}>{voiceTypes.map(x => <option key={x} value={x}>{label(x)}</option>)}</select></label>
      <label>세부 분류<select name="categoryId">
        <option value="">미지정</option>{matching.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select>
        <small>{selectedCategory ? `기본 응답 ${selectedCategory.responseHours}시간 · 해결 ${selectedCategory.resolutionHours}시간` : '분류를 고르면 기한이 자동 설정됩니다'}</small></label>
      <label>접수 경로<select name="channel">{channelTypes.map(x => <option key={x} value={x}>{label(x)}</option>)}</select></label>
      <label>심각도<select name="severity" defaultValue="NORMAL">{severityTypes.map(x => <option key={x} value={x}>{label(x)}</option>)}</select>
        <small>긴급·높음은 기한이 더 짧게 적용됩니다.</small></label>
      <label className="span-2">제목 *<input name="title" required autoFocus placeholder="예: 3차 납품 지연으로 생산 라인 중단" /></label>
      <label className="span-2">고객이 말한 내용<textarea name="body" rows={4} placeholder="고객의 표현을 그대로 남기면 이후 원인 분석에 도움이 됩니다." /></label>
    </div>
    <div className="modal-actions"><button type="button" className="btn btn-ghost" onClick={onClose}>취소</button>
      <button className="btn btn-primary" disabled={busy}>{busy ? '접수 중…' : '접수'}</button></div>
  </form></Modal>
}

function VoiceDrawer({ voice, categories, canWrite, onClose, onSaved, notify }: { voice: Voice; categories: Category[]; canWrite: boolean; onClose: () => void; onSaved: () => void; notify: Props['notify'] }) {
  const [detail, setDetail] = useState<{ voice: Voice; events: VoiceEvent[] } | null>(null)
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState('')
  const load = () => api<{ voice: Voice; events: VoiceEvent[] }>(`/api/v1/voices/${voice.id}`).then(setDetail).catch(e => notify(errorMessage(e), true))
  useEffect(() => { void load() }, [voice.id])
  const v = detail?.voice ?? voice

  async function transition(status: string) {
    // Resolving requires a resolution, so collect it in the same step.
    let resolution = ''
    if (status === 'RESOLVED' && !v.resolution) {
      resolution = prompt('해결 내용을 입력하세요. 고객에게 무엇을 어떻게 처리했는지 남깁니다.') || ''
      if (!resolution.trim()) return
    }
    setBusy(true)
    try {
      await api(`/api/v1/voices/${v.id}`, { method: 'PUT', body: JSON.stringify({ status, resolution, note, version: v.version }) })
      notify(`상태를 ${label(status)}로 변경했습니다.`); setNote(''); await load(); onSaved()
    } catch (e) { notify(errorMessage(e), true) } finally { setBusy(false) }
  }
  async function addEvent(eventType: string) {
    if (!note.trim()) { notify('내용을 입력하세요.', true); return }
    setBusy(true)
    try {
      await api(`/api/v1/voices/${v.id}/events`, { method: 'POST', body: JSON.stringify({ eventType, note }) })
      notify(eventType === 'CUSTOMER_CONTACT' ? '고객 응대를 기록했습니다.' : '메모를 남겼습니다.'); setNote(''); await load()
    } catch (e) { notify(errorMessage(e), true) } finally { setBusy(false) }
  }
  async function saveAnalysis(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const f = new FormData(e.currentTarget); setBusy(true)
    const score = String(f.get('satisfactionScore') || '')
    try {
      await api(`/api/v1/voices/${v.id}`, { method: 'PUT', body: JSON.stringify({
        rootCause: f.get('rootCause'), preventiveAction: f.get('preventiveAction'),
        satisfactionScore: score ? Number(score) : null, satisfactionComment: f.get('satisfactionComment'),
        version: v.version,
      }) })
      notify('원인과 재발 방지 내용을 저장했습니다.'); await load(); onSaved()
    } catch (err) { notify(errorMessage(err), true) } finally { setBusy(false) }
  }

  return <div className="drawer-backdrop" onMouseDown={e => e.target === e.currentTarget && onClose()}>
    <aside className="drawer drawer-wide">
      <div className="drawer-head">
        <div><p className="eyebrow">고객의 목소리 · {v.voiceNo}</p><h2>{v.title}</h2>
          <p>{v.customerName}{v.contactName ? ` · ${v.contactName}` : ''} · 담당 {v.ownerName}</p></div>
        <button className="icon-btn" onClick={onClose}>×</button>
      </div>
      <div className="drawer-body">
        <div className="voice-status-row">
          <Status value={v.voiceType} /><Status value={v.severity} /><Status value={v.status} />
          {v.responseOverdue && <span className="voice-breach">응답 기한 초과</span>}
          {v.resolutionOverdue && <span className="voice-breach">해결 기한 초과</span>}
        </div>
        {v.body && <div className="voice-quote">{v.body}</div>}
        <div className="info-list">
          <div><span>접수 경로</span><b>{label(v.channel)}</b></div>
          <div><span>세부 분류</span><b>{v.categoryName || '미지정'}</b></div>
          <div><span>접수 시각</span><b>{date(v.occurredAt)}</b></div>
          <div><span>응답 기한</span><b>{v.firstRespondedAt ? `응답 완료 (${date(v.firstRespondedAt)})` : dueLabel(v.responseDueAt)}</b></div>
          <div><span>해결 기한</span><b>{v.resolvedAt ? `해결 (${date(v.resolvedAt)})` : dueLabel(v.resolutionDueAt)}</b></div>
          <div><span>경과</span><b>{v.openDays}일</b></div>
        </div>
        {v.resolution && <div className="voice-resolution"><b>해결 내용</b><p>{v.resolution}</p></div>}

        {canWrite && <div className="drawer-section">
          <h3>처리 기록 남기기</h3>
          <textarea rows={3} value={note} onChange={e => setNote(e.target.value)}
            placeholder="고객에게 안내한 내용이나 내부 확인 사항을 남기세요. 고객 응대로 기록하면 응답 기한이 충족됩니다." />
          <div className="voice-actions">
            <button className="btn btn-secondary" disabled={busy} onClick={() => addEvent('CUSTOMER_CONTACT')}>고객 응대 기록</button>
            <button className="btn btn-ghost" disabled={busy} onClick={() => addEvent('COMMENT')}>내부 메모</button>
            <button className="btn btn-ghost" disabled={busy} onClick={() => addEvent('ESCALATED')}>상위 보고</button>
          </div>
          <div className="voice-actions">
            {(nextStatuses[v.status] || []).map(s => <button key={s} className={s === 'RESOLVED' ? 'btn btn-primary' : 'btn btn-secondary'}
              disabled={busy} onClick={() => transition(s)}>{label(s)}로 변경</button>)}
          </div>
        </div>}

        {canWrite && (v.status === 'RESOLVED' || v.status === 'CLOSED') && <form className="drawer-section form" onSubmit={saveAnalysis}>
          <h3>원인 분석과 재발 방지</h3>
          <div className="form-grid">
            <label className="span-2">근본 원인<textarea name="rootCause" rows={2} defaultValue={v.rootCause || ''} placeholder="왜 발생했는지 사실 기준으로 기록합니다." /></label>
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
            <span className="timeline-dot">{e.eventType === 'CUSTOMER_CONTACT' ? '☎' : e.eventType === 'RESOLVED' ? '✓' : e.eventType === 'ESCALATED' ? '!' : '▤'}</span>
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
