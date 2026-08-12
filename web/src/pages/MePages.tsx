import { FormEvent, useEffect, useState } from 'react'
import { api, date, money, number, relative } from '../api'
import { initials, label } from '../labels'
import { Activity, PersonalKey, User, Version } from '../types'
import Layout, { Empty, Modal, Spinner, Status, navigate } from '../components/Layout'
import { KeyModal, McpGuideModal } from '../components/Keys'
import { errorMessage } from '../App'
import { SavedView } from '../components/SavedViews'
import { activityIcon } from '../labels'

type Props={path:string;user:User;version:Version;approvalEnabled:boolean;onLogout:()=>void;notify:(m:string,e?:boolean)=>void;onPasswordChanged:()=>void}
export default function MePages(props:Props){if(props.path==='/me/password')return <PasswordChange {...props}/>;if(props.path==='/me/keys')return <Keys {...props}/>;if(props.path==='/me/activity')return <MyActivity {...props}/>;if(props.path==='/me/about')return <About {...props}/>;if(props.path==='/me/sessions')return <Sessions {...props}/>;if(props.path==='/me/dashboard')return <MyDashboard {...props}/>;if(props.path==='/me/targets')return <MyTargets {...props}/>;if(props.path==='/me/calendar')return <MyCalendar {...props}/>;if(props.path==='/me/notifications')return <MyNotifications {...props}/>;if(props.path==='/me/saved')return <MySaved {...props}/>;if(props.path==='/me/favorites')return <MyFavorites {...props}/>;return <Profile {...props}/>}
function Frame({children,title,subtitle,actions,...props}:Props&{children:React.ReactNode;title:string;subtitle?:string;actions?:React.ReactNode}){return <Layout area="me" {...props} title={title} subtitle={subtitle} actions={actions}>{children}</Layout>}

function PasswordChange(props:Props){const [busy,setBusy]=useState(false);async function submit(e:FormEvent<HTMLFormElement>){e.preventDefault();const f=new FormData(e.currentTarget);if(f.get('next')!==f.get('confirm')){props.notify('새 비밀번호가 일치하지 않습니다.',true);return}setBusy(true);try{await api('/api/v1/me/password',{method:'POST',body:JSON.stringify({currentPassword:f.get('current'),newPassword:f.get('next')})});props.notify('비밀번호가 변경되었습니다.');props.onPasswordChanged()}catch(e){props.notify(errorMessage(e),true)}finally{setBusy(false)}}return <div className="password-page"><div className="password-card"><span className="brand-mark big">R</span><p className="eyebrow">보안 점검</p><h1>초기 비밀번호를 변경하세요</h1><p>Bootstrap 관리자 또는 새 로컬 계정은 업무를 시작하기 전에 안전한 개인 비밀번호로 변경해야 합니다.</p><div className="alert"><b>안전한 비밀번호 기준</b><span>12자 이상이며 다른 시스템에서 사용하지 않은 비밀번호를 권장합니다.</span></div><form className="form" onSubmit={submit}><label>현재 비밀번호<input type="password" name="current" required autoComplete="current-password"/></label><label>새 비밀번호<input type="password" name="next" required minLength={12} autoComplete="new-password"/></label><label>새 비밀번호 확인<input type="password" name="confirm" required minLength={12} autoComplete="new-password"/></label><button className="btn btn-primary btn-block" disabled={busy}>{busy?'변경 중…':'비밀번호 변경 후 시작'}</button></form><button className="link-button" onClick={props.onLogout}>로그아웃</button></div><footer>Relio v{props.version.version} · Build {initials(props.version.gitCommit, 8)}</footer></div>}

