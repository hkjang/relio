import { FormEvent, useMemo, useState } from 'react'
import { api } from '../api'
import { Modal } from './Layout'
import { errorMessage } from '../App'
import type { PersonalKey } from '../types'

// Issuing a key used to mean ticking boxes in a flat grid of raw scope strings.
// Nobody reads thirty codes; people either tick everything or tick too little
// and find out days later that an agent cannot do its job. The picker is grouped,
// says what each scope means, and can be driven in one click for the cases that
// actually come up: everything, nothing, read-only, or write.

type Notify = (m: string, e?: boolean) => void

export type McpToolCatalog = {
  name: string
  title: string
  requiredScopes: string[]
  readOnly: boolean
}

const domainNames: Record<string, string> = {
  customer: '고객', contact: '담당자', lead: 'Lead', opportunity: '영업기회',
  activity: '영업활동', product: '상품', quotation: '견적', contract: '계약',
  sales: '매출', target: '영업목표', forecast: '매출 전망', report: '보고서',
  notification: '알림', approval: '승인', voice: '고객의 목소리',
  intelligence: '위험 분석', analytics: '방문자 분석', admin: '관리', mcp: '연동',
}

const actionNames: Record<string, string> = {
  read: '조회', write: '등록·수정', delete: '삭제', run: '실행',
  manage: '관리', use: '사용', request: '요청', approve: '승인', '*': '전체',
}

export const scopeDomain = (scope: string) => scope.split(':')[0]
export const scopeAction = (scope: string) => scope.split(':')[1] || ''
export const scopeLabel = (scope: string) =>
  `${domainNames[scopeDomain(scope)] || scopeDomain(scope)} ${actionNames[scopeAction(scope)] || scopeAction(scope)}`

/** readOnly is the distinction the bulk buttons are built on: a scope that can
 *  only look, versus one that can change something. */
export const isReadOnlyScope = (scope: string) => scopeAction(scope) === 'read'
const isWriteScope = (scope: string) =>
  ['write', 'delete', 'manage', 'run', 'approve', 'request', '*'].includes(scopeAction(scope))

// mcp:use is not a data permission — it is the switch that lets a key speak MCP
// at all. A key with every CRM scope and no mcp:use authenticates and then fails
// every call, which is a confusing way to spend an afternoon.
const CHANNEL_SCOPE = 'mcp:use'

const presets = [
  { key: 'read', label: '조회 전용 에이전트', hint: '데이터를 읽기만 합니다', pick: (all: string[]) => all.filter(isReadOnlyScope) },
  { key: 'sales', label: '영업 자동화', hint: '고객·영업기회·활동을 등록하고 수정합니다', pick: (all: string[]) =>
    all.filter(s => ['customer', 'contact', 'opportunity', 'activity', 'lead'].includes(scopeDomain(s)) && !['delete'].includes(scopeAction(s)))
      .concat(all.filter(s => ['forecast:read', 'contract:read', 'quotation:read', 'intelligence:read'].includes(s))) },
  { key: 'voc', label: '고객 요청 처리', hint: '고객의 목소리를 접수하고 처리합니다', pick: (all: string[]) =>
    all.filter(s => scopeDomain(s) === 'voice' || ['customer:read', 'contact:read', 'contract:read'].includes(s)) },
  { key: 'minimal', label: '최소 권한', hint: '고객 조회만 허용합니다', pick: (all: string[]) => all.filter(s => s === 'customer:read') },
]

