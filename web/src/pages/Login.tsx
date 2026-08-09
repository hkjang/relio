import { FormEvent, useState } from 'react'
import { api } from '../api'
import { AuthStatus, User, Version } from '../types'
import { errorMessage } from '../App'

export default function Login({ status, version, onLogin, notify }: { status: AuthStatus|null; version: Version; onLogin: (u:User)=>void; notify:(m:string,e?:boolean)=>void }) {
  const [username,setUsername]=useState('')
  const [password,setPassword]=useState('')
  const [busy,setBusy]=useState(false)
  async function submit(e:FormEvent){e.preventDefault();setBusy(true);try{const result=await api<{user:User}>('/api/v1/auth/login',{method:'POST',body:JSON.stringify({username,password})});onLogin(result.user)}catch(err){notify(errorMessage(err),true)}finally{setBusy(false)}}
  const ssoError=new URLSearchParams(location.search).get('sso_error')
  return <div className="login-page">
    <section className="login-story">
      <div className="story-grid"/><div className="story-glow"/>
      <div className="login-brand"><span className="brand-mark">R</span><b>Relio</b></div>
      <div className="story-copy"><p className="eyebrow light">RELATIONSHIP + IO</p><h1>모든 고객 관계가<br/><em>하나로 연결되는 곳</em></h1><p>고객의 맥락부터 영업의 다음 행동까지.<br/>Relio가 팀의 관계와 성장을 연결합니다.</p><div className="story-points"><span><i>✓</i> Customer 360</span><span><i>✓</i> Sales Pipeline</span><span><i>✓</i> Secure API & MCP</span></div></div>
      <p className="offline-note"><span>●</span> 완전한 오프라인 환경에서 안전하게 운영됩니다</p>
    </section>
    <main className="login-main"><div className="login-card">
      <div className="login-heading"><div className="mobile-logo"><span className="brand-mark">R</span><b>Relio</b></div><p className="eyebrow">WELCOME BACK</p><h2>Relio에 로그인</h2><p>계속하려면 인증 방식을 선택하세요.</p></div>
      {ssoError && <div className="alert alert-error">SSO 로그인에 실패했습니다. 관리자에게 연결 설정을 확인해 달라고 요청하세요.</div>}
      {status?.sso.enabled && <><a className="btn btn-sso" href="/api/v1/auth/oidc/start"><span className="keycloak-symbol">K</span>사내 SSO로 로그인<span>→</span></a><div className="divider"><span>또는 관리자 계정</span></div></>}
      <form onSubmit={submit} className="login-form"><label>{status?.localLoginEnabled === false?'Bootstrap 관리자':'관리자 계정'}<input autoFocus value={username} onChange={e=>setUsername(e.target.value)} autoComplete="username" placeholder="아이디를 입력하세요" required/></label><label>비밀번호<input type="password" value={password} onChange={e=>setPassword(e.target.value)} autoComplete="current-password" placeholder="비밀번호를 입력하세요" required/></label><button className="btn btn-primary btn-block" disabled={busy}>{busy?<><span className="spinner small"/>로그인 중…</>:'관리자 계정으로 로그인'}</button></form>
      <p className="breakglass">Bootstrap 관리자는 SSO 장애 시에도 사용할 수 있는 Break Glass 계정입니다.</p>
    </div><footer className="login-version"><b>Relio v{version.version}</b><span>Build {version.gitCommit.slice(0,8)}</span><span>·</span><span>{version.edition}</span></footer></main>
  </div>
}
