import { FormEvent, useEffect, useState } from 'react'
import { api } from '../api'
import { errorMessage } from '../App'
import { label } from '../labels'
import { Confirm, Empty, Modal, Spinner, Status } from '../components/Layout'
import { VoiceWorkspace } from '../components/VoiceWorkspace'

// Administrator side of VOC workspaces: the workspaces themselves, the preset
// that sets one up for a department in a click, and the request types with
// their SLA switch. Everything here can be edited later without a deploy.

type Notify = (m: string, e?: boolean) => void
type AdminCategory = {
  id: string; code: string; name: string; voiceType: string; responseHours: number; resolutionHours: number
  active: boolean; displayOrder: number; usedCount: number; slaEnabled: boolean; workspaceId: string; workspaceName: string
}
type Organization = { id: string; name: string; code: string; type?: string; orgType?: string }
type Preset = { code: string; name: string; description: string }
const voiceCategoryTypes = ['COMPLAINT', 'REQUEST', 'INQUIRY', 'DEFECT', 'CHURN_RISK', 'PRAISE']

function useOrganizations() {
  const [items, setItems] = useState<Organization[]>([])
  useEffect(() => { api<{ items: Organization[] }>('/api/v1/admin/organizations').then(v => setItems(v.items || [])).catch(() => {}) }, [])
  return items
}

export function VoiceAdmin({ notify }: { notify: Notify }) {
  const [workspaces, setWorkspaces] = useState<VoiceWorkspace[] | null>(null)
  const [presets, setPresets] = useState<Preset[]>([])
  const [categories, setCategories] = useState<AdminCategory[] | null>(null)
  const [editWorkspace, setEditWorkspace] = useState<null | { workspace?: VoiceWorkspace }>(null)
  const [applyPreset, setApplyPreset] = useState<Preset | null>(null)
  const [editCategory, setEditCategory] = useState<null | { category?: AdminCategory }>(null)
  const [remove, setRemove] = useState<AdminCategory | null>(null)
  const [busy, setBusy] = useState(false)
  const load = () => Promise.all([
    api<{ items: VoiceWorkspace[]; presets: Preset[] }>('/api/v1/admin/voice-workspaces').then(v => { setWorkspaces(v.items); setPresets(v.presets) }),
    api<{ items: AdminCategory[] }>('/api/v1/admin/voice-categories').then(v => setCategories(v.items)),
  ]).catch(e => notify(errorMessage(e), true))
  useEffect(() => { void load() }, [])
  async function confirmRemove() {
    if (!remove) return
    setBusy(true)
    try { const r = await api<any>(`/api/v1/admin/voice-categories/${remove.id}`, { method: 'DELETE' }); notify(r?.note || '요청 유형을 삭제했습니다.'); setRemove(null); await load() }
    catch (e) { notify(errorMessage(e), true) } finally { setBusy(false) }
  }
  if (!workspaces || !categories) return <Spinner />
  const groups = [{ id: '', name: '일반 고객 요청', isolated: false }, ...workspaces.map(w => ({ id: w.id, name: w.name, isolated: w.isolated }))]
  return <div className="settings-stack">
    <section className="panel settings-section">
      <div className="settings-title"><span>▦</span><div><h2>업무 영역</h2><p>부서별로 유형·항목·고객 코드를 따로 두고, 필요하면 다른 부서와 영업 지표에서 분리합니다.</p></div>
        <div className="heading-actions">{presets.map(p => <button key={p.code} className="btn btn-secondary" onClick={() => setApplyPreset(p)}>템플릿: {p.name}</button>)}
          <button className="btn btn-primary" onClick={() => setEditWorkspace({})}>＋ 업무 영역</button></div></div>
      {workspaces.length ? <div className="table-panel"><table><thead><tr><th>업무 영역</th><th>소유 부서</th><th>격리</th><th>지식 게이트</th><th>고객 코드</th><th>유형 · 항목</th><th>상태</th><th /></tr></thead>
        <tbody>{workspaces.map(w => <tr key={w.id}>
          <td><b>{w.name}</b><code className="table-sub">{w.code}</code></td>
          <td>{w.organizationName || '—'}</td>
          <td>{w.isolated ? <span className="workspace-flag on">부서 전용</span> : <span className="workspace-flag">공유</span>}</td>
          <td>{w.knowledgeGate ? <span className="workspace-flag on">사용</span> : '—'}</td>
          <td>{w.customerCodeLabel ? <>{w.customerCodeLabel}<small className="table-sub">{w.customerCodePattern || '형식 제한 없음'}</small></> : '—'}</td>
          <td>{w.categories.length}개 유형 · {w.fields.length}개 항목<small className="table-sub">{w.slaEnabled ? 'SLA 적용 유형 있음' : 'SLA 미적용'}</small></td>
          <td><Status value={w.active ? 'ACTIVE' : 'DISABLED'} /></td>
          <td><div className="row-menu"><button onClick={() => setEditWorkspace({ workspace: w })}>편집</button></div></td>
        </tr>)}</tbody></table></div>
        : <Empty title="업무 영역이 없습니다" description="모든 요청이 일반 고객 요청으로 처리됩니다. 부서 전용 민원 접수가 필요하면 템플릿으로 시작하세요." />}
    </section>

    <section className="panel settings-section">
      <div className="settings-title"><span>◷</span><div><h2>요청 유형 · SLA</h2><p>유형별로 응답·해결 목표를 두거나, SLA를 끄면 기한을 계산하지도 표시하지도 않습니다.</p></div>
        <div className="heading-actions"><button className="btn btn-primary" onClick={() => setEditCategory({})}>＋ 유형 추가</button></div></div>
      {groups.map(g => {
        const rows = categories.filter(c => (c.workspaceId || '') === g.id)
        if (!rows.length && g.id) return null
        return <div key={g.id || 'general'} className="category-group">
          <h3>{g.name}{g.isolated && <span className="workspace-flag on">부서 전용</span>}</h3>
          <div className="table-panel"><table><thead><tr><th>유형명 · 코드</th><th>구분</th><th>SLA</th><th>응답 목표</th><th>해결 목표</th><th>접수 건수</th><th>상태</th><th /></tr></thead>
            <tbody>{rows.map(x => <tr key={x.id}>
              <td><b>{x.name}</b><code className="table-sub">{x.code}</code></td>
              <td><Status value={x.voiceType} /></td>
              <td>{x.slaEnabled ? '적용' : <span className="muted-copy">미적용</span>}</td>
              <td>{x.slaEnabled ? `${x.responseHours}시간` : '—'}</td>
              <td>{x.slaEnabled ? (x.resolutionHours >= 24 ? `${Math.round(x.resolutionHours / 24)}일` : `${x.resolutionHours}시간`) : '—'}</td>
              <td>{x.usedCount}건</td>
              <td><Status value={x.active ? 'ACTIVE' : 'DISABLED'} /></td>
              <td><div className="row-menu"><button onClick={() => setEditCategory({ category: x })}>편집</button><button className="danger" onClick={() => setRemove(x)}>삭제</button></div></td>
            </tr>)}</tbody></table></div>
        </div>
      })}
    </section>
    {editWorkspace && <WorkspaceModal workspace={editWorkspace.workspace} onClose={() => setEditWorkspace(null)} onSaved={() => { setEditWorkspace(null); void load() }} notify={notify} />}
    {applyPreset && <PresetModal preset={applyPreset} onClose={() => setApplyPreset(null)} onApplied={() => { setApplyPreset(null); void load() }} notify={notify} />}
    {editCategory && <CategoryModal category={editCategory.category} workspaces={workspaces} onClose={() => setEditCategory(null)} onSaved={() => { setEditCategory(null); void load() }} notify={notify} />}
    {remove && <Confirm title="요청 유형 삭제" description={`${remove.name} 유형을 제거합니다. 이미 접수된 건이 있으면 이력 보존을 위해 사용 중지로 전환됩니다.`} requireText={remove.code} busy={busy} onCancel={() => setRemove(null)} onConfirm={confirmRemove} />}
  </div>
}

