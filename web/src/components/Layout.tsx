import { Fragment, ReactNode, useEffect, useRef, useState } from 'react'
import { User, Version } from '../types'

export function navigate(path: string) { history.pushState({}, '', path); window.dispatchEvent(new PopStateEvent('popstate')) }
export function Link({ to, children, className = '', onClick }: { to: string; children: ReactNode; className?: string; onClick?: () => void }) { return <a href={to} className={className} onClick={e => { e.preventDefault(); onClick?.(); navigate(to) }}>{children}</a> }

const icon: Record<string,string> = { dashboard:'⌂', operations:'◉', customers:'◫', opportunities:'◆', pipeline:'▦', intelligence:'◇', activities:'◷', forecast:'↗', contracts:'▤', approvals:'✓', admin:'⚙', me:'◎', search:'⌕', security:'◈', api:'⌁', data:'⇄' }
type NavItem = { to:string; key:string; label:string; group?:string; keywords?:string }
type LayoutProps = { area: 'app'|'me'|'admin'; path: string; user: User; version: Version; approvalEnabled: boolean; onLogout: () => void; children: ReactNode; title: string; subtitle?: string; actions?: ReactNode }

const appNav: NavItem[] = [
  {to:'/app/dashboard',key:'dashboard',label:'Dashboard'}, {to:'/app/customers',key:'customers',label:'고객'}, {to:'/app/opportunities',key:'opportunities',label:'Opportunity'}, {to:'/app/pipeline',key:'pipeline',label:'Pipeline'}, {to:'/app/intelligence',key:'intelligence',label:'Deal Intelligence'}, {to:'/app/activities',key:'activities',label:'영업활동'}, {to:'/app/forecast',key:'forecast',label:'Forecast'}, {to:'/app/contracts',key:'contracts',label:'계약'},
]
const meNav: NavItem[] = [
  {to:'/me/profile',key:'me',label:'내 프로필'}, {to:'/me/dashboard',key:'dashboard',label:'내 Dashboard'}, {to:'/me/targets',key:'forecast',label:'내 영업목표'}, {to:'/me/calendar',key:'activities',label:'내 일정'}, {to:'/me/notifications',key:'approvals',label:'내 알림'}, {to:'/me/saved',key:'search',label:'저장된 검색'}, {to:'/me/favorites',key:'customers',label:'즐겨찾기'}, {to:'/me/keys',key:'pipeline',label:'API / MCP Key'}, {to:'/me/sessions',key:'me',label:'로그인 세션'}, {to:'/me/activity',key:'activities',label:'활동 기록'}, {to:'/me/about',key:'admin',label:'Relio 정보'},
]
const adminNav: NavItem[] = [
  {to:'/admin/overview',key:'dashboard',label:'운영 Command Center',group:'운영',keywords:'현황 준비도 action'},
  {to:'/admin/operations',key:'operations',label:'Diagnostics · Job',group:'운영',keywords:'진단 support bundle migration database'},
  {to:'/admin/audit',key:'activities',label:'Audit Log',group:'운영',keywords:'감사 이력 추적'},
  {to:'/admin/system',key:'admin',label:'시스템 기본정보',group:'기본 설정',keywords:'url locale timezone'},
  {to:'/admin/oidc',key:'me',label:'Keycloak OIDC',group:'기본 설정',keywords:'sso issuer client claim'},
  {to:'/admin/security',key:'security',label:'보안 · 파일 · Session',group:'기본 설정',keywords:'local login rate export'},
  {to:'/admin/users',key:'customers',label:'사용자 · 조직',group:'조직 · 권한',keywords:'user organization team'},
  {to:'/admin/roles',key:'approvals',label:'Role · Data Scope',group:'조직 · 권한',keywords:'permission rbac'},
  {to:'/admin/pipeline',key:'pipeline',label:'CRM · Pipeline',group:'영업 정책',keywords:'stage probability forecast'},
  {to:'/admin/sales-execution',key:'intelligence',label:'Sales Execution',group:'영업 정책',keywords:'playbook exit criteria health'},
  {to:'/admin/relationships',key:'customers',label:'Relationship Intelligence',group:'영업 정책',keywords:'graph account plan team'},
  {to:'/admin/approval',key:'approvals',label:'승인 Workflow',group:'영업 정책',keywords:'review approve reject'},
  {to:'/admin/custom-fields',key:'opportunities',label:'Custom Field',group:'영업 정책',keywords:'metadata jsonb field'},
  {to:'/admin/keys',key:'api',label:'Personal Key · API · MCP',group:'개발자',keywords:'rotation scope origin tool'},
  {to:'/admin/data',key:'data',label:'Data Quality · Config',group:'데이터',keywords:'quality data configuration bundle export import diff'},
]

