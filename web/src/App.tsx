import { useEffect, useState } from 'react'
import { api, APIError, setCSRF } from './api'
import { AuthStatus, User, Version } from './types'
import Login from './pages/Login'
import AppPages from './pages/AppPages'
import MePages from './pages/MePages'
import AdminPages from './pages/AdminPages'
import VoicePages from './pages/VoicePages'
import IntelligencePages from './pages/IntelligencePages'
import { navigate, Spinner } from './components/Layout'

const emptyVersion: Version = { name: 'Relio', version: '…', gitCommit: 'unknown', buildDate: 'unknown', edition: 'Community' }
const normalizeUser = (user: User): User => ({ ...user, permissions: Array.isArray(user.permissions) ? user.permissions : [] })

// A session that expires while a tab is open sends the next request to /login.
// Remember where the user was so signing in returns them there instead of the
// dashboard, which is what made a refresh feel like it "jumped to the main page".
const RETURN_KEY = 'relio.returnTo'
const isReturnable = (path: string) => /^\/(app|admin|me)\//.test(path) && path !== '/me/password'
function rememberReturn() {
  const target = location.pathname + location.search
  if (isReturnable(location.pathname)) sessionStorage.setItem(RETURN_KEY, target)
}
function takeReturn(): string {
  const target = sessionStorage.getItem(RETURN_KEY)
  sessionStorage.removeItem(RETURN_KEY)
  return target && isReturnable(new URL(target, location.origin).pathname) ? target : '/app/dashboard'
}

export default function App() {
  const [path, setPath] = useState(location.pathname)
  const [user, setUser] = useState<User | null>(null)
  const [status, setStatus] = useState<AuthStatus | null>(null)
  const [version, setVersion] = useState<Version>(emptyVersion)
  const [loading, setLoading] = useState(true)
  const [approvalEnabled, setApprovalEnabled] = useState(false)
  const [toast, setToast] = useState<{message:string; error?:boolean}|null>(null)
  useEffect(() => { const pop = () => setPath(location.pathname); addEventListener('popstate', pop); return () => removeEventListener('popstate', pop) }, [])
  useEffect(() => { bootstrap() }, [])
  useEffect(() => { if (!toast) return; const t = setTimeout(() => setToast(null), 4000); return () => clearTimeout(t) }, [toast])
  async function bootstrap() {
    try {
      const authStatus = await api<AuthStatus>('/api/v1/auth/status')
      setStatus(authStatus); setVersion(authStatus.version)
      try {
        const me = await api<{user:User;version:Version}>('/api/v1/auth/me')
        const currentUser = normalizeUser(me.user)
        setUser(currentUser); setCSRF(currentUser.csrfToken); setVersion(me.version)
        if (currentUser.mustChangePassword && location.pathname !== '/me/password') navigate('/me/password')
        else if (location.pathname === '/' || location.pathname === '/login') navigate('/app/dashboard')
        try { const wf = await api<{enabled:boolean}>('/api/v1/approvals/status'); setApprovalEnabled(wf.enabled) } catch { /* permission-specific */ }
      } catch { if (location.pathname !== '/login') { rememberReturn(); navigate('/login') } }
    } finally { setLoading(false) }
  }
  function loggedIn(next: User) { const currentUser=normalizeUser(next); setUser(currentUser); setCSRF(currentUser.csrfToken); if (currentUser.mustChangePassword) navigate('/me/password'); else navigate(takeReturn()) }
  async function logout() { try { await api('/api/v1/auth/logout', { method:'POST' }) } finally { sessionStorage.removeItem(RETURN_KEY); setUser(null); setCSRF(); navigate('/login') } }
  const notify = (message:string,error=false) => setToast({message,error})
  if (loading) return <div className="boot"><div className="brand-mark big">R</div><Spinner /></div>
  if (!user) return <Login status={status} version={version} onLogin={loggedIn} notify={notify} />
  // A user with no Role authenticates successfully but every API answers 403.
  // Explaining that once beats showing an error toast on each screen.
  if (!user.isBootstrap && user.permissions.length === 0 && !user.mustChangePassword) return <NoAccess user={user} version={version} onLogout={logout} />
  const common = { path, user, version, approvalEnabled, onLogout: logout, notify }
  let page
  if (path.startsWith('/admin')) page = <AdminPages {...common} />
  else if (path.startsWith('/app/voices')) page = <VoicePages {...common} />
  else if (path === '/app/intelligence-center' || path === '/app/recommendations') page = <IntelligencePages {...common} />
  else if (path.startsWith('/me')) page = <MePages {...common} onPasswordChanged={async () => { const me=await api<{user:User}>('/api/v1/auth/me');const currentUser=normalizeUser(me.user);setUser(currentUser);setCSRF(currentUser.csrfToken);navigate(takeReturn()) }} />
  else page = <AppPages {...common} />
  return <>{page}{toast && <div className={`toast ${toast.error?'toast-error':''}`}><span>{toast.error?'!':'✓'}</span>{toast.message}</div>}</>
}

function NoAccess({ user, version, onLogout }: { user: User; version: Version; onLogout: () => void }) {
  return <div className="password-page"><div className="password-card">
    <div className="brand-mark big">R</div>
    <h1>아직 사용 권한이 없습니다</h1>
    <p>{user.displayName}님, 로그인은 정상적으로 완료되었지만 이 계정에 부여된 권한이 없어 업무 화면을 열 수 없습니다.</p>
    <div className="alert"><b>관리자에게 요청할 내용</b><span>Relio 관리자 화면의 <b>사용자 · 조직 → 권한</b>에서 이 계정({user.username})에 Role을 부여해 주세요. SSO 신규 사용자라면 <b>권한 · 데이터 범위</b> 화면에서 기본 권한을 지정하면 이후 가입자에게 자동으로 적용됩니다.</span></div>
    <button className="btn btn-secondary btn-block" onClick={onLogout}>로그아웃</button>
    <footer>Relio v{version.version} · {user.authMethod}</footer>
  </div></div>
}

export function errorMessage(error: unknown) { return error instanceof APIError ? error.message : error instanceof Error ? error.message : '알 수 없는 오류가 발생했습니다.' }