function Profile(props:Props){return <Frame {...props} title="내 프로필" subtitle="업무에 사용하는 나의 정보를 확인합니다." actions={<button className="btn btn-primary">프로필 편집</button>}><div className="profile-layout"><section className="panel profile-card"><span className="avatar profile-avatar">{initials(props.user.displayName, 1)}</span><h2>{props.user.displayName}</h2><p>{props.user.email||props.user.username}</p><Status value={props.user.authMethod}/><div className="profile-stats"><div><b>{props.user.dataScope}</b><small>데이터 범위</small></div><div><b>{props.user.permissions.length}</b><small>권한</small></div></div></section><section className="panel profile-info"><div className="panel-head"><div><h2>기본 정보</h2><p>조직 정보는 관리자에게 문의하세요.</p></div></div><div className="info-grid"><div><small>이름</small><b>{props.user.displayName}</b></div><div><small>사용자 ID</small><b>{props.user.username}</b></div><div><small>이메일</small><b>{props.user.email||'미등록'}</b></div><div><small>인증 방식</small><b>{props.user.authMethod}</b></div><div><small>조직 ID</small><b>{props.user.organizationId||'미지정'}</b></div><div><small>데이터 범위</small><b>{props.user.dataScope}</b></div></div></section></div></Frame>}

function Keys(props: Props) {
  const [items, setItems] = useState<PersonalKey[]>([])
  const [scopes, setScopes] = useState<string[]>([])
  const [modal, setModal] = useState(false)
  const [editing, setEditing] = useState<PersonalKey | null>(null)
  const [guide, setGuide] = useState(false)
  const [secret, setSecret] = useState('')
  const load = () => api<{items: PersonalKey[]; allowedScopes: string[]}>('/api/v1/me/keys')
    .then(v => { setItems(v.items); setScopes(v.allowedScopes) })
    .catch(e => props.notify(errorMessage(e), true))
  useEffect(() => { void load() }, [])

  async function revoke(id: string) {
    if (!confirm('이 키를 즉시 폐기할까요? 이 작업은 되돌릴 수 없습니다.')) return
    try {
      await api(`/api/v1/me/keys/${id}`, {method: 'DELETE'})
      props.notify('키를 폐기했습니다.')
      load()
    } catch (e) { props.notify(errorMessage(e), true) }
  }
  async function rotate(id: string) {
    if (!confirm('새 키를 만들고 기존 키를 Grace Period 후 폐기할까요?')) return
    try {
      const v = await api<{secret: string}>(`/api/v1/me/keys/${id}/rotate`, {method: 'POST'})
      setSecret(v.secret)
      props.notify('새 키가 발급되었습니다.')
      load()
    } catch (e) { props.notify(errorMessage(e), true) }
  }

  return <Frame {...props} title="개인 연동 키" subtitle="접근 키를 발급하고 Scope·채널 권한을 안전하게 변경하거나 회전합니다."
    actions={<><button className="btn btn-secondary" onClick={() => setGuide(true)}>MCP 사용 안내</button><button className="btn btn-primary" onClick={() => setModal(true)}>＋ 새 키 발급</button></>}>
    <div className="key-security"><span>⌁</span><div><b>Secret은 그대로 두고 권한만 변경할 수 있습니다</b><p>원본 Secret은 발급 직후 한 번만 표시되며, 권한 변경은 다음 API·MCP 요청부터 즉시 적용됩니다.</p></div></div>
    <section className="panel table-panel">{items.length ? <table><thead><tr><th>이름 · 키 ID</th><th>채널</th><th>범위</th><th>상태</th><th>만료일</th><th>최근 사용</th><th/></tr></thead><tbody>
      {items.map(k => <tr key={k.id}><td><b>{k.name}</b><code className="key-id">relio_{k.keyId}_••••••</code></td><td>{k.channels.map(c => <Status key={c} value={c}/>)}</td><td title={k.scopes.join(', ')}><span className="scope-count">{k.scopes.length} scopes</span><small className="table-sub scope-preview">{k.scopes.slice(0, 2).join(', ')}</small></td><td><Status value={k.status}/>{k.graceExpiresAt && <small className="table-sub">Grace {date(k.graceExpiresAt)}</small>}</td><td>{date(k.expiresAt)}</td><td>{k.lastUsedAt ? <>{relative(k.lastUsedAt)}<small className="table-sub">{k.lastUsedIp}</small></> : '사용 안 함'}</td><td><div className="row-menu"><button onClick={() => setEditing(k)} disabled={k.status !== 'ACTIVE'}>권한</button><button onClick={() => rotate(k.id)} disabled={k.status !== 'ACTIVE'}>회전</button><button className="danger" onClick={() => revoke(k.id)} disabled={k.status === 'REVOKED'}>폐기</button></div></td></tr>)}
    </tbody></table> : <Empty icon="⌁" title="발급된 개인 키가 없습니다" description="REST API 또는 MCP에서 사용할 첫 키를 발급하세요." action={<button className="btn btn-primary" onClick={() => setModal(true)}>키 발급</button>}/>}</section>
    {modal && <KeyModal scopes={scopes} onClose={() => setModal(false)} onCreated={v => { setModal(false); setSecret(v); load() }} notify={props.notify}/>}
    {editing && <KeyModal scopes={scopes} editing={editing} onClose={() => setEditing(null)} onUpdated={() => { setEditing(null); load() }} notify={props.notify}/>}
    {secret && <SecretModal secret={secret} onClose={() => setSecret('')} notify={props.notify}/>}
    {guide && <McpGuideModal onClose={() => setGuide(false)}/>}
  </Frame>
}
function SecretModal({secret,onClose,notify}:{secret:string;onClose:()=>void;notify:Props['notify']}){const [copied,setCopied]=useState(false);async function copy(){await navigator.clipboard.writeText(secret);setCopied(true);notify('Secret을 클립보드에 복사했습니다.')}return <Modal title="키가 발급되었습니다" onClose={onClose}><div className="one-time-warning"><b>지금 한 번만 확인할 수 있습니다</b><p>창을 닫으면 다시 볼 수 없습니다. 안전한 비밀값 저장소에 보관하세요.</p></div><div className="secret-box"><code>{secret}</code><button onClick={copy}>{copied?'복사됨 ✓':'복사'}</button></div><div className="modal-actions"><button className="btn btn-primary" onClick={onClose}>안전하게 보관했습니다</button></div></Modal>}

