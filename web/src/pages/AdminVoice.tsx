import { FormEvent, useEffect, useState } from 'react'
import { DataTable } from '../components/DataTable'
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
type AdminUser = { id: string; username: string; displayName: string; organizationId: string; organizationName: string; active: boolean }
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
      {workspaces.length ? <div className="table-panel"><DataTable rows={workspaces} label="업무 영역" searchValue={w=>[w.name,w.code,w.organizationName].join(' ')} columns={[{key:'name',label:'업무 영역',value:w=>w.name}, {key:'org',label:'소유 부서',value:w=>w.organizationName}, {key:'isolated',label:'격리',value:w=>w.isolated}, {key:'gate',label:'지식 게이트',value:w=>w.knowledgeGate}, {key:'code',label:'고객 코드',value:w=>w.customerCodeLabel}, {key:'count',label:'유형 · 항목',value:w=>w.categories.length,initialDirection:'desc'}, {key:'active',label:'상태',value:w=>w.active,initialDirection:'desc'}, {key:'actions',label:'관리'}]} renderRow={w => <tr key={w.id}>
          <td><b>{w.name}</b><code className="table-sub">{w.code}</code></td>
          <td>{w.organizationName || '—'}</td>
          <td>{w.isolated ? <span className="workspace-flag on">부서 전용</span> : <span className="workspace-flag">공유</span>}</td>
          <td>{w.knowledgeGate ? <span className="workspace-flag on">사용</span> : '—'}</td>
          <td>{w.customerCodeLabel ? <>{w.customerCodeLabel}<small className="table-sub">{w.customerCodePattern || '형식 제한 없음'} · {w.customerCodeUniqueScope === 'WORKSPACE' ? '영역별 중복 검사' : '전체 중복 검사'}</small></> : '—'}</td>
          <td>{w.categories.length}개 유형 · {w.fields.length}개 항목<small className="table-sub">{w.slaEnabled ? 'SLA 적용 유형 있음' : 'SLA 미적용'}</small></td>
          <td><Status value={w.active ? 'ACTIVE' : 'DISABLED'} /></td>
          <td><div className="row-menu"><button onClick={() => setEditWorkspace({ workspace: w })}>편집</button></div></td>
        </tr>} /></div>
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
          <div className="table-panel"><DataTable rows={rows} label="요청 유형" searchValue={x=>[x.name,x.code,label(x.voiceType)].join(' ')} columns={[{key:'name',label:'유형명 · 코드',value:x=>x.name}, {key:'type',label:'구분',value:x=>label(x.voiceType)}, {key:'sla',label:'SLA',value:x=>x.slaEnabled}, {key:'response',label:'응답 목표',value:x=>x.slaEnabled?x.responseHours:null,align:'right'}, {key:'resolution',label:'해결 목표',value:x=>x.slaEnabled?x.resolutionHours:null,align:'right'}, {key:'count',label:'접수 건수',value:x=>x.usedCount,align:'right',initialDirection:'desc'}, {key:'active',label:'상태',value:x=>x.active,initialDirection:'desc'}, {key:'actions',label:'관리'}]} renderRow={x => <tr key={x.id}>
              <td><b>{x.name}</b><code className="table-sub">{x.code}</code></td>
              <td><Status value={x.voiceType} /></td>
              <td>{x.slaEnabled ? '적용' : <span className="muted-copy">미적용</span>}</td>
              <td>{x.slaEnabled ? `${x.responseHours}시간` : '—'}</td>
              <td>{x.slaEnabled ? (x.resolutionHours >= 24 ? `${Math.round(x.resolutionHours / 24)}일` : `${x.resolutionHours}시간`) : '—'}</td>
              <td>{x.usedCount}건</td>
              <td><Status value={x.active ? 'ACTIVE' : 'DISABLED'} /></td>
              <td><div className="row-menu"><button onClick={() => setEditCategory({ category: x })}>편집</button><button className="danger" onClick={() => setRemove(x)}>삭제</button></div></td>
            </tr>} /></div>
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
      customerCodeUniqueScope: f.get('customerCodeUniqueScope') || 'GLOBAL',
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
    <label>고객 코드 중복 기준<select name="customerCodeUniqueScope" defaultValue={workspace?.customerCodeUniqueScope || 'GLOBAL'}><option value="GLOBAL">전체 고객 기준</option><option value="WORKSPACE">이 업무 영역 안에서만</option></select><small>고객이 등록된 뒤에는 변경할 수 없습니다.</small></label>
    {editing && <label className="check-label span-2"><input type="checkbox" name="active" defaultChecked={workspace!.active} /> 사용 중</label>}
  </div>
    {editing && isolated !== workspace!.isolated && <div className="alert"><b>격리 설정을 바꿉니다</b><span>저장하면 이 영역의 기존 요청과 등록 고객의 공개 범위도 즉시 함께 바뀝니다.</span></div>}
    <div className="modal-actions"><button type="button" className="btn btn-ghost" onClick={onClose}>취소</button><button className="btn btn-primary" disabled={busy}>{busy ? '저장 중…' : editing ? '변경 저장' : '업무 영역 추가'}</button></div>
  </form></Modal>
}

