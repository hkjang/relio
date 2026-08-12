import React, { Fragment, ReactNode, useEffect, useRef, useState } from 'react'
import { api } from '../api'
import { initials, label } from '../labels'
import { User, Version } from '../types'
import { NavItem, adminNav, appNav, meNav, navIcon as icon } from '../nav'
import Palette from './Palette'

export function navigate(path: string) { history.pushState({}, '', path); window.dispatchEvent(new PopStateEvent('popstate')) }
export function Link({ to, children, className = '', onClick }: { to: string; children: ReactNode; className?: string; onClick?: () => void }) { return <a href={to} className={className} onClick={e => { e.preventDefault(); onClick?.(); navigate(to) }}>{children}</a> }

type LayoutProps = { area: 'app'|'me'|'admin'; path: string; user: User; version: Version; approvalEnabled: boolean; onLogout: () => void; children: ReactNode; title: string; subtitle?: string; actions?: ReactNode }

export default function Layout({ area, path, user, version, approvalEnabled, onLogout, children, title, subtitle, actions }: LayoutProps) {
  const [profileOpen, setProfileOpen] = useState(false)
  const [quickOpen, setQuickOpen] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const [adminQuery, setAdminQuery] = useState('')
  const [paletteOpen, setPaletteOpen] = useState(false)
  const profileRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const close = (e: MouseEvent) => { if (!profileRef.current?.contains(e.target as Node)) setProfileOpen(false) }
    const shortcut = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPaletteOpen(true) }
      if (e.key === 'Escape') { setMenuOpen(false); setProfileOpen(false); setQuickOpen(false) }
    }
    document.addEventListener('mousedown', close); document.addEventListener('keydown', shortcut)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', shortcut) }
  }, [])
  useEffect(() => { setMenuOpen(false); setPaletteOpen(false) }, [path])
  const permissions = user.permissions || []
  const canAdmin = user.isBootstrap || permissions.includes('admin:*') || permissions.includes('admin:read')
  const nav = area === 'app' ? [...appNav, ...(approvalEnabled ? [{to:'/app/approvals',key:'approvals',label:'검토 · 승인',group:'고객 관리'}] : [])] : area === 'me' ? meNav : adminNav
  const filteredNav = area !== 'admin' || !adminQuery.trim() ? nav : nav.filter(item => `${item.label} ${item.group || ''} ${item.keywords || ''}`.toLowerCase().includes(adminQuery.trim().toLowerCase()))
  const adminSearch = (value:string) => setAdminQuery(value)
  return <div className={`shell shell-${area}`}>
    {area === 'admin' && menuOpen && <button className="sidebar-scrim" aria-label="관리 메뉴 닫기" onClick={() => setMenuOpen(false)}/>}
    <aside className={`sidebar ${menuOpen ? 'sidebar-open' : ''}`} aria-label={area === 'admin' ? '관리자 메뉴' : '주 메뉴'}>
      <div className="sidebar-mobile-head"><b>관리 메뉴</b><button className="icon-btn" aria-label="관리 메뉴 닫기" onClick={() => setMenuOpen(false)}>×</button></div>
      <Link to={area === 'app' ? '/app/dashboard' : area === 'me' ? '/me/profile' : '/admin/overview'} className="brand"><span className="brand-mark">R</span><span><b>Relio</b><small>{area === 'admin' ? 'Admin Console' : area === 'me' ? 'Personal' : 'Sales CRM'}</small></span></Link>
      {area === 'admin' && <label className="admin-nav-search"><span>⌕</span><input value={adminQuery} onChange={e=>adminSearch(e.target.value)} placeholder="설정 메뉴 찾기" aria-label="관리자 메뉴 검색"/></label>}
      <nav className="side-nav" onClick={() => setMenuOpen(false)}>
        {filteredNav.map((item,index) => <Fragment key={item.to}>{item.group && item.group !== filteredNav[index-1]?.group && <p className="nav-group">{item.group}</p>}<Link to={item.to} className={path === item.to || path.startsWith(item.to + '/') ? 'active' : ''}><span className="nav-icon">{icon[item.key]}</span><span>{item.label}</span></Link></Fragment>)}
        {area === 'admin' && filteredNav.length === 0 && <p className="nav-empty">일치하는 설정 메뉴가 없습니다.</p>}
      </nav>
      <div className="side-footer">
        {area !== 'app' && <Link to="/app/dashboard" className="back-link">← 영업 업무화면</Link>}
        <span>고객 관계 플랫폼</span><small>오프라인 기업용 CRM</small>
      </div>
    </aside>
    <main className="main">
      <header className="topbar">
        {area === 'admin' && <button className="mobile-menu-btn" onClick={() => setMenuOpen(true)} aria-expanded={menuOpen} aria-label="관리 메뉴 열기"><span>☰</span> 관리 메뉴</button>}
        <button type="button" className="global-search" onClick={() => setPaletteOpen(true)} aria-haspopup="dialog" aria-label="빠른 이동 열기">
          <span aria-hidden="true">⌕</span>
          <span className="global-search-copy">메뉴, 고객, 담당자, 영업기회로 바로 이동</span>
          <kbd>{navigator.platform.includes('Mac') ? '⌘ K' : 'Ctrl K'}</kbd>
        </button>
        <div className="top-actions">
          {area === 'app' && <div className="quick-wrap"><button className="btn btn-primary btn-sm" onClick={() => setQuickOpen(v => !v)} aria-expanded={quickOpen}>＋ 빠른 등록</button>{quickOpen && <div className="popover quick-menu"><button onClick={() => {setQuickOpen(false); navigate('/app/customers?new=1')}}>고객 등록</button><button onClick={() => {setQuickOpen(false); navigate('/app/opportunities?new=1')}}>영업기회 생성</button><button onClick={() => {setQuickOpen(false); navigate('/app/activities?new=meeting')}}>미팅 기록</button><button onClick={() => {setQuickOpen(false); navigate('/app/activities?new=call')}}>전화 기록</button><button onClick={() => {setQuickOpen(false); navigate('/app/activities?new=task')}}>할 일 생성</button></div>}</div>}
          <NotificationBell canRead={user.isBootstrap || (user.permissions||[]).some(p => p === 'admin:*' || p === 'notification:read')} />
          <div className="profile-wrap" ref={profileRef}><button className="profile-button" onClick={() => setProfileOpen(v => !v)} aria-expanded={profileOpen}><span className="avatar">{initials(user.displayName, 1)}</span><span className="profile-copy"><b>{user.displayName}</b><small>{user.dataScope} · {user.authMethod}</small></span><span>⌄</span></button>{profileOpen && <div className="popover profile-menu"><div className="profile-head"><span className="avatar large">{user.displayName.slice(0,1)}</span><div><b>{user.displayName}</b><small>{user.email || user.username}</small></div></div><Link to="/me/profile">내 프로필</Link><Link to="/me/dashboard">개인 설정</Link><Link to="/me/keys">개인 연동 키</Link>{canAdmin && <><hr/><Link to="/admin/overview">관리자 콘솔 <span>→</span></Link></>}<hr/><div className="version-menu"><b>Relio v{version.version}</b><span>빌드 {version.gitCommit.slice(0,8)}</span></div><button className="logout" onClick={onLogout}>로그아웃</button></div>}</div>
        </div>
      </header>
      {paletteOpen && <Palette user={user} approvalEnabled={approvalEnabled} onClose={() => setPaletteOpen(false)} />}
      <section className="page">
        <div className="page-heading"><div><p className="eyebrow">{area === 'admin' ? '관리자 콘솔' : area === 'me' ? '개인 업무' : '영업 업무'}</p><h1>{title}</h1>{subtitle && <p className="subtitle">{subtitle}</p>}</div>{actions && <div className="heading-actions">{actions}</div>}</div>
        {children}
      </section>
    </main>
  </div>
}

