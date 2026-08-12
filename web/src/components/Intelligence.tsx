import { FormEvent, useEffect, useState } from 'react'
import { api, date, relative } from '../api'
import { Empty, Modal, Spinner, Status, navigate } from './Layout'
import { label } from '../labels'

// The intelligence panel. Its job is to answer three questions in the order a
// salesperson actually asks them: how bad is it, why, and what do I do now.
// Everything shown is derived and explainable — no score appears without the
// factors that produced it.

export type Signal = {
  id: string; signalType: string; sentiment: string; severity: string
  entityType: string; entityId: string; accountId: string; accountName: string
  title: string; description: string; evidence: Record<string, any>
  detectedAt: string; sourceType: string; sourceId?: string; status: string
}
export type RiskFactor = { signal: string; detail: string; points: number }
export type Risk = {
  id: string; riskType: string; entityType: string; entityId: string
  accountId: string; accountName: string; riskScore: number; severity: string
  title: string; description: string; factors: RiskFactor[]
  detectedAt: string; acceptedNote?: string; status: string
}
export type Insight = {
  id: string; accountId: string; accountName: string; insightType: string
  title: string; summary: string; evidence: string[]; confidence: number
  generatedAt: string; status: string
}
export type Recommendation = {
  id: string; accountId: string; accountName: string; opportunityId?: string
  recommendationType: string; priority: string; title: string; description: string
  dueDate?: string; sourceType: string; sourceId?: string
  assigneeId: string; assigneeName: string; status: string; taskId?: string
  dismissReason?: string; generatedAt: string
}
export type AccountIntelligence = {
  accountId: string; riskScore: number; severity: string
  signals: Signal[]; risks: Risk[]; insights: Insight[]; recommendations: Recommendation[]
  analyzedAt?: string
}
type Notify = (m: string, e?: boolean) => void

export const severityTone = (severity: string) =>
  severity === 'CRITICAL' ? 'critical' : severity === 'HIGH' ? 'high' : severity === 'MEDIUM' ? 'medium' : 'low'

/** RiskGauge is the one number the panel leads with. */
export function RiskGauge({ score, severity }: { score: number; severity: string }) {
  return <div className={`risk-gauge tone-${severityTone(severity)}`}>
    <strong>{score}</strong>
    <span>/ 100</span>
    <Status value={severity} />
  </div>
}

export function SignalRow({ signal, onIgnore }: { signal: Signal; onIgnore?: (s: Signal) => void }) {
  return <div className={`signal-row sentiment-${signal.sentiment.toLowerCase()}`}>
    <span className={`signal-dot tone-${severityTone(signal.severity)}`} aria-hidden="true" />
    <span className="signal-copy">
      <b>{signal.title}</b>
      <small>{signal.description}</small>
    </span>
    <span className="signal-meta">
      <Status value={signal.severity} />
      <small title={date(signal.detectedAt)}>{relative(signal.detectedAt)}</small>
    </span>
    {onIgnore && <button className="link-button" onClick={() => onIgnore(signal)}>무시</button>}
  </div>
}

