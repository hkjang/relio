import { FormEvent, useState } from 'react'
import { api } from '../api'
import { AuthStatus, User, Version } from '../types'
import { errorMessage, pendingReturn } from '../App'

// The callback used to redirect with a single opaque code, so a user whose
// account simply was not provisioned saw the same message as a Keycloak outage.
const ssoErrors: Record<string,{title:string;detail:string}> = {
  not_provisioned: { title: 'Relio에 등록되지 않은 계정입니다', detail: 'SSO 인증은 성공했지만 이 계정은 아직 Relio 사용자로 등록되지 않았습니다. 관리자에게 계정 생성 또는 자동 생성 활성화를 요청하세요.' },
  no_default_role: { title: '신규 사용자에게 부여할 권한이 없습니다', detail: '관리자가 권한 화면에서 기본 권한을 지정하거나 SSO 설정에서 기본 권한을 선택해야 로그인할 수 있습니다.' },
  state_expired: { title: '로그인 요청이 만료되었습니다', detail: '10분 안에 로그인을 완료해야 합니다. 다시 시도해 주세요.' },
  token_exchange_failed: { title: '사내 SSO가 클라이언트 인증을 거부했습니다', detail: '관리자에게 클라이언트 인증 정보와 허용 리다이렉트 주소 설정 확인을 요청하세요.' },
  token_invalid: { title: '로그인 토큰을 검증하지 못했습니다', detail: '서버 시간 동기화와 사내 SSO 서명 키 설정을 확인해야 합니다.' },
  claim_missing: { title: '사용자 정보가 비어 있습니다', detail: 'Keycloak Client의 Username 또는 이메일 항목 Mapper 설정을 관리자에게 확인 요청하세요.' },
  discovery_failed: { title: '사내 SSO에 연결하지 못했습니다', detail: 'Issuer URL, 네트워크 경로와 사내 Root CA 인증서를 관리자에게 확인 요청하세요.' },
  callback_failed: { title: 'SSO 로그인에 실패했습니다', detail: '관리자에게 연결 설정을 확인해 달라고 요청하세요.' },
}

// Inline so the login screen needs no icon package; each is decorative, the
// button text carries the meaning.
const ShieldIcon = () => <svg aria-hidden="true" width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/><path d="m9 12 2 2 4-4"/></svg>
const LockIcon = () => <svg aria-hidden="true" width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="4" y="11" width="16" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/></svg>
const ChevronIcon = ({ open }: { open: boolean }) => <svg aria-hidden="true" className={`chevron${open ? ' open' : ''}`} width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="m6 9 6 6 6-6"/></svg>

export default function Login({ status, version, onLogin, notify }: { status: AuthStatus|null; version: Version; onLogin: (u:User)=>void; notify:(m:string,e?:boolean)=>void }) {
  const [username,setUsername]=useState('')
  const [password,setPassword]=useState('')
  const [busy,setBusy]=useState(false)
  const params=new URLSearchParams(location.search)
  const ssoOn=!!status?.sso.enabled
  // With SSO on, the organisation account is the way in and the local admin is
  // a break-glass path, so it waits behind a disclosure instead of competing
  // with the SSO button. Without SSO it is the only way in and stays open.
  const [localOpen,setLocalOpen]=useState(!ssoOn)
  async function submit(e:FormEvent){e.preventDefault();setBusy(true);try{const result=await api<{user:User}>('/api/v1/auth/login',{method:'POST',body:JSON.stringify({username,password})});onLogin(result.user)}catch(err){notify(errorMessage(err),true)}finally{setBusy(false)}}
  const ssoError=params.get('sso_error')
  // The callback lands on /login?sso=none when a silent attempt found no
  // Keycloak session. That is not a failure, but without a word the user sees
  // a login screen appear for no apparent reason.
  const ssoRefused=params.get('sso')==='none'&&!ssoError
  return <div className="login-page">
    <section className="login-story">
      <div className="story-grid"/><div className="story-glow"/>
      <div className="login-brand"><span className="brand-mark">R</span><b>Relio</b></div>
      <div className="story-copy"><p className="eyebrow light">고객 관계 플랫폼</p><h1>모든 고객 관계가<br/><em>하나로 연결되는 곳</em></h1><p>고객의 맥락부터 영업의 다음 행동까지.<br/>Relio가 팀의 고객 관계와 성장을 연결합니다.</p><div className="story-points"><span><i>✓</i> 고객 360</span><span><i>✓</i> 영업 파이프라인</span><span><i>✓</i> 안전한 API · MCP</span></div></div>
      <p className="offline-note"><span>●</span> 완전한 오프라인 환경에서 안전하게 운영됩니다</p>
    </section>
    <main className="login-main"><div className="login-card">
      <div className="login-heading"><div className="mobile-logo"><span className="brand-mark">R</span><b>Relio</b></div><p className="eyebrow">다시 만나 반갑습니다</p><h2>Relio에 로그인</h2><p>인증 후 원래 화면으로 안전하게 돌아갑니다.</p></div>
      {ssoError && <div className="alert alert-error"><b>{(ssoErrors[ssoError] || ssoErrors.callback_failed).title}</b><span>{(ssoErrors[ssoError] || ssoErrors.callback_failed).detail}</span><small className="sso-error-code">오류 코드: {ssoError}</small></div>}
      {ssoOn && ssoRefused && <div className="login-notice" role="status"><ShieldIcon/><span>조직 계정 세션이 없어 자동으로 로그인하지 않았습니다. 아래 버튼으로 로그인하세요.</span></div>}
      {ssoOn && <a className="btn btn-sso" href={`/api/v1/auth/oidc/start?return_to=${encodeURIComponent(pendingReturn())}`}><ShieldIcon/>조직 계정으로 SSO 로그인</a>}
      {ssoOn && <button type="button" className="local-login-toggle" aria-expanded={localOpen} aria-controls="local-login-form" onClick={()=>setLocalOpen(open=>!open)}><LockIcon/><span>관리자 계정으로 로그인</span><ChevronIcon open={localOpen}/></button>}
      {localOpen && <form id="local-login-form" onSubmit={submit} className="login-form">
        <div className="login-notice subtle"><LockIcon/><span>{ssoOn?'SSO를 사용할 수 없을 때를 위한 복구용 관리자 계정입니다. 평소에는 조직 계정으로 로그인하세요.':'설치 관리자 계정입니다. SSO를 설정한 뒤에도 장애 시 복구용으로 계속 사용할 수 있습니다.'}</span></div>
        <label>{status?.localLoginEnabled === false?'Bootstrap 관리자':'관리자 계정'}<input autoFocus value={username} onChange={e=>setUsername(e.target.value)} autoComplete="username" placeholder="아이디를 입력하세요" required/></label>
        <label>비밀번호<input type="password" value={password} onChange={e=>setPassword(e.target.value)} autoComplete="current-password" placeholder="비밀번호를 입력하세요" required/></label>
        <button className={`btn btn-block ${ssoOn?'btn-secondary':'btn-primary'}`} disabled={busy}>{busy?<><span className="spinner small"/>로그인 중…</>:'관리자 계정으로 로그인'}</button>
      </form>}
    </div><footer className="login-version"><b>Relio v{version.version}</b><span>빌드 {version.gitCommit.slice(0,8)}</span><span>·</span><span>{version.edition}</span></footer></main>
  </div>
}
