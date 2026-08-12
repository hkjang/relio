// Korean labels for every code value that reaches the screen.
//
// The API deliberately returns stable uppercase codes so integrations do not
// break, but a Korean sales team should never have to read REPORTS_TO or
// BEST_CASE. Translation happens here, in one place, so a new screen cannot
// reintroduce raw codes.

const dictionary: Record<string, string> = {
  // 고객
  PROSPECT: '잠재 고객', CUSTOMER: '거래 고객', PARTNER: '파트너',
  GOOD: '양호', NORMAL: '보통', RISK: '주의',

  // 영업기회 · Forecast
  OPEN: '진행 중', WON: '수주', LOST: '실주',
  PIPELINE: '파이프라인', BEST_CASE: '기대', COMMIT: '확약', CLOSED: '종료', OMITTED: '제외',

  // 영업활동
  MEETING: '미팅', CALL: '전화', EMAIL: '이메일', VISIT: '방문', NOTE: '메모', TASK: '할 일',
  DEMO: '시연', PROPOSAL: '제안', OTHER: '기타',

  // 담당자 관계
  DECISION_MAKER: '의사결정자', INFLUENCER: '영향력자', CHAMPION: '지지자', PROCUREMENT: '구매 담당',
  BLOCKER: '반대자', EVALUATOR: '평가자', END_USER: '실사용자',
  HIGH: '높음', MEDIUM: '보통', LOW: '낮음',
  SUPPORT: '우호', NEUTRAL: '중립', OPPOSE: '반대',
  REPORTS_TO: '보고 라인', INFLUENCES: '영향력 행사', WORKS_WITH: '협업',
  BLOCKS: '견제', TRUSTS: '신뢰', ADVISES: '자문',

  // Account Plan · White Space
  DRAFT: '초안', ARCHIVED: '보관',
  NOT_OFFERED: '미제안', DISCOVERY: '요구 파악', OPPORTUNITY: '영업기회 전환',
  NOT_APPLICABLE: '해당 없음',

  // 협업 역할
  PRESALES: '기술영업', CONSULTANT: '컨설턴트', MANAGER: '팀장',
  EXECUTIVE_SPONSOR: '임원 스폰서', LEGAL: '법무', DELIVERY: '구축·이행',

  // 계약 · 매출
  ONE_TIME: '일시 인식', MONTHLY: '월별', QUARTERLY: '분기별', ANNUAL: '연별',
  PLANNED: '예정', RECOGNIZED: '인식 완료', CANCELLED: '취소',
  NOT_STARTED: '미착수', IN_PROGRESS: '진행 중', RENEWED: '갱신 완료', CHURNED: '이탈',
  EXPIRING: '만료 임박', EXPIRED: '만료', DUE: '기한 도래', NEW: '신규',

  // 승인
  PENDING: '검토 대기', APPROVED: '승인', REJECTED: '반려',

  // 권한 · 조직
  USER: '본인', TEAM: '팀', DEPARTMENT: '부서', DIVISION: '본부', COMPANY: '전사',
  LOCAL: '로컬 계정', OIDC: '사내 SSO', PERSONAL_KEY: '개인 키',
  OIDC_ACCESS_TOKEN: 'SSO 토큰',
  SYSTEM_ADMIN: '시스템 관리자', SALES_USER: '영업 담당자', SALES_MANAGER: '영업 팀장',

  // 개인 키
  ACTIVE: '사용 중', ROTATING: '교체 중', REVOKED: '폐기', DISABLED: '중지',

  // 운영 진단 · Job
  HEALTHY: '정상', WARNING: '확인 필요', CRITICAL: '긴급', VERIFIED: '검증됨',
  SUCCESS: '성공', FAILED: '실패', READY: '대기', RUNNING: '실행 중',
  DYNAMIC: '즉시 적용', RESTART_REQUIRED: '재시작 필요',
  ENCRYPTION_KEY: '환경변수 보호', FILE: '볼륨 파일 보호',
  INITIALIZED: '초기화', ADOPTED: '이관', REWRAPPED: '재봉인',

  // Deal Health 규칙
  NO_NEXT_ACTION: '다음 행동 미정', NO_DECISION_MAKER: '의사결정자 미확인',
  NO_CHAMPION: '지지자 없음', NO_RECENT_ACTIVITY: '최근 접점 없음',
  NO_ACTIVITY: '접점 기록 없음', STAGE_STALLED: '단계 정체',
  CLOSE_DATE_PASSED: '예상 계약일 경과', CLOSE_DATE_OVERDUE: '예상 계약일 초과',
  CLOSE_DATE_SLIPPAGE: '계약일 지연', AMOUNT_DROP: '금액 하락',
  PROBABILITY_DROP: '확률 하락', NEW_PIPELINE: '신규 유입',
  AMOUNT_INCREASE: '금액 증가', AMOUNT_DECREASE: '금액 감소',
  SLIPPAGE: '지연', REMOVED: '제외됨',

  // Playbook · Exit Criteria
  CHECKLIST: '체크리스트', ACTION: '권장 행동', FIELD: '입력 항목',
  FIELD_PRESENT: '필드 입력 확인', RECENT_ACTIVITY: '최근 접점 확인',
  PLAYBOOK_COMPLETE: 'Playbook 완료', CUSTOM_FIELD: '사용자 정의 필드',
  OFF: '미적용', BLOCK: '차단', PRESENT: '입력됨',

  // 조직 유형
  QUOTATION: '견적', CONTRACT: '계약',

  // 고객의 목소리 (VOC)
  COMPLAINT: '불만', REQUEST: '요청', INQUIRY: '문의', DEFECT: '품질 이슈',
  PRAISE: '감사·칭찬', CHURN_RISK: '이탈 징후',
  PHONE: '전화', PORTAL: '고객포털', CHAT: '채팅',
  RECEIVED: '접수', IN_REVIEW: '내용 확인', PENDING_CUSTOMER: '고객 회신 대기',
  RESOLVED: '해결',
  CREATED: '접수', STATUS_CHANGE: '상태 변경', COMMENT: '내부 메모',
  CUSTOMER_CONTACT: '고객 응대', ASSIGNED: '담당자 변경', ESCALATED: '상위 보고',
  REOPENED: '재처리', SATISFACTION: '만족도 등록',
}