/** IntelligencePanel is what Customer 360 and the Opportunity drawer render. */
export function IntelligencePanel({ customerId, opportunityId, canWrite, canRun, notify, compact }: {
  customerId: string; opportunityId?: string; canWrite: boolean; canRun?: boolean; notify: Notify; compact?: boolean
}) {
  const [data, setData] = useState<AccountIntelligence | null>(null)
  const [explaining, setExplaining] = useState<Risk | null>(null)
  const [deciding, setDeciding] = useState<{ item: Recommendation; mode: 'accept' | 'dismiss' } | null>(null)
  const [running, setRunning] = useState(false)
  const query = opportunityId ? `?opportunityId=${opportunityId}` : ''
  const load = () => api<AccountIntelligence>(`/api/v1/customers/${customerId}/intelligence${query}`)
    .then(setData).catch(e => notify(e instanceof Error ? e.message : '분석 결과를 불러오지 못했습니다.', true))
  useEffect(() => { void load() }, [customerId, opportunityId])

  async function run() {
    setRunning(true)
    try {
      const summary = await api<any>('/api/v1/intelligence/run', { method: 'POST', body: '{}' })
      notify(`분석 완료 · 신규 신호 ${summary.signalsOpened}건 · 해소 ${summary.signalsResolved}건`)
      await load()
    } catch (e) { notify(e instanceof Error ? e.message : '분석을 실행하지 못했습니다.', true) } finally { setRunning(false) }
  }

  async function ignore(signal: Signal) {
    try { await api(`/api/v1/signals/${signal.id}/ignore`, { method: 'POST' }); notify('신호를 무시 처리했습니다.'); void load() }
    catch (e) { notify(e instanceof Error ? e.message : '처리하지 못했습니다.', true) }
  }

  if (!data) return <section className="panel intelligence-panel"><Spinner /></section>
  const nothing = !data.signals.length && !data.risks.length && !data.recommendations.length
  return <section className="panel intelligence-panel">
    <div className="panel-head">
      <div><h2>위험 · 신호 분석</h2>
        <p>{data.analyzedAt ? `${relative(data.analyzedAt)} 분석 · 규칙 기반` : '아직 분석이 실행되지 않았습니다'}</p></div>
      <span className="panel-head-actions">
        {canRun && (!data.analyzedAt || nothing) && <button className="btn btn-sm btn-secondary" onClick={run} disabled={running}>{running ? '분석 중…' : '지금 분석'}</button>}
        {!nothing && <RiskGauge score={data.riskScore} severity={data.severity} />}
      </span>
    </div>

    {nothing ? <Empty icon="◇" title="지금은 감지된 위험이 없습니다"
      description={data.analyzedAt ? '접촉 공백, 단계 정체, 미해결 요청, 계약 만료 신호가 모두 정상 범위입니다.' : '분석이 실행되면 이 고객의 신호와 위험이 여기에 표시됩니다.'} /> : <>

      {data.risks.length > 0 && <div className="intel-block">
        <h3>위험</h3>
        <div className="risk-list">{data.risks.map(risk => <div key={risk.id} className={`risk-row tone-${severityTone(risk.severity)}`}>
          <span className="risk-score">{risk.riskScore}</span>
          <span className="risk-copy"><b>{risk.title}</b><small>{risk.description}</small>
            {risk.status === 'ACCEPTED' && <em className="accepted-tag">감수 처리됨{risk.acceptedNote ? ` · ${risk.acceptedNote}` : ''}</em>}</span>
          <button className="btn btn-sm btn-ghost" onClick={() => setExplaining(risk)}>근거 보기</button>
        </div>)}</div>
      </div>}

      {data.signals.length > 0 && <div className="intel-block">
        <h3>신호 <em>{data.signals.length}건</em></h3>
        <div className="signal-list">{data.signals.slice(0, compact ? 4 : 20).map(signal =>
          <SignalRow key={signal.id} signal={signal} onIgnore={canWrite ? ignore : undefined} />)}</div>
      </div>}

      {data.insights.length > 0 && <div className="intel-block">
        <h3>분석</h3>
        {data.insights.map(insight => <div className="insight-card" key={insight.id}>
          <b>{insight.title}</b>
          <p>{insight.summary}</p>
          <ul>{insight.evidence.map((line, i) => <li key={i}>{line}</li>)}</ul>
          <small>신뢰도 {insight.confidence}% · {relative(insight.generatedAt)}</small>
        </div>)}
      </div>}

      {data.recommendations.length > 0 && <div className="intel-block">
        <h3>추천 행동</h3>
        <div className="recommendation-list">{data.recommendations.map(item =>
          <RecommendationCard key={item.id} item={item} canWrite={canWrite}
            onDecide={mode => setDeciding({ item, mode })} />)}</div>
      </div>}
    </>}

    {explaining && <ExplainRisk risk={explaining} onClose={() => setExplaining(null)} onAccepted={() => { setExplaining(null); void load() }} canWrite={canWrite} notify={notify} />}
    {deciding && <DecideRecommendation {...deciding} onClose={() => setDeciding(null)}
      onDone={() => { setDeciding(null); void load() }} notify={notify} />}
  </section>
}

export function RecommendationCard({ item, canWrite, onDecide, showAccount }: {
  item: Recommendation; canWrite: boolean
  onDecide: (mode: 'accept' | 'dismiss') => void
  showAccount?: boolean
}) {
  const overdue = item.status === 'OPEN' && item.dueDate && new Date(item.dueDate) < new Date()
  return <div className={`recommendation-card priority-${item.priority.toLowerCase()}`}>
    <div className="recommendation-head">
      <Status value={item.priority} />
      {showAccount && <button className="link-button" onClick={() => navigate('/app/customers/' + item.accountId)}>{item.accountName}</button>}
      {item.dueDate && <small className={overdue ? 'danger-text' : ''}>{overdue ? '기한 초과 · ' : ''}{date(item.dueDate)}까지</small>}
      {item.status !== 'OPEN' && <Status value={item.status} />}
    </div>
    <b>{item.title}</b>
    <p>{item.description}</p>
    {item.status === 'OPEN' && canWrite && <div className="recommendation-actions">
      <button className="btn btn-sm btn-primary" onClick={() => onDecide('accept')}>수락하고 할 일 생성</button>
      <button className="btn btn-sm btn-ghost" onClick={() => onDecide('dismiss')}>무시</button>
    </div>}
    {item.status === 'ACCEPTED' && <small className="decided-note">할 일로 전환됨 · 담당 {item.assigneeName}</small>}
    {item.status === 'DISMISSED' && item.dismissReason && <small className="decided-note">무시 사유 · {item.dismissReason}</small>}
  </div>
}

