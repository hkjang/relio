// The navigation map, in one place so the sidebar and the command palette can
// never disagree about where something lives or what it is called.

export type NavItem = { to: string; key: string; label: string; group?: string; keywords?: string; description?: string }

// Shared by both admin discovery surfaces. All words must match, in any order.
export function matchesNav(item: NavItem, query: string) {
  const text = `${item.label} ${item.group || ''} ${item.keywords || ''} ${item.description || ''}`.normalize('NFKC').toLocaleLowerCase('ko-KR')
  return query.normalize('NFKC').toLocaleLowerCase('ko-KR').trim().split(/\s+/).every(word => text.includes(word))
}

export const navIcon: Record<string, string> = {
  dashboard: '⌂', operations: '◉', customers: '◫', opportunities: '◆', pipeline: '▦',
  intelligence: '◇', activities: '◷', forecast: '↗', contracts: '▤', approvals: '✓',
  admin: '⚙', me: '◎', search: '⌕', security: '◈', api: '⌁', data: '⇄',
}

export const appNav: NavItem[] = [
  { to: '/app/dashboard', key: 'dashboard', label: '영업 현황', group: '오늘', keywords: 'dashboard 홈 kpi' },
  { to: '/app/recommendations', key: 'security', label: '내 추천 행동', group: '오늘', keywords: 'recommendation 추천 할일 next action' },
  { to: '/app/customers', key: 'customers', label: '고객', group: '영업', keywords: 'customer account 거래처' },
  { to: '/app/opportunities', key: 'opportunities', label: '영업기회', group: '영업', keywords: 'opportunity deal 딜' },
  { to: '/app/pipeline', key: 'pipeline', label: '파이프라인', group: '영업', keywords: 'pipeline kanban stage 단계' },
  { to: '/app/activities', key: 'activities', label: '영업활동', group: '영업', keywords: 'activity 미팅 통화 방문 timeline' },
  { to: '/app/intelligence-center', key: 'intelligence', label: '고객 위험 분석', group: '분석', keywords: 'intelligence signal risk 위험 신호 추천' },
  { to: '/app/intelligence', key: 'opportunities', label: '영업기회 분석', group: '분석', keywords: 'deal health coaching 코칭 위험' },
  { to: '/app/forecast', key: 'forecast', label: '매출 전망', group: '분석', keywords: 'forecast 전망 snapshot waterfall' },
  { to: '/app/voices', key: 'approvals', label: '고객의 목소리', group: '고객 관리', keywords: 'voc 불만 요청 문의 이탈' },
  { to: '/app/contracts', key: 'contracts', label: '계약', group: '고객 관리', keywords: 'contract 갱신 renewal 매출인식' },
]

export const meNav: NavItem[] = [
  { to: '/me/profile', key: 'me', label: '내 프로필', keywords: 'profile 계정' },
  { to: '/me/dashboard', key: 'dashboard', label: '내 현황', keywords: 'my dashboard' },
  { to: '/me/targets', key: 'forecast', label: '내 영업목표', keywords: 'target quota 목표' },
  { to: '/me/calendar', key: 'activities', label: '내 일정', keywords: 'calendar 일정' },
  { to: '/me/notifications', key: 'approvals', label: '내 알림', keywords: 'notification 알림' },
  { to: '/me/saved', key: 'search', label: '저장된 검색', keywords: 'saved view 검색 저장' },
  { to: '/me/favorites', key: 'customers', label: '즐겨찾기', keywords: 'favorite 즐겨찾기 star' },
  { to: '/me/keys', key: 'pipeline', label: '개인 연동 키', keywords: 'api key mcp token 개인키' },
  { to: '/me/sessions', key: 'me', label: '로그인 세션', keywords: 'session 세션 로그아웃' },
  { to: '/me/activity', key: 'activities', label: '활동 기록', keywords: 'audit 내 기록' },
  { to: '/me/about', key: 'admin', label: 'Relio 정보', keywords: 'version 버전 about' },
]