export function KeyModal({ scopes, tools, onClose, onCreated, onUpdated, notify, editing }: {
  scopes: string[]; tools: McpToolCatalog[]; onClose: () => void; onCreated?: (secret: string) => void; onUpdated?: () => void; notify: Notify; editing?: PersonalKey
}) {
  const defaultScopes = ['customer:read', 'opportunity:read', 'activity:read', 'forecast:read', CHANNEL_SCOPE]
  const editableInitial = editing?.scopes.filter(scope => scopes.includes(scope))
  const mcpAvailable = scopes.includes(CHANNEL_SCOPE)
  const [selected, setSelected] = useState<string[]>(editableInitial || defaultScopes.filter(scope => scopes.includes(scope)))
  const [rest, setRest] = useState(editing ? editing.channels.includes('REST') : true)
  const [mcp, setMcp] = useState(editing ? editing.channels.includes('MCP') && mcpAvailable : mcpAvailable)
  const [busy, setBusy] = useState(false)
  const [guide, setGuide] = useState(false)

  const dataScopes = useMemo(() => scopes.filter(s => s !== CHANNEL_SCOPE), [scopes])
  const groups = useMemo(() => {
    const byDomain = new Map<string, string[]>()
    for (const scope of dataScopes) {
      const domain = scopeDomain(scope)
      byDomain.set(domain, [...(byDomain.get(domain) || []), scope])
    }
    return [...byDomain.entries()]
  }, [dataScopes])

  const has = (scope: string) => selected.includes(scope)
  const toggle = (scope: string, on: boolean) =>
    setSelected(current => on ? [...new Set([...current, scope])] : current.filter(x => x !== scope))
  const setMany = (list: string[], on: boolean) =>
    setSelected(current => on ? [...new Set([...current, ...list])] : current.filter(x => !list.includes(x)))
  // Bulk actions never touch mcp:use: it is a channel switch, not data access.
  const apply = (list: string[]) => setSelected([...new Set([...list, ...(mcp ? [CHANNEL_SCOPE] : [])])])

  const readable = dataScopes.filter(isReadOnlyScope)
  const writable = dataScopes.filter(isWriteScope)
  const chosenData = selected.filter(s => s !== CHANNEL_SCOPE)
  const writeCount = chosenData.filter(isWriteScope).length
  const selectedMcpTools = mcp ? tools.filter(tool => tool.requiredScopes.every(has)) : []
  const missingChannelScope = mcp && !has(CHANNEL_SCOPE)
  const unavailableCount = editing ? editing.scopes.filter(scope => !scopes.includes(scope)).length : 0

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = new FormData(e.currentTarget)
    setBusy(true)
    try {
      const access = {
        scopes: mcp ? [...new Set([...selected, CHANNEL_SCOPE])] : selected.filter(s => s !== CHANNEL_SCOPE),
        channels: [...(rest ? ['REST'] : []), ...(mcp ? ['MCP'] : [])],
      }
      if (editing) {
        await api(`/api/v1/me/keys/${editing.id}`, {
          method: 'PUT', body: JSON.stringify({ ...access, version: editing.version }),
        })
        notify('키 권한을 변경했습니다. 다음 요청부터 즉시 적용됩니다.')
        onUpdated?.()
      } else {
        const result = await api<{ secret: string }>('/api/v1/me/keys', {
          method: 'POST', body: JSON.stringify({ name: form.get('name'), ...access }),
        })
        onCreated?.(result.secret)
      }
    } catch (err) { notify(errorMessage(err), true) } finally { setBusy(false) }
  }

  return <Modal title={editing ? '키 권한 변경' : '새 개인 연동 키'} onClose={onClose} wide>
    <form className="form key-form" onSubmit={submit}>
      {editing ? <div className="key-edit-target">
        <span>변경 대상</span><b>{editing.name}</b><code>relio_{editing.keyId}_••••••</code>
        <small>Secret은 바뀌지 않으며 저장한 권한은 다음 API·MCP 요청부터 적용됩니다.</small>
      </div> : <label>키 이름 *<input name="name" required autoFocus placeholder="예: 영업 리포트 Agent" />
        <small>어디에 쓰는 키인지 알아볼 수 있게 적으세요. 나중에 폐기할 때 기준이 됩니다.</small></label>}

      <fieldset>
        <legend>사용 채널</legend>
        <div className="check-row">
          <label><input type="checkbox" checked={rest} onChange={e => setRest(e.target.checked)} /> REST API</label>
          <label title={mcpAvailable ? undefined : '본인에게 mcp:use 권한이 없습니다'}><input type="checkbox" checked={mcp} disabled={!mcpAvailable} onChange={e => setMcp(e.target.checked)} /> MCP (AI 에이전트)</label>
        </div>
        {mcp && <p className="field-note">
          MCP 연결 방법이 필요하면 <button type="button" className="link-button" onClick={() => setGuide(true)}>MCP 사용 안내</button>를 확인하세요.
        </p>}
      </fieldset>

      <fieldset>
        <legend>권한 범위 <small>본인 권한보다 넓게 부여되지 않습니다</small></legend>

        {unavailableCount > 0 && <p className="field-note warn">
          현재 본인 권한에서 제외된 {unavailableCount}개 Scope는 저장 시 키에서도 제거됩니다.
        </p>}

        <div className="scope-presets">
          {presets.map(preset => <button key={preset.key} type="button" className="preset-chip"
            onClick={() => apply(preset.pick(dataScopes))} title={preset.hint}>
            {preset.label}
          </button>)}
        </div>

        <div className="scope-bulk">
          <button type="button" onClick={() => setMany(dataScopes, true)}>전체 선택</button>
          <button type="button" onClick={() => setSelected(mcp ? [CHANNEL_SCOPE] : [])}>전체 해제</button>
          <span className="scope-bulk-divider" />
          <button type="button" onClick={() => setMany(readable, true)}>조회 전체 선택</button>
          <button type="button" onClick={() => setMany(readable, false)}>조회 전체 해제</button>
          <span className="scope-bulk-divider" />
          <button type="button" onClick={() => setMany(writable, true)}>입력 전체 선택</button>
          <button type="button" onClick={() => setMany(writable, false)}>입력 전체 해제</button>
        </div>

        <div className="scope-groups">
          {groups.map(([domain, list]) => {
            const all = list.every(has)
            const some = list.some(has)
            return <div className="scope-group" key={domain}>
              <div className="scope-group-head">
                <label className="scope-group-toggle">
                  <input type="checkbox" checked={all} ref={el => { if (el) el.indeterminate = some && !all }}
                    onChange={e => setMany(list, e.target.checked)} />
                  <b>{domainNames[domain] || domain}</b>
                </label>
                <small>{list.filter(has).length}/{list.length}</small>
              </div>
              <div className="scope-items">
                {list.map(scope => {
                  const linkedTools = tools.filter(tool => tool.requiredScopes.includes(scope)).length
                  return <label key={scope} className={`scope-item ${isWriteScope(scope) ? 'is-write' : ''}`}>
                  <input type="checkbox" checked={has(scope)} onChange={e => toggle(scope, e.target.checked)} />
                  <span><b>{actionNames[scopeAction(scope)] || scopeAction(scope)}</b><code>{scope}</code>
                    <em className={linkedTools ? 'scope-mcp-count' : 'scope-rest-only'}>{linkedTools ? `MCP 연계 ${linkedTools}` : 'REST 전용'}</em>
                  </span>
                </label>})}
              </div>
            </div>
          })}
        </div>

        <div className="scope-summary">
          <b>{chosenData.length}개 선택됨</b>
          <span>{writeCount > 0 ? `이 중 ${writeCount}개는 데이터를 변경할 수 있습니다.` : '조회 전용입니다. 데이터를 변경할 수 없습니다.'}</span>
          {mcp && <strong>MCP 도구 {selectedMcpTools.length}개 노출</strong>}
        </div>
        {mcp && selectedMcpTools.length === 0 && <p className="field-note warn">
          현재 선택 조합으로 노출되는 MCP 도구가 없습니다. 각 도구에 필요한 Scope를 함께 선택하거나 관리자 Tool 허용목록을 확인하세요.
        </p>}
        {missingChannelScope && <p className="field-note warn">
          MCP 채널을 사용하려면 <code>mcp:use</code>가 필요합니다. 발급 시 자동으로 포함됩니다.
        </p>}
      </fieldset>

      <div className="modal-actions">
        <button type="button" className="btn btn-ghost" onClick={onClose}>취소</button>
        <button className="btn btn-primary" disabled={busy || !chosenData.length || (!rest && !mcp)}>
          {busy ? (editing ? '저장 중…' : '발급 중…') : (editing ? '권한 저장' : '키 발급')}
        </button>
      </div>
    </form>
    {guide && <McpGuideModal onClose={() => setGuide(false)} />}
  </Modal>
}