function Sessions(props:Props){const [items,setItems]=useState<any[]|null>(null);const load=()=>api<{items:any[]}>('/api/v1/me/sessions').then(v=>setItems(v.items)).catch(e=>props.notify(errorMessage(e),true));useEffect(()=>{void load()},[]);async function revoke(x:any){if(x.current){props.onLogout();return}if(!confirm('선택한 로그인 Session을 종료할까요?'))return;try{await api(`/api/v1/me/sessions/${x.id}`,{method:'DELETE'});props.notify('Session을 종료했습니다.');load()}catch(e){props.notify(errorMessage(e),true)}}return <Frame {...props} title="로그인 세션" subtitle="활성 로그인 위치와 인증 방식을 확인하고 개별 접속을 종료합니다.">{!items?<Spinner/>:<div className="settings-stack">{items.map(x=><section className="panel session-card" key={x.id}><div className="session-icon">◎</div><div><div className="session-title"><b>{x.current?'현재 세션':'다른 세션'}</b><Status value="ACTIVE"/></div><p>{x.authMethod} · {x.ip||'IP 없음'}</p><small>{x.userAgent||'Client 정보 없음'} · 최근 {relative(x.lastSeenAt)}</small></div><button className="btn btn-secondary" onClick={()=>revoke(x)}>{x.current?'로그아웃':'Session 종료'}</button></section>)}</div>}</Frame>}
function MyActivity(props:Props){const [items,setItems]=useState<any[]|null>(null);useEffect(()=>{api<{items:any[]}>('/api/v1/me/activity').then(v=>setItems(v.items)).catch(e=>props.notify(errorMessage(e),true))},[]);return <Frame {...props} title="내 활동 기록" subtitle="화면, API, MCP에서 수행한 나의 활동 이력을 확인합니다.">{!items?<Spinner/>:<section className="panel table-panel">{items.length?<table><thead><tr><th>시각</th><th>채널</th><th>작업</th><th>대상</th><th>IP</th></tr></thead><tbody>{items.map((x,i)=><tr key={i}><td>{date(x.occurredAt)}</td><td><Status value={x.channel}/></td><td><b>{x.action}</b></td><td>{x.resource} <small>{x.resourceId}</small></td><td><code>{x.ip||'—'}</code></td></tr>)}</tbody></table>:<Empty title="활동 이력이 없습니다" description="Relio에서 수행한 작업이 이곳에 기록됩니다."/>}</section>}</Frame>}
function About(props:Props){const v=props.version;return <Frame {...props} title="Relio 정보" subtitle="현재 실행 중인 서비스의 빌드와 에디션 정보입니다."><div className="about-card panel"><div className="about-logo"><span className="brand-mark big">R</span><div><h2>Relio</h2><p>고객 관계 플랫폼</p></div></div><div className="about-version">v{v.version}</div><div className="info-grid"><div><small>버전</small><b>{v.version}</b></div><div><small>에디션</small><b>{v.edition}</b></div><div><small>커밋</small><code>{v.gitCommit}</code></div><div><small>빌드 일자</small><b>{v.buildDate}</b></div></div><p className="about-foot">사람을 위한 CRM · 시스템을 위한 API · AI 에이전트를 위한 MCP</p></div></Frame>}
function PersonalPlaceholder(props:Props){const route=props.path.split('/').pop();const info:Record<string,[string,string,string]>= {dashboard:['내 현황','나에게 중요한 KPI와 위젯을 개인화합니다.','Dashboard 위젯 개인화'],targets:['내 영업목표','목표와 현재 실적을 함께 확인합니다.','등록된 개인 목표가 없습니다'],calendar:['내 일정','영업 일정과 후속 활동을 시간 흐름으로 관리합니다.','예정된 일정이 없습니다'],notifications:['내 알림','알림 수신 방식과 우선 조치를 관리합니다.','새 알림이 없습니다'],saved:['저장된 검색','자주 사용하는 검색 조건을 보관합니다.','저장된 검색이 없습니다'],favorites:['즐겨찾기','중요 고객과 Opportunity를 빠르게 엽니다.','즐겨찾기가 없습니다']};const [title,sub,empty]=info[route||'dashboard']||info.dashboard;return <Frame {...props} title={title} subtitle={sub}><section className="panel"><Empty title={empty} description="업무 화면에서 항목을 추가하면 이곳에 표시됩니다." action={<button className="btn btn-secondary" onClick={()=>navigate('/app/dashboard')}>업무화면으로 이동</button>}/></section></Frame>}