/** label turns an API code into Korean, leaving unknown values untouched. */
export function label(value?: string | null): string {
  if (!value) return ''
  return dictionary[value] ?? dictionary[value.toUpperCase()] ?? value
}

/** contactRoleLabel resolves the codes a contact role shares with another
 *  domain. USER is a Data Scope meaning "본인" everywhere else, but on a contact
 *  it means the person who actually uses what we sell. */
const contactRoleNames: Record<string, string> = { USER: '실사용자' }
export function contactRoleLabel(value?: string | null): string {
  if (!value) return ''
  return contactRoleNames[value.toUpperCase()] ?? label(value)
}

/** codeLabel shows the Korean name with the original code for administrators. */
export function codeLabel(value?: string | null): string {
  if (!value) return ''
  const korean = label(value)
  return korean === value ? value : `${korean} (${value})`
}

/** initials builds an avatar label, ignoring the punctuation Korean company
 *  names commonly start with so "(주)한빛제조" reads as "한빛" not "(주". */
export function initials(name?: string | null, length = 2): string {
  if (!name) return '?'
  const cleaned = name.replace(/\(주\)|\(유\)|\(재\)|\(사\)|[()[\]{}·,.\-_/\\'"]/g, '').trim()
  const source = cleaned || name.trim()
  return source.slice(0, length) || '?'
}

/** activityIcon is the glyph shown on a timeline entry for each contact type. */
export function activityIcon(type?: string | null): string {
  const glyphs: Record<string, string> = {
    MEETING: '♟', CALL: '☎', EMAIL: '✉', VISIT: '⌂', NOTE: '▤',
    TASK: '✓', DEMO: '▶', PROPOSAL: '▦',
  }
  return glyphs[(type || '').toUpperCase()] ?? '•'
}
