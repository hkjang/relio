import { FormEvent, useState } from 'react'
import { api } from '../api'
import { AuthStatus, User, Version } from '../types'
import { errorMessage } from '../App'

// The callback used to redirect with a single opaque code, so a user whose
// account simply was not provisioned saw the same message as a Keycloak outage.
const ssoErrors: Record<string,{title:string;detail:string}> = {
  not_provisioned: { title: 'Relio에 등록되지 않은 계정입니다', detail: 'SSO 인증은 성공했지만 이 계정은 아직 Relio 사용자로 등록되지 않았습니다. 관리자에게 계정 생성 또는 자동 생성(Auto Provisioning) 활성화를 요청하세요.' },
  no_default_role: { title: '신규 사용자에게 부여할 Role이 없습니다', detail: '관리자가 Role 화면에서 기본 Sign-in Role을 지정하거나 OIDC 설정에서 기본 Role을 선택해야 로그인할 수 있습니다.' },
  state_expired: { title: '로그인 요청이 만료되었습니다', detail: '10분 안에 로그인을 완료해야 합니다. 다시 시도해 주세요.' },
  token_exchange_failed: { title: 'Keycloak이 Client 인증을 거부했습니다', detail: '관리자에게 Client ID와 Client Secret, Valid Redirect URI 설정 확인을 요청하세요.' },
  token_invalid: { title: 'ID Token을 검증하지 못했습니다', detail: '서버 시간 동기화와 Keycloak 서명 키(JWKS) 설정을 확인해야 합니다.' },
  claim_missing: { title: '사용자 정보 Claim이 비어 있습니다', detail: 'Keycloak Client의 Username 또는 Email Claim Mapper 설정을 관리자에게 확인 요청하세요.' },
  discovery_failed: { title: 'Keycloak에 연결하지 못했습니다', detail: 'Issuer URL, 네트워크 경로와 사내 Root CA 인증서를 관리자에게 확인 요청하세요.' },
  callback_failed: { title: 'SSO 로그인에 실패했습니다', detail: '관리자에게 연결 설정을 확인해 달라고 요청하세요.' },
}

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
      {ssoError && <div className="alert alert-error"><b>{(ssoErrors[ssoError] || ssoErrors.callback_failed).title}</b><span>{(ssoErrors[ssoError] || ssoErrors.callback_failed).detail}</span><small className="sso-error-code">오류 코드: {ssoError}</small></div>}
      {status?.sso.enabled && <><a className="btn btn-sso" href="/api/v1/auth/oidc/start"><span className="keycloak-symbol">K</span>사내 SSO로 로그인<span>→</span></a><div className="divider"><span>또는 관리자 계정</span></div></>}
      <form onSubmit={submit} className="login-form"><label>{status?.localLoginEnabled === false?'Bootstrap 관리자':'관리자 계정'}<input autoFocus value={username} onChange={e=>setUsername(e.target.value)} autoComplete="username" placeholder="아이디를 입력하세요" required/></label><label>비밀번호<input type="password" value={password} onChange={e=>setPassword(e.target.value)} autoComplete="current-password" placeholder="비밀번호를 입력하세요" required/></label><button className="btn btn-primary btn-block" disabled={busy}>{busy?<><span className="spinner small"/>로그인 중…</>:'관리자 계정으로 로그인'}</button></form>
      <p className="breakglass">Bootstrap 관리자는 SSO 장애 시에도 사용할 수 있는 Break Glass 계정입니다.</p>
    </div><footer className="login-version"><b>Relio v{version.version}</b><span>Build {version.gitCommit.slice(0,8)}</span><span>·</span><span>{version.edition}</span></footer></main>
  </div>
}