function MyDashboard(props:Props){
  const [metrics,setMetrics]=useState<Record<string,number>|null>(null),[today,setToday]=useState<any>(null),[kpi,setKpi]=useState<any>(null)
  useEffect(()=>{
    api<Record<string,number>>('/api/v1/dashboard').then(setMetrics).catch(e=>props.notify(errorMessage(e),true))
    api('/api/v1/today').then(setToday).catch(()=>{})
    api('/api/v1/sales/kpi').then(setKpi).catch(()=>{})
  },[])
  const urgent=today?(today.counts.CRITICAL||0)+(today.counts.HIGH||0):0
  return <Frame {...props} title="내 현황" subtitle="내 담당 범위의 실적과 오늘 처리할 일을 함께 확인합니다.">
    {!metrics?<Spinner/>:<>
      <div className="my-kpi-grid">
        <div><small>담당 고객</small><strong>{metrics.customerCount}</strong><span>내 데이터 범위</span></div>
        <div><small>진행 영업기회</small><strong>{metrics.openOpportunities}</strong><span>{metrics.staleOpportunities||0}건 점검 필요</span></div>
        <div><small>파이프라인</small><strong>{money(metrics.pipelineAmount)}</strong><span>가중 {money(metrics.weightedAmount)}</span></div>
        <div><small>이번 달 수주</small><strong>{money(metrics.wonThisMonth)}</strong><span>{metrics.dueActions||0}개 후속 작업</span></div>
      </div>
      <div className="me-grid">
        <section className="panel"><div className="panel-head"><div><h2>오늘 처리할 일</h2><p>{urgent?`즉시 대응 ${urgent}건`:'밀린 일이 없습니다'}</p></div>
          <button className="btn btn-sm btn-secondary" onClick={()=>navigate('/app/dashboard')}>영업 현황 →</button></div>
          {today?.items?.length?<div className="me-task-list">{today.items.slice(0,6).map((x:any,i:number)=>
            <button key={i} onClick={()=>navigate(x.route)}><span className={`me-task-mark sev-${x.severity.toLowerCase()}`}/><span><b>{x.title}</b><small>{x.subtitle}</small></span></button>)}</div>
            :<Empty icon="✓" title="처리할 일이 없습니다" description="기한을 넘긴 고객 요청과 정체된 영업기회가 없습니다."/>}
        </section>
        <section className="panel"><div className="panel-head"><div><h2>내 영업 지표</h2><p>담당 범위 기준</p></div></div>
          {kpi?<div className="info-list">
            <div><span>수주율</span><b>{kpi.winRate!=null?`${Math.round(kpi.winRate)}%`:'—'}</b></div>
            <div><span>평균 수주 금액</span><b>{money(kpi.averageWonAmount||0)}</b></div>
            <div><span>평균 영업 기간</span><b>{kpi.averageSalesCycleDays?`${Math.round(kpi.averageSalesCycleDays)}일`:'—'}</b></div>
            <div><span>수주 건수</span><b>{number(kpi.wonCount||0)}건</b></div>
            <div><span>실주 건수</span><b>{number(kpi.lostCount||0)}건</b></div>
          </div>:<Spinner/>}
        </section>
      </div>
    </>}
  </Frame>
}