function OrganizationSelect({ name, value, required }: { name: string; value?: string; required?: boolean }) {
  const organizations = useOrganizations()
  return <select name={name} defaultValue={value || ''} required={required} key={organizations.length}>
    <option value="">{required ? '부서 선택' : '지정 안 함'}</option>
    {organizations.map(o => <option key={o.id} value={o.id}>{o.name} ({o.code})</option>)}
  </select>
}

function WorkspaceModal({ workspace, onClose, onSaved, notify }: { workspace?: VoiceWorkspace; onClose: () => void; onSaved: () => void; notify: Notify }) {
  const editing = Boolean(workspace?.id)
  const [busy, setBusy] = useState(false)
  const [isolated, setIsolated] = useState(workspace?.isolated ?? false)
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const f = new FormData(e.currentTarget); setBusy(true)
    const body = { code: f.get('code'), name: f.get('name'), description: f.get('description'), organizationId: f.get('organizationId'), isolated,
      knowledgeGate: f.get('knowledgeGate') === 'on', customerCodeLabel: f.get('customerCodeLabel'), customerCodePattern: f.get('customerCodePattern'),
      active: editing ? f.get('active') === 'on' : true, displayOrder: Number(f.get('displayOrder') || 100) }
    try {
      await api(editing ? `/api/v1/admin/voice-workspaces/${workspace!.id}` : '/api/v1/admin/voice-workspaces', { method: editing ? 'PUT' : 'POST', body: JSON.stringify(body) })
      notify(editing ? '업무 영역을 저장했습니다.' : '업무 영역을 만들었습니다.'); onSaved()
    } catch (err) { notify(errorMessage(err), true) } finally { setBusy(false) }
  }
  return <Modal title={editing ? `${workspace!.name} 편집` : '업무 영역 추가'} onClose={onClose} wide><form className="form" onSubmit={submit}><div className="form-grid">
    <label>이름 *<input name="name" required autoFocus defaultValue={workspace?.name || ''} placeholder="회원사 민원" /></label>
    <label>코드 *<input name="code" required pattern="[A-Z][A-Z0-9_]{1,39}" defaultValue={workspace?.code || ''} readOnly={editing} placeholder="MEMBER_SUPPORT" /><small>영문 대문자·숫자·밑줄. 만든 뒤에는 바꿀 수 없습니다.</small></label>
    <label className="span-2">설명<input name="description" defaultValue={workspace?.description || ''} /></label>
    <label>소유 부서{isolated ? ' *' : ''}<OrganizationSelect name="organizationId" value={workspace?.organizationId} required={isolated} /><small>이 부서와 하위 조직 구성원이 사용합니다.</small></label>
    <label>표시 순서<input name="displayOrder" type="number" defaultValue={workspace?.displayOrder ?? 100} /></label>
    <label className="check-label span-2"><input type="checkbox" checked={isolated} onChange={e => setIsolated(e.target.checked)} /> 부서 전용(격리) — 소유 부서 외에는 요청과 이 영역으로 등록한 고객이 보이지 않고, 이탈 위험·인텔리전스 등 영업 지표에서 제외합니다</label>
    <label className="check-label span-2"><input type="checkbox" name="knowledgeGate" defaultChecked={workspace?.knowledgeGate} /> 지식 게이트 — 해결 시 원인 근거를 필수로 받고, 검토자가 '반영'한 건만 에이전트 유사 사례 검색에 나옵니다</label>
    <label>고객 코드 이름<input name="customerCodeLabel" defaultValue={workspace?.customerCodeLabel || ''} placeholder="회원사코드" /><small>입력하면 간이 등록에서 필수가 됩니다.</small></label>
    <label>고객 코드 형식(정규식)<input name="customerCodePattern" defaultValue={workspace?.customerCodePattern || ''} placeholder="^[0-9]{12}$" /></label>
    {editing && <label className="check-label span-2"><input type="checkbox" name="active" defaultChecked={workspace!.active} /> 사용 중</label>}
  </div>
    {editing && isolated !== workspace!.isolated && <div className="alert"><b>격리 설정을 바꿉니다</b><span>저장하면 이 영역의 기존 요청과 등록 고객의 공개 범위도 즉시 함께 바뀝니다.</span></div>}
    <div className="modal-actions"><button type="button" className="btn btn-ghost" onClick={onClose}>취소</button><button className="btn btn-primary" disabled={busy}>{busy ? '저장 중…' : editing ? '변경 저장' : '업무 영역 추가'}</button></div>
  </form></Modal>
}