// NotificationBell surfaces the notifications the API already produces. Before
// this the bell was decorative and the only way to see a notification was the
// REST endpoint.
type Notification = { id: string; title: string; body?: string; type: string; readAt?: string; resourceType?: string; resourceId?: string; createdAt: string }
function NotificationBell({ canRead }: { canRead: boolean }) {
  const [open, setOpen] = useState(false)
  const [items, setItems] = useState<Notification[] | null>(null)
  const [busy, setBusy] = useState(false)
  const wrap = useRef<HTMLDivElement>(null)
  const unread = (items || []).filter(x => !x.readAt).length
  const load = () => api<{ items?: Notification[] }>('/api/v1/notifications?limit=20').then(v => setItems(v.items || [])).catch(() => setItems([]))
  useEffect(() => { if (!canRead) { setItems([]); return } void load() }, [canRead])
  useEffect(() => {
    const close = (e: MouseEvent) => { if (!wrap.current?.contains(e.target as Node)) setOpen(false) }
    addEventListener('mousedown', close); return () => removeEventListener('mousedown', close)
  }, [])
  async function markRead(id: string) {
    setBusy(true)
    try {
      await api(`/api/v1/notifications/${id}/read`, { method: 'POST' })
      setItems(v => (v || []).map(x => x.id === id ? { ...x, readAt: new Date().toISOString() } : x))
    } catch { /* a stale notification must not break navigation */ } finally { setBusy(false) }
  }
  if (!canRead) return null
  return <div className="notification-wrap" ref={wrap}>
    <button className="icon-btn" aria-label={unread ? `읽지 않은 알림 ${unread}건` : '알림'} aria-expanded={open} onClick={() => { setOpen(v => !v); if (!open) void load() }}>♢{unread > 0 && <span className="notification-dot" />}</button>
    {open && <div className="popover notification-menu">
      <header><b>알림</b>{unread > 0 && <span>{unread}건 미확인</span>}</header>
      {!items ? <p className="notification-empty">불러오는 중입니다…</p>
        : items.length ? <div className="notification-list">{items.map(x => <button key={x.id} className={x.readAt ? '' : 'unread'} disabled={busy} onClick={() => { void markRead(x.id); if (x.resourceType === 'OPPORTUNITY') navigate('/app/opportunities'); else if (x.resourceType === 'CUSTOMER' && x.resourceId) navigate('/app/customers/' + x.resourceId) }}>
          <span className="notification-type">{x.type}</span>
          <span><b>{x.title}</b>{x.body && <small>{x.body}</small>}</span>
        </button>)}</div>
        : <p className="notification-empty">새로운 알림이 없습니다.</p>}
    </div>}
  </div>
}

