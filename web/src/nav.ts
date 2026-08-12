// The navigation map, in one place so the sidebar and the command palette can
// never disagree about where something lives or what it is called.

export type NavItem = { to: string; key: string; label: string; group?: string; keywords?: string }

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
  { to: '/admin/overview', key: 'dashboard', label: '운영 현황판', group: '운영', keywords: '현황 준비도 action' },
  { to: '/admin/operations', key: 'operations', label: '시스템 진단 · 작업', group: '운영', keywords: '진단 support bundle migration database' },
  { to: '/admin/audit', key: 'activities', label: '감사 로그', group: '운영', keywords: '감사 이력 추적' },
  { to: '/admin/system', key: 'admin', label: '시스템 기본정보', group: '기본 설정', keywords: 'url locale timezone' },
  { to: '/admin/oidc', key: 'me', label: '사내 SSO 연결', group: '기본 설정', keywords: 'sso issuer client claim' },
  { to: '/admin/security', key: 'security', label: '보안 · 파일 · 접속', group: '기본 설정', keywords: 'local login rate export' },
  { to: '/admin/analytics', key: 'operations', label: '방문자 분석 · CSP', group: '기본 설정', keywords: 'analytics ga matomo plausible umami csp 추적 스크립트 차단' },
  { to: '/admin/users', key: 'customers', label: '사용자 · 조직', group: '조직 · 권한', keywords: 'user organization team' },
  { to: '/admin/roles', key: 'approvals', label: '권한 · 데이터 범위', group: '조직 · 권한', keywords: 'permission rbac' },
  { to: '/admin/pipeline', key: 'pipeline', label: '영업 단계 설정', group: '영업 정책', keywords: 'stage probability forecast' },
  { to: '/admin/sales-execution', key: 'intelligence', label: '영업 실행 정책', group: '영업 정책', keywords: 'playbook exit criteria health' },
  { to: '/admin/relationships', key: 'customers', label: '관계 분석', group: '영업 정책', keywords: 'graph account plan team' },
  { to: '/admin/approval', key: 'approvals', label: '승인 절차', group: '영업 정책', keywords: 'review approve reject' },
  { to: '/admin/custom-fields', key: 'opportunities', label: '사용자 정의 항목', group: '영업 정책', keywords: 'metadata jsonb field' },
  { to: '/admin/products', key: 'contracts', label: '상품 카탈로그', group: '영업 정책', keywords: 'product price catalog 단가 상품' },
  { to: '/admin/voice-categories', key: 'approvals', label: '고객 요청 유형 · SLA', group: '영업 정책', keywords: 'voc 불만 요청 문의 sla 응답 해결' },
  { to: '/admin/keys', key: 'api', label: '연동 키 · API · MCP', group: '개발자', keywords: 'rotation scope origin tool' },
  { to: '/admin/data', key: 'data', label: '데이터 품질 · 설정', group: '데이터', keywords: 'quality data configuration bundle export import diff' },
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