function PresetModal({ preset, onClose, onApplied, notify }: { preset: Preset; onClose: () => void; onApplied: () => void; notify: Notify }) {
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<{ created: string[]; skipped: string[] } | null>(null)
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const f = new FormData(e.currentTarget); setBusy(true)
    try {
      const r = await api<{ created: string[]; skipped: string[] }>(`/api/v1/admin/voice-presets/${preset.code}/apply`, { method: 'POST', body: JSON.stringify({ organizationId: f.get('organizationId') }) })
      setResult(r); notify(`템플릿을 적용했습니다. 새로 만든 항목 ${r.created.length}개.`)
    } catch (err) { notify(errorMessage(err), true) } finally { setBusy(false) }
  }
  return <Modal title={`템플릿 적용 · ${preset.name}`} onClose={result ? onApplied : onClose}>
    {!result ? <form className="form" onSubmit={submit}>
      <p className="muted-copy">{preset.description}</p>
      <label>적용할 부서 *<OrganizationSelect name="organizationId" required /><small>업무 영역의 소유 부서가 되며, 만들어지는 Role은 부서 범위(DEPARTMENT)로 설정됩니다.</small></label>
      <div className="alert"><b>이미 있는 항목은 건너뜁니다</b><span>다시 적용해도 편집한 내용은 바뀌지 않습니다. Role은 만든 뒤 사용자에게 직접 부여하세요.</span></div>
      <div className="modal-actions"><button type="button" className="btn btn-ghost" onClick={onClose}>취소</button><button className="btn btn-primary" disabled={busy}>{busy ? '적용 중…' : '적용'}</button></div>
    </form> : <div className="form">
      <b>새로 만든 항목</b><ul className="preset-result">{result.created.map(x => <li key={x}>{x}</li>)}{!result.created.length && <li>없음</li>}</ul>
      {result.skipped.length > 0 && <><b>이미 있어 건너뛴 항목</b><ul className="preset-result muted">{result.skipped.map(x => <li key={x}>{x}</li>)}</ul></>}
      <div className="alert"><b>다음 단계</b><span>사용자 · 조직 화면에서 담당자에게 '회원사 민원 담당', 검토자에게 '회원사 민원 검토자' Role을 부여하세요.</span></div>
      <div className="modal-actions"><button type="button" className="btn btn-primary" onClick={onApplied}>닫기</button></div>
    </div>}
  </Modal>
}

