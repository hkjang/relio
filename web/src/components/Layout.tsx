import { ReactNode, useEffect, useRef, useState } from 'react'
import { User, Version } from '../types'

export function navigate(path: string) { history.pushState({}, '', path); window.dispatchEvent(new PopStateEvent('popstate')) }
export function Link({ to, children, className = '' }: { to: string; children: ReactNode; className?: string }) { return <a href={to} className={className} onClick={e => { e.preventDefault(); navigate(to) }}>{children}</a> }

const icon: Record<string,string> = { dashboard:'⌂', customers:'◫', opportunities:'◆', pipeline:'▦', activities:'◷', forecast:'↗', contracts:'▤', approvals:'✓', admin:'⚙', me:'◎', search:'⌕' }

type LayoutProps = { area: 'app'|'me'|'admin'; path: string; user: User; version: Version; approvalEnabled: boolean; onLogout: () => void; children: ReactNode; title: string; subtitle?: string; actions?: ReactNode }

export default function Layout({ area, path, user, version, approvalEnabled, onLogout, children, title, subtitle, actions }: LayoutProps) {
  const [profileOpen, setProfileOpen] = useState(false)
  const [quickOpen, setQuickOpen] = useState(false)
  const profileRef = useRef<HTMLDivElement>(null)
  useEffect(() => { const close = (e: MouseEvent) => { if (!profileRef.current?.contains(e.target as Node)) setProfileOpen(false) }; document.addEventListener('mousedown', close); return () => document.removeEventListener('mousedown', close) }, [])
  const canAdmin = user.isBootstrap || user.permissions.includes('admin:*') || user.permissions.includes('admin:read')
  const appNav = [
    ['/app/dashboard','dashboard','Dashboard'], ['/app/customers','customers','고객'], ['/app/opportunities','opportunities','Opportunity'], ['/app/pipeline','pipeline','Pipeline'], ['/app/activities','activities','영업활동'], ['/app/forecast','forecast','Forecast'], ['/app/contracts','contracts','계약'],
    ...(approvalEnabled ? [['/app/approvals','approvals','검토 · 승인']] : []),
  ]
  const meNav = [['/me/profile','me','내 프로필'],['/me/dashboard','dashboard','내 Dashboard'],['/me/targets','forecast','내 영업목표'],['/me/calendar','activities','내 일정'],['/me/notifications','approvals','내 알림'],['/me/saved','search','저장된 검색'],['/me/favorites','customers','즐겨찾기'],['/me/keys','pipeline','API / MCP Key'],['/me/sessions','me','로그인 세션'],['/me/activity','activities','활동 기록'],['/me/about','admin','Relio 정보']]
  const adminNav = [['/admin/overview','dashboard','운영 현황'],['/admin/system','admin','시스템 설정'],['/admin/oidc','me','Keycloak OIDC'],['/admin/users','customers','사용자 · 조직'],['/admin/roles','approvals','권한 · 데이터 범위'],['/admin/pipeline','pipeline','CRM · Pipeline'],['/admin/approval','approvals','승인 Workflow'],['/admin/keys','me','개인키 · API · MCP'],['/admin/custom-fields','opportunities','Custom Field'],['/admin/security','admin','보안 · 파일 · 알림'],['/admin/audit','activities','Audit'],['/admin/data','contracts','Import · Export']]
  const nav = area === 'app' ? appNav : area === 'me' ? meNav : adminNav
  return <div className={`shell shell-${area}`}>
    <aside className="sidebar">
      <Link to={area === 'app' ? '/app/dashboard' : area === 'me' ? '/me/profile' : '/admin/overview'} className="brand"><span className="brand-mark">R</span><span><b>Relio</b><small>{area === 'admin' ? 'Admin Console' : area === 'me' ? 'Personal' : 'Sales CRM'}</small></span></Link>
      <nav className="side-nav">{nav.map(([to,key,label]) => <Link key={to} to={to} className={path === to || path.startsWith(to + '/') ? 'active' : ''}><span className="nav-icon">{icon[key]}</span><span>{label}</span></Link>)}</nav>
      <div className="side-footer">
        {area !== 'app' && <Link to="/app/dashboard" className="back-link">← CRM 업무화면</Link>}
        <span>Relationship + IO</span><small>Offline Enterprise CRM</small>
      </div>
    </aside>
    <main className="main">
      <header className="topbar">
        <div className="global-search"><span>⌕</span><input aria-label="통합 검색" placeholder="고객, 담당자, Opportunity 통합 검색" onKeyDown={e => { if (e.key === 'Enter' && e.currentTarget.value) navigate('/app/customers?q=' + encodeURIComponent(e.currentTarget.value)) }}/><kbd>Enter</kbd></div>
        <div className="top-actions">
          {area === 'app' && <div className="quick-wrap"><button className="btn btn-primary btn-sm" onClick={() => setQuickOpen(v => !v)}>＋ Quick Action</button>{quickOpen && <div className="popover quick-menu"><button onClick={() => {setQuickOpen(false); navigate('/app/customers?new=1')}}>고객 등록</button><button onClick={() => {setQuickOpen(false); navigate('/app/opportunities?new=1')}}>Opportunity 생성</button><button onClick={() => {setQuickOpen(false); navigate('/app/activities?new=meeting')}}>미팅 기록</button><button onClick={() => {setQuickOpen(false); navigate('/app/activities?new=call')}}>전화 기록</button><button onClick={() => {setQuickOpen(false); navigate('/app/activities?new=task')}}>Task 생성</button></div>}</div>}
          <button className="icon-btn" aria-label="알림">♢<span className="notification-dot" /></button>
          <div className="profile-wrap" ref={profileRef}><button className="profile-button" onClick={() => setProfileOpen(v => !v)}><span className="avatar">{user.displayName.slice(0,1).toUpperCase()}</span><span className="profile-copy"><b>{user.displayName}</b><small>{user.dataScope} · {user.authMethod}</small></span><span>⌄</span></button>{profileOpen && <div className="popover profile-menu"><div className="profile-head"><span className="avatar large">{user.displayName.slice(0,1)}</span><div><b>{user.displayName}</b><small>{user.email || user.username}</small></div></div><Link to="/me/profile">내 프로필</Link><Link to="/me/dashboard">개인 설정</Link><Link to="/me/keys">API / MCP Key</Link>{canAdmin && <><hr/><Link to="/admin/overview">관리자 콘솔 <span>→</span></Link></>}<hr/><div className="version-menu"><b>Relio v{version.version}</b><span>Build {version.gitCommit.slice(0,8)}</span></div><button className="logout" onClick={onLogout}>로그아웃</button></div>}</div>
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
export function Spinner() { return <div className="spinner-wrap"><span className="spinner"/><p>데이터를 불러오는 중입니다</p></div> }
export function Status({ value }: { value: string }) { const c = value.toLowerCase().replaceAll('_','-'); return <span className={`status status-${c}`}>{value}</span> }
export function Modal({ title, onClose, children, wide = false }: { title: string; onClose: () => void; children: ReactNode; wide?: boolean }) { return <div className="modal-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}><div className={`modal ${wide ? 'modal-wide' : ''}`} role="dialog" aria-modal="true"><div className="modal-head"><h2>{title}</h2><button className="icon-btn" onClick={onClose}>×</button></div>{children}</div></div> }