export const adminNav: NavItem[] = [
  { to: '/admin/overview', key: 'dashboard', label: '운영 현황판', group: '운영', keywords: '현황 준비도 action', description: '서비스 준비도와 우선 조치를 한눈에 확인합니다.' },
  { to: '/admin/operations', key: 'operations', label: '시스템 진단 · 작업', group: '운영', keywords: '진단 support bundle migration database', description: '실행 환경, 데이터 키와 백그라운드 작업을 진단합니다.' },
  { to: '/admin/audit', key: 'activities', label: '감사 로그', group: '운영', keywords: '감사 이력 추적', description: '누가 언제 무엇을 변경했는지 추적합니다.' },
  { to: '/admin/system', key: 'admin', label: '시스템 기본정보', group: '기본 설정', keywords: 'url locale timezone', description: '서비스 이름, 접속 주소와 지역 설정을 관리합니다.' },
  { to: '/admin/oidc', key: 'me', label: '사내 SSO 연결', group: '기본 설정', keywords: 'sso issuer client claim', description: 'Keycloak 연결과 사용자·권한 매핑을 설정합니다.' },
  { to: '/admin/security', key: 'security', label: '보안 · 파일 · 접속', group: '기본 설정', keywords: 'local login rate export', description: '로그인, 세션, 요청 한도와 업로드를 제어합니다.' },
  { to: '/admin/analytics', key: 'operations', label: '방문자 분석 · CSP', group: '기본 설정', keywords: 'analytics ga matomo plausible umami csp 추적 스크립트 차단', description: '방문자 분석 공급자와 스크립트 보안을 관리합니다.' },
  { to: '/admin/mail', key: 'activities', label: '메일 알림', group: '기본 설정', keywords: 'mail smtp 메일 알림 릴레이 발송 기록 시험 발송', description: 'SMTP 연결, 이벤트 알림과 발송 결과를 확인합니다.' },
  { to: '/admin/users', key: 'customers', label: '사용자 · 조직', group: '조직 · 권한', keywords: 'user organization team', description: '계정, 조직, 소속과 활성 상태를 관리합니다.' },
  { to: '/admin/roles', key: 'approvals', label: '권한 · 데이터 범위', group: '조직 · 권한', keywords: 'permission rbac', description: '기능 권한과 데이터 접근 범위를 설계합니다.' },
  { to: '/admin/pipeline', key: 'pipeline', label: '영업 단계 설정', group: '영업 정책', keywords: 'stage probability forecast', description: '영업 단계, 성공확률과 전망 분류를 설정합니다.' },
  { to: '/admin/sales-execution', key: 'intelligence', label: '영업 실행 정책', group: '영업 정책', keywords: 'playbook exit criteria health', description: '단계별 실행 지침과 위험 판단 기준을 관리합니다.' },
  { to: '/admin/relationships', key: 'customers', label: '관계 분석', group: '영업 정책', keywords: 'graph account plan team', description: '관계도 표시 범위와 영업 협업 역할을 설정합니다.' },
  { to: '/admin/approval', key: 'approvals', label: '승인 절차', group: '영업 정책', keywords: 'review approve reject', description: '승인 대상, 조건과 검토 순서를 관리합니다.' },
  { to: '/admin/custom-fields', key: 'opportunities', label: '사용자 정의 항목', group: '영업 정책', keywords: 'metadata jsonb field', description: '업무에 필요한 입력 항목과 허용값을 정의합니다.' },
  { to: '/admin/products', key: 'contracts', label: '상품 카탈로그', group: '영업 정책', keywords: 'product price catalog 단가 상품', description: '견적에 사용할 상품, 코드와 단가를 관리합니다.' },
  { to: '/admin/voice-categories', key: 'approvals', label: '고객 요청 유형 · 업무 영역', group: '영업 정책', keywords: 'voc 불만 요청 문의 sla 응답 해결 업무 영역 부서 회원사 템플릿 지식', description: '부서별 민원 유형, SLA와 검토 권한을 설정합니다.' },
  { to: '/admin/keys', key: 'api', label: '연동 키 · API · MCP', group: '개발자', keywords: 'rotation scope origin tool', description: '연동 키 수명, API·MCP 접근 정책을 관리합니다.' },
  { to: '/admin/data', key: 'data', label: '데이터 품질 · 설정', group: '데이터', keywords: 'quality data configuration bundle export import diff', description: '데이터 품질을 점검하고 설정을 비교·이관합니다.' },
]

/** QuickAction is something the palette can do rather than somewhere it can go. */
export type QuickAction = { key: string; label: string; hint: string; to: string; permission?: string }

export const quickActions: QuickAction[] = [
  { key: 'new-customer', label: '고객 등록', hint: '새 고객을 등록합니다', to: '/app/customers?new=1', permission: 'customer:write' },
  { key: 'new-opportunity', label: '영업기회 생성', hint: '새 영업기회를 만듭니다', to: '/app/opportunities?new=1', permission: 'opportunity:write' },
  { key: 'new-activity', label: '영업활동 기록', hint: '미팅, 통화, 방문을 기록합니다', to: '/app/activities?new=1', permission: 'activity:write' },
  { key: 'new-voice', label: '고객 요청 접수', hint: '불만, 요청, 문의를 접수합니다', to: '/app/voices?new=1', permission: 'voice:write' },
  { key: 'my-recommendations', label: '내 추천 행동 열기', hint: '처리 대기 중인 추천을 봅니다', to: '/app/recommendations', permission: 'intelligence:read' },
]