function MyTargets(props:Props){
  const [items,setItems]=useState<any[]|null>(null)
  useEffect(()=>{api<{items:any[]}>('/api/v1/targets').then(v=>setItems(v.items)).catch(e=>props.notify(errorMessage(e),true))},[])
  const mine=(items||[]).filter(x=>x.ownerId===props.user.id||!x.ownerId)
  return <Frame {...props} title="내 영업목표" subtitle="기간별 목표와 현재 달성률을 함께 확인합니다.">
    {!items?<Spinner/>:mine.length?<section className="panel table-panel"><table>
      <thead><tr><th>기간</th><th>구분</th><th>목표</th><th>실적</th><th>달성률</th></tr></thead>
      <tbody>{mine.map(t=>{const rate=t.targetAmount>0?Math.round((t.achievedAmount/t.targetAmount)*100):0
        return <tr key={t.id}>
          <td><b>{t.periodType==='MONTH'?`${t.periodYear}년 ${t.periodMonth}월`:t.periodType==='QUARTER'?`${t.periodYear}년 ${t.periodQuarter}분기`:`${t.periodYear}년`}</b></td>
          <td><Status value={t.targetType||'REVENUE'}/></td>
          <td>{money(t.targetAmount)}</td>
          <td>{money(t.achievedAmount||0)}</td>
          <td><div className="target-bar"><span style={{width:`${Math.min(100,rate)}%`}} className={rate>=100?'done':rate>=70?'near':''}/></div><small>{rate}%</small></td>
        </tr>})}</tbody></table></section>
      :<section className="panel"><Empty icon="↗" title="등록된 개인 목표가 없습니다" description="관리자 또는 팀장이 기간별 영업목표를 등록하면 이곳에서 달성률을 확인할 수 있습니다."/></section>}
  </Frame>
}

