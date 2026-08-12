import { useEffect, useState } from 'react'
import { api, date, relative } from '../api'
import { User, Version } from '../types'
import Layout, { Empty, Spinner, Status, navigate, rowProps } from '../components/Layout'
import { label } from '../labels'
import { errorMessage } from '../App'
import { DecideRecommendation, Recommendation, Risk, Signal, RecommendationCard, severityTone } from '../components/Intelligence'

type Props = { path: string; user: User; version: Version; approvalEnabled: boolean; onLogout: () => void; notify: (m: string, e?: boolean) => void }

export default function IntelligencePages(props: Props) {
  if (props.path === '/app/recommendations') return <MyRecommendations {...props} />
  return <IntelligenceCenter {...props} />
}

const can = (user: User, permission: string) =>
  user.isBootstrap || (user.permissions || []).some(x => x === 'admin:*' || x === permission)

/** IntelligenceCenter is the whole-portfolio view: every risk and signal the
 *  user can see, worst first, so a manager can start the week at the top. */
function IntelligenceCenter(props: Props) {
  const [risks, setRisks] = useState<Risk[] | null>(null)
  const [signals, setSignals] = useState<Signal[] | null>(null)
  const [status, setStatus] = useState<any>(null)
  const [tab, setTab] = useState<'risks' | 'signals'>('risks')
  const [severity, setSeverity] = useState('')
  const [running, setRunning] = useState(false)
  const canRun = can(props.user, 'intelligence:run')

  const load = () => Promise.all([
    api<{ items: Risk[] }>(`/api/v1/risks?limit=200${severity ? `&severity=${severity}` : ''}`),
    api<{ items: Signal[] }>(`/api/v1/signals?limit=200${severity ? `&severity=${severity}` : ''}`),
    api<{ lastRun: any }>('/api/v1/intelligence/status'),
  ]).then(([r, s, st]) => { setRisks(r.items); setSignals(s.items); setStatus(st.lastRun) })
    .catch(e => props.notify(errorMessage(e), true))
  useEffect(() => { void load() }, [severity])

  async function run() {
    setRunning(true)
    try {
      const summary = await api<any>('/api/v1/intelligence/run', { method: 'POST', body: '{}' })
      props.notify(`분석 완료 · 고객 ${summary.accountsScanned}건 · 신규 신호 ${summary.signalsOpened}건 · 해소 ${summary.signalsResolved}건`)
      await load()
    } catch (e) { props.notify(errorMessage(e), true) } finally { setRunning(false) }
  }

  const counts = (items: { severity: string }[] | null) => ({
    CRITICAL: (items || []).filter(x => x.severity === 'CRITICAL').length,
    HIGH: (items || []).filter(x => x.severity === 'HIGH').length,
    MEDIUM: (items || []).filter(x => x.severity === 'MEDIUM').length,
    LOW: (items || []).filter(x => x.severity === 'LOW').length,
  })
  const riskCounts = counts(risks)

  return <Layout area="app" {...props} title="고객 위험 분석" subtitle="담당 범위에서 감지된 위험과 신호를 한곳에서 확인합니다."
    actions={<>{status?.finishedAt && <span className="date-chip">{relative(status.finishedAt)} 분석</span>}
      {canRun && <button className="btn btn-primary" onClick={run} disabled={running}>{running ? '분석 중…' : '지금 분석'}</button>}</>}>

    <div className="kpi-grid">
      <IntelKPI label="긴급 위험" value={riskCounts.CRITICAL} tone="critical" foot="90점 이상" icon="!" />
      <IntelKPI label="높은 위험" value={riskCounts.HIGH} tone="high" foot="70점 이상" icon="◆" />
      <IntelKPI label="감지된 신호" value={(signals || []).length} tone="medium" foot="해소되지 않은 신호" icon="◇" />
      <IntelKPI label="분석한 고객" value={status?.accountsScanned ?? 0} tone="low" foot={status?.finishedAt ? `${date(status.finishedAt)} 기준` : '아직 실행되지 않음'} icon="✓" />
    </div>

    <div className="toolbar">
      <div className="segmented">
        <button className={tab === 'risks' ? 'active' : ''} onClick={() => setTab('risks')}>위험 {risks?.length ?? 0}</button>
        <button className={tab === 'signals' ? 'active' : ''} onClick={() => setTab('signals')}>신호 {signals?.length ?? 0}</button>
      </div>
      <select value={severity} onChange={e => setSeverity(e.target.value)} aria-label="심각도 필터">
        <option value="">전체 심각도</option>
        {['CRITICAL', 'HIGH', 'MEDIUM', 'LOW'].map(x => <option key={x} value={x}>{label(x)}</option>)}
      </select>
    </div>

    {tab === 'risks'
      ? (!risks ? <Spinner /> : risks.length
        ? <section className="panel table-panel"><table className="intel-table"><thead><tr>
          <th className="col-score">점수</th><th className="col-severity">심각도</th><th>위험</th>
          <th className="col-account">고객</th><th className="col-type">유형</th><th className="col-when">감지</th>
        </tr></thead><tbody>
          {risks.map(risk => <tr key={risk.id} {...rowProps(() => navigate('/app/customers/' + risk.accountId))}>
            <td><span className={`score-pill tone-${severityTone(risk.severity)}`}>{risk.riskScore}</span></td>
            <td><Status value={risk.severity} /></td>
            <td className="intel-cell"><b>{risk.title}</b><small>{risk.description}</small></td>
            <td>{risk.accountName}</td>
            <td><Status value={risk.riskType} /></td>
            <td>{relative(risk.detectedAt)}</td>
          </tr>)}
        </tbody></table></section>
        : <Empty icon="◇" title="감지된 위험이 없습니다" description={status ? '현재 담당 범위의 모든 고객이 정상 범위입니다.' : '아직 분석이 실행되지 않았습니다.'} />)
      : (!signals ? <Spinner /> : signals.length
        ? <section className="panel table-panel"><table className="intel-table"><thead><tr>
          <th className="col-severity">심각도</th><th>신호</th><th className="col-account">고객</th>
          <th className="col-type">유형</th><th className="col-when">감지</th>
        </tr></thead><tbody>
          {signals.map(signal => <tr key={signal.id} {...rowProps(() => navigate('/app/customers/' + signal.accountId))}>
            <td><Status value={signal.severity} /></td>
            <td className="intel-cell"><b>{signal.title}</b><small>{signal.description}</small></td>
            <td>{signal.accountName}</td>
            <td><Status value={signal.signalType} /></td>
            <td>{relative(signal.detectedAt)}</td>
          </tr>)}
        </tbody></table></section>
        : <Empty icon="◇" title="감지된 신호가 없습니다" description="접촉 공백, 단계 정체, 계약 만료 신호가 모두 정상 범위입니다." />)}
  </Layout>
}