function CategoryModal({ category, workspaces, onClose, onSaved, notify }: { category?: AdminCategory; workspaces: VoiceWorkspace[]; onClose: () => void; onSaved: () => void; notify: Notify }) {
  const editing = Boolean(category?.id)
  const [busy, setBusy] = useState(false)
  const [sla, setSla] = useState(category?.slaEnabled ?? true)
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const f = new FormData(e.currentTarget); setBusy(true)
    const body = { code: f.get('code'), name: f.get('name'), voiceType: f.get('voiceType'), workspaceId: f.get('workspaceId') || '',
      slaEnabled: sla, responseHours: Number(f.get('responseHours') || 8), resolutionHours: Number(f.get('resolutionHours') || 72),
      active: editing ? f.get('active') === 'on' : true, displayOrder: Number(f.get('displayOrder')) }
    try {
      await api(editing ? `/api/v1/admin/voice-categories/${category!.id}` : '/api/v1/admin/voice-categories', { method: editing ? 'PUT' : 'POST', body: JSON.stringify(body) })
      notify(editing ? '요청 유형을 저장했습니다.' : '요청 유형을 추가했습니다.'); onSaved()
    } catch (err) { notify(errorMessage(err), true) } finally { setBusy(false) }
  }
  const locked = editing && category!.usedCount > 0
  return <Modal title={editing ? `${category!.name} 편집` : '고객 요청 유형 추가'} onClose={onClose}><form className="form" onSubmit={submit}><div className="form-grid">
    <label>유형명 *<input name="name" required autoFocus defaultValue={category?.name || ''} placeholder="서비스 오류 신고" /></label>
    <label>코드 *<input name="code" required pattern="[A-Za-z0-9_]+" defaultValue={category?.code || ''} readOnly={editing} placeholder="MS_SERVICE_ERROR" /></label>
    <label>업무 영역<select name="workspaceId" defaultValue={category?.workspaceId || ''} disabled={locked}>
      <option value="">일반 고객 요청</option>{workspaces.map(w => <option key={w.id} value={w.id}>{w.name}</option>)}</select>
      {locked && <><input type="hidden" name="workspaceId" value={category!.workspaceId || ''} /><small>접수된 요청이 있어 업무 영역을 바꿀 수 없습니다.</small></>}</label>
    <label>구분 *<select name="voiceType" defaultValue={category?.voiceType || 'COMPLAINT'}>{voiceCategoryTypes.map(x => <option key={x} value={x}>{label(x)}</option>)}</select></label>
    <label className="check-label span-2"><input type="checkbox" checked={sla} onChange={e => setSla(e.target.checked)} /> SLA 적용 — 끄면 응답·해결 기한을 계산하지도 표시하지도 않습니다</label>
    <label>응답 목표 (시간)<input name="responseHours" type="number" min="1" disabled={!sla} defaultValue={category?.responseHours ?? 8} /><small>접수 후 첫 응대까지</small></label>
    <label>해결 목표 (시간)<input name="resolutionHours" type="number" min="1" disabled={!sla} defaultValue={category?.resolutionHours ?? 72} /><small>접수 후 해결까지</small></label>
    <label>표시 순서<input name="displayOrder" type="number" defaultValue={category?.displayOrder ?? 100} /></label>
    {editing && <label className="check-label"><input type="checkbox" name="active" defaultChecked={category!.active} /> 사용 중</label>}
  </div>{sla && <div className="alert"><b>심각도가 기한을 더 조입니다</b><span>긴급은 응답 2시간·해결 24시간, 높음은 응답 4시간·해결 48시간을 넘지 않도록 자동 단축됩니다.</span></div>}
    <div className="modal-actions"><button type="button" className="btn btn-ghost" onClick={onClose}>취소</button><button className="btn btn-primary" disabled={busy}>{busy ? '저장 중…' : editing ? '변경 저장' : '유형 추가'}</button></div></form></Modal>
}