export function Empty({ icon = '◇', title, description, action }: { icon?: string; title: string; description: string; action?: ReactNode }) { return <div className="empty"><span className="empty-icon">{icon}</span><h3>{title}</h3><p>{description}</p>{action}</div> }
export function Spinner() { return <div className="spinner-wrap" role="status"><span className="spinner"/><p>데이터를 불러오는 중입니다</p></div> }
export function Status({ value, raw = false, text }: { value: string; raw?: boolean; text?: string }) {
  // Keep the code in the class name so existing colour rules still match,
  // but show the Korean label to the user. `text` overrides the dictionary for
  // codes that mean different things in different domains — USER is a Data
  // Scope ("본인") and also a contact role ("실사용자").
  const c = value.toLowerCase().replaceAll('_', '-')
  return <span className={`status status-${c}`} title={value}>{raw ? value : text || label(value)}</span>
}
export function Modal({ title, onClose, children, wide = false }: { title: string; onClose: () => void; children: ReactNode; wide?: boolean }) {
  const surface = useDialog(onClose)
  return <div className="modal-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
    <div ref={surface} className={`modal ${wide ? 'modal-wide' : ''}`} role="dialog" aria-modal="true" aria-label={title}>
      <div className="modal-head"><h2>{title}</h2><button className="icon-btn" aria-label="닫기" onClick={onClose}>×</button></div>
      {children}
    </div>
  </div>
}

// Dialogs nest — a contact form opens on top of a request form — so the page
// behind them can only be unlocked once the last one closes.
let openDialogs = 0
function lockPageScroll() {
  if (openDialogs++ === 0) document.body.style.overflow = 'hidden'
  return () => { if (--openDialogs === 0) document.body.style.overflow = '' }
}

/** useDialog makes an overlay usable without a mouse: Escape closes it, Tab stays
 *  inside it, and focus returns to whatever opened it. Without this a keyboard
 *  user tabs straight out of the dialog into the page behind it. It also freezes
 *  the page underneath, so a scroll gesture moves the dialog and not the list
 *  the user is about to come back to. */
export function useDialog(onClose: () => void) {
  const surface = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const unlock = lockPageScroll()
    const opener = document.activeElement as HTMLElement | null
    const focusable = () => Array.from(
      surface.current?.querySelectorAll<HTMLElement>('a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])') ?? []
    ).filter(el => el.offsetParent !== null)
    focusable()[0]?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') { e.stopPropagation(); onClose(); return }
      if (e.key !== 'Tab') return
      const items = focusable()
      if (!items.length) return
      const first = items[0], last = items[items.length - 1]
      if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
      else if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
    }
    document.addEventListener('keydown', onKey, true)
    return () => { document.removeEventListener('keydown', onKey, true); unlock(); opener?.focus?.() }
  }, [onClose])
  return surface
}

/** rowProps turns a clickable table row into a real control: reachable by Tab,
 *  activated by Enter or Space, and announced as a button to screen readers. */
export function rowProps(activate: () => void) {
  return {
    tabIndex: 0,
    role: 'button' as const,
    onClick: activate,
    onKeyDown: (e: React.KeyboardEvent) => {
      if (e.key === 'Enter' || e.key === ' ') {
        // Space scrolls the page by default, which loses the user's place.
        e.preventDefault()
        activate()
      }
    },
  }
}

// Confirm gates every destructive administrator action. Typing the record name is
// required only when the action cannot be undone from the console.
export function Confirm({ title, description, confirmLabel = '삭제', requireText, busy, onCancel, onConfirm }: { title: string; description: ReactNode; confirmLabel?: string; requireText?: string; busy?: boolean; onCancel: () => void; onConfirm: () => void }) {
  const [typed, setTyped] = useState('')
  const ready = !requireText || typed.trim() === requireText
  return <Modal title={title} onClose={onCancel}>
    <div className="form confirm-form">
      <div className="alert alert-error"><b>이 작업은 되돌릴 수 없습니다</b><span>{description}</span></div>
      {requireText && <label>확인을 위해 <code>{requireText}</code>를 입력하세요<input autoFocus value={typed} onChange={e => setTyped(e.target.value)} placeholder={requireText}/></label>}
      <div className="modal-actions"><button type="button" className="btn btn-ghost" onClick={onCancel}>취소</button><button type="button" className="btn btn-danger" disabled={!ready || busy} onClick={onConfirm}>{busy ? '처리 중…' : confirmLabel}</button></div>
    </div>
  </Modal>
}