/** McpGuideModal is the connection instructions, kept next to the key that needs
 *  them rather than in a document nobody opens. */
export function McpGuideModal({ onClose, keyPreview }: { onClose: () => void; keyPreview?: string }) {
  const [tab, setTab] = useState<'connect' | 'config' | 'tools' | 'trouble'>('connect')
  const origin = location.origin
  const sample = keyPreview || 'relio_{keyId}_{secret}'
  const qwenCommand = `qwen mcp add --scope user --transport http relio ${origin}/mcp \\
  --header "Authorization: Bearer ${sample}"`
  const qwenConfig = `{
  "mcpServers": {
    "relio": {
      "httpUrl": "${origin}/mcp",
      "headers": {
        "Authorization": "Bearer ${sample}"
      }
    }
  }
}`
  const openCodeCommand = `opencode mcp add relio --url ${origin}/mcp \\
  --header "Authorization=Bearer ${sample}"`
  const openCodeConfig = `{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "relio": {
      "type": "remote",
      "url": "${origin}/mcp",
      "enabled": true,
      "oauth": false,
      "headers": {
        "Authorization": "Bearer ${sample}"
      }
    }
  }
}`
  const bridgeConfig = `{
  "mcpServers": {
    "relio": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "${origin}/mcp",
               "--header", "Authorization: Bearer ${sample}"]
    }
  }
}`
  const curl = `curl -sS ${origin}/mcp \\
  -H "Authorization: Bearer ${sample}" \\
  -H "Content-Type: application/json" \\
  -H "Accept: application/json, text/event-stream" \\
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'`

  return <Modal title="MCP 사용 안내" onClose={onClose} wide>
    <div className="form mcp-guide">
      <div className="segmented guide-tabs">
        {([['connect', '연결'], ['config', '클라이언트 설정'], ['tools', '도구와 권한'], ['trouble', '문제 해결']] as const)
          .map(([key, label]) => <button key={key} type="button" className={tab === key ? 'active' : ''} onClick={() => setTab(key)}>{label}</button>)}
      </div>

      {tab === 'connect' && <>
        <h3>엔드포인트</h3>
        <CopyBlock text={`${origin}/mcp`} />
        <p className="muted-copy">Streamable HTTP 방식입니다. 서버가 먼저 보내는 SSE 스트림은 사용하지 않으므로 <code>GET /mcp</code>는 405를 반환합니다. 정상 동작입니다.</p>

        <h3>인증</h3>
        <CopyBlock text={`Authorization: Bearer ${sample}`} />
        <p className="muted-copy">발급한 개인 키를 그대로 사용합니다. 키에 <b>MCP 채널</b>과 <code>mcp:use</code> 권한이 모두 있어야 합니다.</p>

        <h3>직접 확인</h3>
        <CopyBlock text={curl} />

        <h3>지원 프로토콜 버전</h3>
        <p className="muted-copy"><code>2025-11-25</code>, <code>2025-06-18</code>, <code>2025-03-26</code>, <code>2024-11-05</code> — 클라이언트가 요청한 버전을 그대로 사용합니다.</p>
      </>}

      {tab === 'config' && <>
        <h3>Qwen Code · 권장</h3>
        <p className="muted-copy">Qwen은 <code>httpUrl</code>을 사용하는 네이티브 Streamable HTTP 설정이 필요합니다. <code>url</code>은 구형 SSE 설정이므로 사용하지 마세요.</p>
        <CopyBlock text={qwenCommand} />
        <CopyBlock text={qwenConfig} />

        <h3>OpenCode · 권장</h3>
        <p className="muted-copy">OpenCode는 Qwen의 <code>mcpServers</code>가 아니라 최상위 <code>mcp</code>에 <code>type: remote</code>로 설정합니다. CLI Header 구분자는 콜론이 아닌 등호입니다.</p>
        <CopyBlock text={openCodeCommand} />
        <CopyBlock text={openCodeConfig} />

        <h3>Claude Desktop 등 stdio 전용 클라이언트</h3>
        <p className="muted-copy">클라이언트가 원격 HTTP를 직접 지원하지 않을 때만 <code>mcp-remote</code> Bridge를 사용하세요.</p>
        <CopyBlock text={bridgeConfig} />
        <p className="muted-copy">Origin 제한이 켜져 있으면 관리자 화면의 <b>연동 키 · API · MCP</b>에서 클라이언트 Origin을 먼저 허용해야 합니다.</p>
      </>}

      {tab === 'tools' && <>
        <h3>도구는 권한의 교집합입니다</h3>
        <p className="muted-copy">노출되는 도구는 <b>키 Scope</b> ∩ <b>사용자 권한</b> ∩ <b>관리자 Tool 허용목록</b>입니다. 세 가지 중 하나라도 빠지면 도구 목록에 나타나지 않으며, 호출해도 거부됩니다.</p>
        <div className="info-list">
          <div><span>조회 도구</span><b>고객, 담당자, 영업기회, 활동, 계약, Forecast, 고객의 목소리, 위험 분석</b></div>
          <div><span>기록 도구</span><b>고객·담당자·Lead·영업기회·활동·견적·계약·매출·목표 등록 및 업무 상태 수정</b></div>
          <div><span>데이터 범위</span><b>화면과 동일하게 적용됩니다. MCP로 넓어지지 않습니다.</b></div>
        </div>
        <p className="muted-copy">모든 호출은 감사 로그와 MCP 요청 로그에 남습니다.</p>
      </>}

      {tab === 'trouble' && <>
        <div className="info-list guide-trouble">
          <div><span>도구 목록이 비어 있음</span><b>키 Scope에 해당 권한이 없거나 관리자 Tool 허용목록에서 제외된 경우입니다.</b></div>
          <div><span>403 mcp_access_denied</span><b>키에 MCP 채널이 없거나 <code>mcp:use</code> 권한이 빠졌습니다.</b></div>
          <div><span>403 invalid_origin</span><b>관리자 화면에서 클라이언트 Origin을 허용하세요.</b></div>
          <div><span>405 sse_not_supported</span><b>정상입니다. 이 서버는 POST만 사용합니다.</b></div>
          <div><span>failed to parse json · Failed to get tools</span><b>v1.11.4 이상인지 확인하고, Qwen은 <code>httpUrl</code>, OpenCode는 <code>type: remote</code> 설정을 사용하세요.</b></div>
          <div><span>Qwen Pending approval</span><b>Project Scope 서버는 작업공간 승인 후 연결됩니다. 바로 확인하려면 위 명령처럼 User Scope로 추가하세요.</b></div>
          <div><span>도구 호출이 isError로 반환됨</span><b>전송 오류가 아니라 도구가 실행되어 실패한 것입니다. 메시지에 사유가 들어 있습니다.</b></div>
        </div>
      </>}

      <div className="modal-actions"><button type="button" className="btn btn-primary" onClick={onClose}>닫기</button></div>
    </div>
  </Modal>
}

function CopyBlock({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  return <div className="copy-block">
    <pre>{text}</pre>
    <button type="button" className="btn btn-sm btn-secondary" onClick={async () => {
      try { await navigator.clipboard.writeText(text); setCopied(true); setTimeout(() => setCopied(false), 1500) } catch { /* denied */ }
    }}>{copied ? '복사됨' : '복사'}</button>
  </div>
}