function ExplainRisk({ risk, onClose, onAccepted, canWrite, notify }: {
  risk: Risk; onClose: () => void; onAccepted: () => void; canWrite: boolean; notify: Notify
}) {
  const [detail, setDetail] = useState<any>(null)
  const [accepting, setAccepting] = useState(false)
  useEffect(() => { api(`/api/v1/risks/${risk.id}/explain`).then(setDetail).catch(() => setDetail({ reasons: [], signals: [] })) }, [risk.id])
  async function accept(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const note = String(new FormData(e.currentTarget).get('note') || '')
    try { await api(`/api/v1/risks/${risk.id}/accept`, { method: 'POST', body: JSON.stringify({ note }) })
      notify('위험을 감수 처리했습니다.'); onAccepted() }
    catch (err) { notify(err instanceof Error ? err.message : '처리하지 못했습니다.', true) }
  }
  return <Modal title="위험 점수 근거" onClose={onClose} wide>
    <div className="form">
      <div className="explain-head">
        <RiskGauge score={risk.riskScore} severity={risk.severity} />
        <div><b>{risk.title}</b><p>{label(risk.riskType)} · {risk.accountName}</p></div>
      </div>
      {!detail ? <Spinner /> : <>
        <h3>점수를 만든 요인</h3>
        <div className="factor-list">{risk.factors.map((factor, i) => <div key={i} className={factor.points < 0 ? 'factor negative-points' : 'factor'}>
          <span>{factor.detail}</span><b>{factor.points > 0 ? '+' : ''}{factor.points}점</b>
        </div>)}
          <div className="factor total"><span>합계 (최대 100)</span><b>{risk.riskScore}점</b></div>
        </div>
        <p className="muted-copy">40점 이상 보통, 70점 이상 높음, 90점 이상 긴급으로 분류합니다.</p>
        {detail.signals?.length > 0 && <>
          <h3>관련 신호</h3>
          <div className="signal-list">{detail.signals.map((signal: Signal) => <SignalRow key={signal.id} signal={signal} />)}</div>
        </>}
      </>}
      {canWrite && risk.status !== 'ACCEPTED' && (accepting
        ? <form onSubmit={accept} className="accept-form">
          <label>감수 사유 *<input name="note" required autoFocus placeholder="예: 고객 요청으로 일정 조정에 합의함" /></label>
          <div className="modal-actions"><button type="button" className="btn btn-ghost" onClick={() => setAccepting(false)}>취소</button>
            <button className="btn btn-primary">위험 감수</button></div>
        </form>
        : <div className="modal-actions"><button className="btn btn-ghost" onClick={() => setAccepting(true)}>이 위험을 감수</button>
          <button className="btn btn-primary" onClick={onClose}>닫기</button></div>)}
    </div>
  </Modal>
}

export function DecideRecommendation({ item, mode, onClose, onDone, notify }: {
  item: Recommendation; mode: 'accept' | 'dismiss'; onClose: () => void; onDone: () => void; notify: Notify
}) {
  const [busy, setBusy] = useState(false)
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const f = new FormData(e.currentTarget); setBusy(true)
    try {
      if (mode === 'accept') {
        await api(`/api/v1/recommendations/${item.id}/accept`, { method: 'POST', body: JSON.stringify({ dueDate: f.get('dueDate') }) })
        notify('추천을 수락하고 할 일을 생성했습니다.')
      } else {
        await api(`/api/v1/recommendations/${item.id}/dismiss`, { method: 'POST', body: JSON.stringify({ reason: f.get('reason') }) })
        notify('추천을 무시 처리했습니다.')
      }
      onDone()
    } catch (err) { notify(err instanceof Error ? err.message : '처리하지 못했습니다.', true) } finally { setBusy(false) }
  }
  return <Modal title={mode === 'accept' ? '추천 수락' : '추천 무시'} onClose={onClose}>
    <form className="form" onSubmit={submit}>
      <div className="recommendation-preview"><b>{item.title}</b><p>{item.description}</p><small>{item.accountName}</small></div>
      {mode === 'accept'
        ? <>
          <label>완료 기한<input name="dueDate" type="date" defaultValue={item.dueDate ? item.dueDate.slice(0, 10) : ''} /></label>
          <p className="muted-copy">수락하면 {item.assigneeName}님의 할 일로 등록되어 오늘 할 일 큐에 나타납니다.</p>
        </>
        : <>
          <label>무시 사유 *<input name="reason" required autoFocus placeholder="예: 이미 고객과 협의 완료" /></label>
          <p className="muted-copy">사유를 남겨야 같은 판단이 반복될 때 규칙을 조정할 수 있습니다.</p>
        </>}
      <div className="modal-actions"><button type="button" className="btn btn-ghost" onClick={onClose}>취소</button>
        <button className="btn btn-primary" disabled={busy}>{busy ? '처리 중…' : mode === 'accept' ? '수락하고 할 일 생성' : '무시'}</button></div>
    </form>
  </Modal>
}