function IntelKPI({ label: text, value, tone, foot, icon }: { label: string; value: number; tone: string; foot: string; icon: string }) {
  return <article className={`kpi-card intel-kpi tone-${tone}`}>
    <div className="kpi-top"><span className="kpi-icon">{icon}</span></div>
    <p>{text}</p><strong>{value}건</strong><small>{foot}</small>
  </article>
}

/** MyRecommendations is the personal work queue: what the analysis says this
 *  user should do next, and the two buttons that end the decision. */
function MyRecommendations(props: Props) {
  const [items, setItems] = useState<Recommendation[] | null>(null)
  const [status, setStatus] = useState('OPEN')
  const [deciding, setDeciding] = useState<{ item: Recommendation; mode: 'accept' | 'dismiss' } | null>(null)
  const canWrite = can(props.user, 'intelligence:write')
  const load = () => api<{ items: Recommendation[] }>(`/api/v1/recommendations?mine=true&status=${status}&limit=100`)
    .then(v => setItems(v.items)).catch(e => props.notify(errorMessage(e), true))
  useEffect(() => { void load() }, [status])

  const open = (items || []).filter(x => x.status === 'OPEN')
  const overdue = open.filter(x => x.dueDate && new Date(x.dueDate) < new Date()).length

  return <Layout area="app" {...props} title="내 추천 행동" subtitle="분석이 나에게 제안하는 다음 행동입니다. 수락하면 할 일로 등록됩니다."
    actions={<select className="heading-select" value={status} onChange={e => setStatus(e.target.value)} aria-label="상태 필터">
      {[['OPEN', '처리 대기'], ['ACCEPTED', '수락함'], ['DISMISSED', '무시함'], ['ALL', '전체']].map(([v, t]) =>
        <option key={v} value={v}>{t}</option>)}
    </select>}>

    {!items ? <Spinner /> : items.length ? <>
      {status === 'OPEN' && <div className="alert intel-summary">
        <b>{open.length}건의 추천이 대기 중입니다</b>
        <span>{overdue > 0 ? `이 중 ${overdue}건은 권장 기한을 넘겼습니다.` : '권장 기한을 넘긴 항목은 없습니다.'}</span>
      </div>}
      <div className="recommendation-grid">
        {items.map(item => <RecommendationCard key={item.id} item={item} canWrite={canWrite} showAccount
          onDecide={mode => setDeciding({ item, mode })} />)}
      </div>
    </> : <Empty icon="✓" title={status === 'OPEN' ? '처리할 추천이 없습니다' : '해당 상태의 추천이 없습니다'}
      description="분석이 새로운 위험을 찾으면 여기에 추천 행동이 나타납니다." />}

    {deciding && <DecideRecommendation {...deciding} onClose={() => setDeciding(null)}
      onDone={() => { setDeciding(null); void load() }} notify={props.notify} />}
  </Layout>
}