function MyCalendar(props:Props){
  const [due,setDue]=useState<any[]|null>(null),[acts,setActs]=useState<Activity[]>([])
  useEffect(()=>{
    api<{items:any[]}>('/api/v1/tasks/due').then(v=>setDue(v.items)).catch(e=>props.notify(errorMessage(e),true))
    api<{items:Activity[]}>('/api/v1/activities?limit=40').then(v=>setActs(v.items)).catch(()=>{})
  },[])
  // Group by day so the page reads as a schedule rather than a flat list.
  const upcoming=(acts||[]).filter(a=>a.nextAction&&a.nextActionDate)
    .sort((a,b)=>String(a.nextActionDate).localeCompare(String(b.nextActionDate)))
  const byDay=upcoming.reduce<Record<string,Activity[]>>((acc,a)=>{const k=String(a.nextActionDate).slice(0,10);(acc[k]=acc[k]||[]).push(a);return acc},{})
  const todayKey=new Date().toISOString().slice(0,10)
  return <Frame {...props} title="내 일정" subtitle="예정된 다음 행동과 기한이 임박한 후속 작업을 시간 순으로 확인합니다.">
    {due===null?<Spinner/>:<div className="me-grid">
      <section className="panel"><div className="panel-head"><div><h2>다음 행동 일정</h2><p>{upcoming.length}건 예정</p></div></div>
        {Object.keys(byDay).length?<div className="calendar-days">{Object.entries(byDay).map(([day,list])=>
          <div key={day} className={day<todayKey?'past':day===todayKey?'today':''}>
            <header><b>{date(day)}</b>{day===todayKey&&<em>오늘</em>}{day<todayKey&&<em className="overdue">지남</em>}</header>
            {list.map(a=><button key={a.id} onClick={()=>navigate('/app/activities')}><span>{activityIcon(a.activityType)}</span><span><b>{a.nextAction}</b><small>{a.subject}</small></span></button>)}
          </div>)}</div>
          :<Empty icon="◷" title="예정된 일정이 없습니다" description="영업활동을 기록할 때 다음 행동과 날짜를 함께 입력하면 일정으로 표시됩니다."/>}
      </section>
      <section className="panel"><div className="panel-head"><div><h2>기한 임박 후속 작업</h2><p>7일 이내</p></div></div>
        {due.length?<div className="me-task-list">{due.map((t:any)=><button key={t.id} onClick={()=>navigate('/app/activities')}>
          <span className="me-task-mark sev-warning"/><span><b>{t.title||t.subject}</b><small>{t.customerName||''} · {date(t.dueAt||t.nextActionDate)}</small></span></button>)}</div>
          :<Empty icon="✓" title="임박한 작업이 없습니다" description="7일 이내 기한인 후속 작업이 없습니다."/>}
      </section>
    </div>}
  </Frame>
}

function MyNotifications(props:Props){
  const [items,setItems]=useState<any[]|null>(null),[busy,setBusy]=useState(false)
  const load=()=>api<{items:any[]}>('/api/v1/notifications?limit=100').then(v=>setItems(v.items)).catch(e=>props.notify(errorMessage(e),true))
  useEffect(()=>{void load()},[])
  async function readAll(){
    const unread=(items||[]).filter(x=>!x.readAt)
    if(!unread.length)return
    setBusy(true)
    try{await Promise.all(unread.map(x=>api(`/api/v1/notifications/${x.id}/read`,{method:'POST'})));props.notify(`${unread.length}건을 읽음 처리했습니다.`);await load()}
    catch(e){props.notify(errorMessage(e),true)}finally{setBusy(false)}
  }
  async function read(id:string){try{await api(`/api/v1/notifications/${id}/read`,{method:'POST'});await load()}catch(e){props.notify(errorMessage(e),true)}}
  const unread=(items||[]).filter(x=>!x.readAt).length
  return <Frame {...props} title="내 알림" subtitle="업무 알림과 우선 조치를 확인합니다."
    actions={unread>0?<button className="btn btn-secondary" disabled={busy} onClick={readAll}>{busy?'처리 중…':`${unread}건 모두 읽음`}</button>:undefined}>
    {!items?<Spinner/>:items.length?<section className="panel"><div className="notification-page-list">{items.map(x=>
      <div key={x.id} className={x.readAt?'':'unread'}>
        <span className="notification-type">{label(x.type)}</span>
        <span><b>{x.title}</b>{x.body&&<small>{x.body}</small>}<em>{relative(x.createdAt)}</em></span>
        {!x.readAt&&<button className="btn btn-sm btn-ghost" onClick={()=>read(x.id)}>읽음</button>}
      </div>)}</div></section>
      :<section className="panel"><Empty icon="♢" title="새 알림이 없습니다" description="승인 요청, 기한 임박, 담당 변경이 생기면 이곳에 표시됩니다."/></section>}
  </Frame>
}