function PresetModal({ preset, onClose, onApplied, notify }: { preset: Preset; onClose: () => void; onApplied: () => void; notify: Notify }) {
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<{ created: string[]; skipped: string[]; pending: string[] } | null>(null)
  const [users, setUsers] = useState<AdminUser[]>([])
  useEffect(() => { api<{items: AdminUser[]}>('/api/v1/admin/users').then(v => setUsers(v.items.filter(x => x.active))).catch(() => {}) }, [])
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const f = new FormData(e.currentTarget); setBusy(true)
    const coded = (name: string) => String(f.get(name) || '').split('\n').map(x => x.trim()).filter(Boolean).map(x => {
      const [code, ...labelParts] = x.split('|'); return { code: code.trim().toUpperCase(), name: labelParts.join('|').trim() }
    })
    const categories = String(f.get('categories') || '').split('\n').map(x => x.trim()).filter(Boolean).map(x => {
      const [code, name, voiceType] = x.split('|').map(v => v.trim()); return { code: (code || '').toUpperCase(), name: name || '', voiceType: (voiceType || '').toUpperCase() }
    })
    const field = (name: string) => ({ values: coded(`${name}Values`), required: f.get(`${name}Required`) === 'on', defaultValue: f.get(`${name}Default`) || '' })
    const body = { organizationId: f.get('organizationId'), customerCodePattern: f.get('customerCodePattern') || '', customerCodeUniqueScope: f.get('customerCodeUniqueScope'), categories,
      serviceType: field('serviceType'), developmentMethod: field('developmentMethod'), resolver: field('resolver'),
      errorCode: { values: [], required: f.get('errorCodeRequired') === 'on', defaultValue: f.get('errorCodeDefault') || '' },
      agentUserIds: f.getAll('agentUserIds'), reviewerUserIds: f.getAll('reviewerUserIds') }
    try {
      const r = await api<{ created: string[]; skipped: string[]; pending: string[] }>(`/api/v1/admin/voice-presets/${preset.code}/apply`, { method: 'POST', body: JSON.stringify(body) })
      setResult(r); notify(`템플릿을 적용했습니다. 새로 만든 항목 ${r.created.length}개.`)
    } catch (err) { notify(errorMessage(err), true) } finally { setBusy(false) }
  }
  return <Modal title={`템플릿 적용 · ${preset.name}`} onClose={result ? onApplied : onClose} wide>
    {!result ? <form className="form" onSubmit={submit}>
      <p className="muted-copy">{preset.description}</p>
      <label>적용할 부서 *<OrganizationSelect name="organizationId" required /><small>업무 영역의 소유 부서가 되며, 만들어지는 Role은 부서 범위(DEPARTMENT)로 설정됩니다.</small></label>
      <div className="alert"><b>확정된 운영값만 입력하세요</b><span>예시 값은 자동으로 만들지 않습니다. 모든 코드와 명칭은 부서 확정본을 입력해야 하며, 다시 적용할 때 기존 편집값은 덮어쓰지 않습니다.</span></div>
      <label>민원 유형 8종 *<textarea name="categories" rows={8} required placeholder={'TYPE_CODE | 유형 명칭 | COMPLAINT\nTYPE_CODE_2 | 유형 명칭 | DEFECT'} /><small>정확히 8줄 · 코드 | 명칭 | 구분(COMPLAINT, REQUEST, INQUIRY, DEFECT, CHURN_RISK, PRAISE)</small></label>
      <label>서비스 구분 허용값 *<textarea name="serviceTypeValues" rows={4} required placeholder={'SERVICE_CODE | 서비스 명칭'} /></label>
      <div className="form-grid"><label className="check-label"><input type="checkbox" name="serviceTypeRequired" /> 서비스 구분 필수</label><label>서비스 구분 기본 코드<input name="serviceTypeDefault" placeholder="선택" /></label></div>
      <label>개발방식 허용값 *<textarea name="developmentMethodValues" rows={4} required placeholder={'DEV_CODE | 개발방식 명칭'} /></label>
      <div className="form-grid"><label className="check-label"><input type="checkbox" name="developmentMethodRequired" /> 개발방식 필수</label><label>개발방식 기본 코드<input name="developmentMethodDefault" placeholder="선택" /></label></div>
      <div className="form-grid"><label className="check-label"><input type="checkbox" name="errorCodeRequired" /> 오류코드 필수</label><label>오류코드 기본값<input name="errorCodeDefault" placeholder="선택" /></label></div>
      <label>해결 주체 4종 *<textarea name="resolverValues" rows={4} required placeholder={'RESOLVER_CODE | 해결 주체 명칭'} /><small>정확히 4줄 · 허용되지 않은 코드는 해결 처리에서 거부됩니다.</small></label>
      <div className="form-grid"><label className="check-label"><input type="checkbox" name="resolverRequired" /> 해결 주체 필수</label><label>해결 주체 기본 코드<input name="resolverDefault" placeholder="선택" /></label></div>
      <div className="form-grid"><label>회원사코드 형식(정규식)<input name="customerCodePattern" placeholder="선택 · 예: ^[0-9]{12}$" /></label><label>회원사코드 중복 기준<select name="customerCodeUniqueScope" defaultValue="GLOBAL"><option value="GLOBAL">전체 고객 기준</option><option value="WORKSPACE">업무 영역별 기준</option></select></label></div>
      <div className="form-grid"><label>담당자 계정<select name="agentUserIds" multiple size={6}>{users.map(u => <option key={u.id} value={u.id}>{u.displayName} ({u.username}) · {u.organizationName || '조직 없음'}</option>)}</select><small>미확정이면 비워 두고 나중에 사용자 화면에서 Role을 부여할 수 있습니다.</small></label>
        <label>검토자 계정<select name="reviewerUserIds" multiple size={6}>{users.map(u => <option key={u.id} value={u.id}>{u.displayName} ({u.username}) · {u.organizationName || '조직 없음'}</option>)}</select><small>검토자 Role에만 지식 반영 판정 권한이 포함됩니다.</small></label></div>
      <div className="modal-actions"><button type="button" className="btn btn-ghost" onClick={onClose}>취소</button><button className="btn btn-primary" disabled={busy}>{busy ? '적용 중…' : '적용'}</button></div>
    </form> : <div className="form">
      <b>새로 만든 항목</b><ul className="preset-result">{result.created.map(x => <li key={x}>{x}</li>)}{!result.created.length && <li>없음</li>}</ul>
      {result.skipped.length > 0 && <><b>이미 있어 건너뛴 항목</b><ul className="preset-result muted">{result.skipped.map(x => <li key={x}>{x}</li>)}</ul></>}
      {result.pending.length > 0 && <div className="alert"><b>남은 설정</b><span>{result.pending.join(' · ')}</span></div>}
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