export default function Layout({ area, path, user, version, approvalEnabled, onLogout, children, title, subtitle, actions }: LayoutProps) {
  const [profileOpen, setProfileOpen] = useState(false)
  const [quickOpen, setQuickOpen] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const [adminQuery, setAdminQuery] = useState('')
  const profileRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    const close = (e: MouseEvent) => { if (!profileRef.current?.contains(e.target as Node)) setProfileOpen(false) }
    const shortcut = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); searchRef.current?.focus() }
      if (e.key === 'Escape') { setMenuOpen(false); setProfileOpen(false); setQuickOpen(false) }
    }
    document.addEventListener('mousedown', close); document.addEventListener('keydown', shortcut)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', shortcut) }
  }, [])
  useEffect(() => setMenuOpen(false), [path])
  const canAdmin = user.isBootstrap || user.permissions.includes('admin:*') || user.permissions.includes('admin:read')
  const nav = area === 'app' ? [...appNav, ...(approvalEnabled ? [{to:'/app/approvals',key:'approvals',label:'검토 · 승인'}] : [])] : area === 'me' ? meNav : adminNav
  const filteredNav = area !== 'admin' || !adminQuery.trim() ? nav : nav.filter(item => `${item.label} ${item.group || ''} ${item.keywords || ''}`.toLowerCase().includes(adminQuery.trim().toLowerCase()))
  const adminSearch = (value:string) => setAdminQuery(value)
  return <div className={`shell shell-${area}`}>
    {area === 'admin' && menuOpen && <button className="sidebar-scrim" aria-label="관리 메뉴 닫기" onClick={() => setMenuOpen(false)}/>}
    <aside className={`sidebar ${menuOpen ? 'sidebar-open' : ''}`} aria-label={area === 'admin' ? '관리자 메뉴' : '주 메뉴'}>
      <div className="sidebar-mobile-head"><b>관리 메뉴</b><button className="icon-btn" aria-label="관리 메뉴 닫기" onClick={() => setMenuOpen(false)}>×</button></div>
      <Link to={area === 'app' ? '/app/dashboard' : area === 'me' ? '/me/profile' : '/admin/overview'} className="brand"><span className="brand-mark">R</span><span><b>Relio</b><small>{area === 'admin' ? 'Admin Console' : area === 'me' ? 'Personal' : 'Sales CRM'}</small></span></Link>
      {area === 'admin' && <label className="admin-nav-search"><span>⌕</span><input value={adminQuery} onChange={e=>adminSearch(e.target.value)} placeholder="설정 메뉴 찾기" aria-label="관리자 메뉴 검색"/></label>}
      <nav className="side-nav" onClick={() => setMenuOpen(false)}>
        {filteredNav.map((item,index) => <Fragment key={item.to}>{area === 'admin' && item.group !== filteredNav[index-1]?.group && <p className="nav-group">{item.group}</p>}<Link to={item.to} className={path === item.to || path.startsWith(item.to + '/') ? 'active' : ''}><span className="nav-icon">{icon[item.key]}</span><span>{item.label}</span></Link></Fragment>)}
        {area === 'admin' && filteredNav.length === 0 && <p className="nav-empty">일치하는 설정 메뉴가 없습니다.</p>}
      </nav>
      <div className="side-footer">
        {area !== 'app' && <Link to="/app/dashboard" className="back-link">← CRM 업무화면</Link>}
        <span>Relationship + IO</span><small>Offline Enterprise CRM</small>
      </div>
    </aside>
    <main className="main">
      <header className="topbar">
        {area === 'admin' && <button className="mobile-menu-btn" onClick={() => setMenuOpen(true)} aria-expanded={menuOpen} aria-label="관리 메뉴 열기"><span>☰</span> 관리 메뉴</button>}
        <div className="global-search"><span>⌕</span><input ref={searchRef} aria-label={area === 'admin' ? '관리자 메뉴 검색' : '통합 검색'} value={area === 'admin' ? adminQuery : undefined} placeholder={area === 'admin' ? '설정, 정책, 운영 기능 검색' : '고객, 담당자, Opportunity 통합 검색'} onChange={area === 'admin' ? e=>adminSearch(e.target.value) : undefined} onKeyDown={e => { if (e.key === 'Enter') { if (area === 'admin' && filteredNav[0]) navigate(filteredNav[0].to); else if (e.currentTarget.value) navigate('/app/customers?q=' + encodeURIComponent(e.currentTarget.value)) } }}/><kbd>{area === 'admin' ? '⌘ K' : 'Enter'}</kbd></div>
        <div className="top-actions">
          {area === 'app' && <div className="quick-wrap"><button className="btn btn-primary btn-sm" onClick={() => setQuickOpen(v => !v)} aria-expanded={quickOpen}>＋ Quick Action</button>{quickOpen && <div className="popover quick-menu"><button onClick={() => {setQuickOpen(false); navigate('/app/customers?new=1')}}>고객 등록</button><button onClick={() => {setQuickOpen(false); navigate('/app/opportunities?new=1')}}>Opportunity 생성</button><button onClick={() => {setQuickOpen(false); navigate('/app/activities?new=meeting')}}>미팅 기록</button><button onClick={() => {setQuickOpen(false); navigate('/app/activities?new=call')}}>전화 기록</button><button onClick={() => {setQuickOpen(false); navigate('/app/activities?new=task')}}>Task 생성</button></div>}</div>}
          <button className="icon-btn" aria-label="알림">♢<span className="notification-dot" /></button>
          <div className="profile-wrap" ref={profileRef}><button className="profile-button" onClick={() => setProfileOpen(v => !v)} aria-expanded={profileOpen}><span className="avatar">{user.displayName.slice(0,1).toUpperCase()}</span><span className="profile-copy"><b>{user.displayName}</b><small>{user.dataScope} · {user.authMethod}</small></span><span>⌄</span></button>{profileOpen && <div className="popover profile-menu"><div className="profile-head"><span className="avatar large">{user.displayName.slice(0,1)}</span><div><b>{user.displayName}</b><small>{user.email || user.username}</small></div></div><Link to="/me/profile">내 프로필</Link><Link to="/me/dashboard">개인 설정</Link><Link to="/me/keys">API / MCP Key</Link>{canAdmin && <><hr/><Link to="/admin/overview">관리자 콘솔 <span>→</span></Link></>}<hr/><div className="version-menu"><b>Relio v{version.version}</b><span>Build {version.gitCommit.slice(0,8)}</span></div><button className="logout" onClick={onLogout}>로그아웃</button></div>}</div>
        </div>
      </header>
      <section className="page">
        <div className="page-heading"><div><p className="eyebrow">{area === 'admin' ? 'ADMINISTRATION' : area === 'me' ? 'PERSONAL WORKSPACE' : 'SALES WORKSPACE'}</p><h1>{title}</h1>{subtitle && <p className="subtitle">{subtitle}</p>}</div>{actions && <div className="heading-actions">{actions}</div>}</div>
        {children}
      </section>
    </main>
  </div>
}

export function Empty({ icon = '◇', title, description, action }: { icon?: string; title: string; description: string; action?: ReactNode }) { return <div className="empty"><span className="empty-icon">{icon}</span><h3>{title}</h3><p>{description}</p>{action}</div> }
export function Spinner() { return <div className="spinner-wrap" role="status"><span className="spinner"/><p>데이터를 불러오는 중입니다</p></div> }
export function Status({ value }: { value: string }) { const c = value.toLowerCase().replaceAll('_','-'); return <span className={`status status-${c}`}>{value}</span> }
export function Modal({ title, onClose, children, wide = false }: { title: string; onClose: () => void; children: ReactNode; wide?: boolean }) { return <div className="modal-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}><div className={`modal ${wide ? 'modal-wide' : ''}`} role="dialog" aria-modal="true" aria-label={title}><div className="modal-head"><h2>{title}</h2><button className="icon-btn" aria-label="닫기" onClick={onClose}>×</button></div>{children}</div></div> }
