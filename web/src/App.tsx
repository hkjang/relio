import { useEffect, useState } from 'react'
import { api, APIError, setCSRF } from './api'
import { AuthStatus, User, Version } from './types'
import Login from './pages/Login'
import AppPages from './pages/AppPages'
import MePages from './pages/MePages'
import AdminPages from './pages/AdminPages'
import { navigate, Spinner } from './components/Layout'

const emptyVersion: Version = { name: 'Relio', version: '…', gitCommit: 'unknown', buildDate: 'unknown', edition: 'Community' }

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
        setUser(me.user); setCSRF(me.user.csrfToken); setVersion(me.version)
        if (me.user.mustChangePassword && location.pathname !== '/me/password') navigate('/me/password')
        else if (location.pathname === '/' || location.pathname === '/login') navigate('/app/dashboard')
        try { const wf = await api<{enabled:boolean}>('/api/v1/approvals/status'); setApprovalEnabled(wf.enabled) } catch { /* permission-specific */ }
      } catch { if (location.pathname !== '/login') navigate('/login') }
    } finally { setLoading(false) }
  }
  function loggedIn(next: User) { setUser(next); setCSRF(next.csrfToken); if (next.mustChangePassword) navigate('/me/password'); else navigate('/app/dashboard') }
  async function logout() { try { await api('/api/v1/auth/logout', { method:'POST' }) } finally { setUser(null); setCSRF(); navigate('/login') } }
  const notify = (message:string,error=false) => setToast({message,error})
  if (loading) return <div className="boot"><div className="brand-mark big">R</div><Spinner /></div>
  if (!user) return <Login status={status} version={version} onLogin={loggedIn} notify={notify} />
  const common = { path, user, version, approvalEnabled, onLogout: logout, notify }
  let page
  if (path.startsWith('/admin')) page = <AdminPages {...common} />
  else if (path.startsWith('/me')) page = <MePages {...common} onPasswordChanged={async () => { const me=await api<{user:User}>('/api/v1/auth/me');setUser(me.user);setCSRF(me.user.csrfToken);navigate('/app/dashboard') }} />
  else page = <AppPages {...common} />
  return <>{page}{toast && <div className={`toast ${toast.error?'toast-error':''}`}><span>{toast.error?'!':'✓'}</span>{toast.message}</div>}</>
}

export function errorMessage(error: unknown) { return error instanceof APIError ? error.message : error instanceof Error ? error.message : '알 수 없는 오류가 발생했습니다.' }