function MySaved(props:Props){
  const [items,setItems]=useState<SavedView[]|null>(null)
  const load=()=>api<{items:SavedView[]}>('/api/v1/me/views').then(v=>setItems(v.items)).catch(e=>props.notify(errorMessage(e),true))
  useEffect(()=>{void load()},[])
  const routes:Record<string,string>={CUSTOMER:'/app/customers',OPPORTUNITY:'/app/opportunities',VOICE:'/app/voices',ACTIVITY:'/app/activities',CONTRACT:'/app/contracts'}
  const names:Record<string,string>={CUSTOMER:'고객',OPPORTUNITY:'영업기회',VOICE:'고객의 목소리',ACTIVITY:'영업활동',CONTRACT:'계약'}
  async function remove(v:SavedView){try{await api(`/api/v1/me/views/${v.id}`,{method:'DELETE'});props.notify(`'${v.name}'을 삭제했습니다.`);await load()}catch(e){props.notify(errorMessage(e),true)}}
  return <Frame {...props} title="저장된 검색" subtitle="자주 쓰는 검색 조건을 화면별로 보관하고 바로 적용합니다.">
    {!items?<Spinner/>:items.length?<section className="panel table-panel"><table>
      <thead><tr><th>이름</th><th>화면</th><th>조건</th><th>관리</th></tr></thead>
      <tbody>{items.map(v=><tr key={v.id}>
        <td><b>{v.name}</b></td>
        <td>{names[v.resource]||v.resource}</td>
        <td><code className="truncate">{v.query||'조건 없음 (전체)'}</code></td>
        <td><div className="row-menu">
          <button onClick={()=>navigate(`${routes[v.resource]||'/app'}${v.query?'?'+v.query:''}`)}>적용</button>
          <button className="danger" onClick={()=>remove(v)}>삭제</button>
        </div></td>
      </tr>)}</tbody></table></section>
      :<section className="panel"><Empty icon="⌕" title="저장된 검색이 없습니다" description="고객이나 고객의 목소리 화면에서 조건을 지정한 뒤 '현재 조건 저장'을 누르면 이곳에 모입니다."/></section>}
  </Frame>
}

function MyFavorites(props:Props){
  const [items,setItems]=useState<any[]|null>(null)
  const load=()=>api<{items:any[]}>('/api/v1/me/favorites').then(v=>setItems(v.items)).catch(e=>props.notify(errorMessage(e),true))
  useEffect(()=>{void load()},[])
  const names:Record<string,string>={CUSTOMER:'고객',OPPORTUNITY:'영업기회',VOICE:'고객의 목소리',CONTRACT:'계약'}
  async function unstar(x:any){try{await api('/api/v1/me/favorites',{method:'POST',body:JSON.stringify({resource:x.resource,resourceId:x.resourceId})});await load()}catch(e){props.notify(errorMessage(e),true)}}
  return <Frame {...props} title="즐겨찾기" subtitle="자주 보는 고객과 영업기회를 모아 바로 이동합니다.">
    {!items?<Spinner/>:items.length?<div className="favorite-grid">{items.map(x=>
      <article className="panel favorite-card" key={`${x.resource}-${x.resourceId}`}>
        <header><span className="favorite-kind">{names[x.resource]||x.resource}</span>
          <button className="favorite-star on" aria-label="즐겨찾기 해제" onClick={()=>unstar(x)}>★</button></header>
        <b>{x.title}</b><small>{x.subtitle||'추가 정보 없음'}</small>
        <button className="btn btn-sm btn-secondary" onClick={()=>navigate(x.route)}>열기 →</button>
      </article>)}</div>
      :<section className="panel"><Empty icon="☆" title="즐겨찾기가 없습니다" description="고객 목록에서 별표를 누르면 이곳에 모입니다. 담당 범위를 벗어난 항목은 자동으로 사라집니다."/></section>}
  </Frame>
}
