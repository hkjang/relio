import { useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api'
import { User } from '../types'
import { NavItem, adminNav, appNav, meNav, navIcon, quickActions } from '../nav'
import { initials } from '../labels'

// The topbar search box used to be a dead end: in the sales area it ignored what
// you typed and, on Enter, dumped you into the customer list with a query string.
// Meanwhile /api/v1/search already returned customers, contacts and
// opportunities and nothing called it.
//
// This is one place to go anywhere: menus, records and actions, reachable from
// any screen with ⌘K, driven entirely by the keyboard.

type Entry = {
  id: string
  group: string
  label: string
  hint?: string
  icon: string
  to: string
  avatar?: string
}
const RECENT_KEY = 'relio.recentDestinations'
const RECENT_MAX = 5

function readRecent(): Entry[] {
  try {
    const raw = JSON.parse(sessionStorage.getItem(RECENT_KEY) || localStorage.getItem(RECENT_KEY) || '[]')
    return Array.isArray(raw) ? raw.slice(0, RECENT_MAX) : []
  } catch { return [] }
}

function rememberRecent(entry: Entry) {
  const kept = [{ ...entry, group: '최근 이동' }, ...readRecent().filter(x => x.to !== entry.to)].slice(0, RECENT_MAX)
  try { localStorage.setItem(RECENT_KEY, JSON.stringify(kept)) } catch { /* private mode */ }
}

const matches = (haystack: string, needle: string) => {
  const text = haystack.toLowerCase()
  const query = needle.toLowerCase().trim()
  if (!query) return true
  // Every whitespace-separated term must appear, so "고객 위험" finds
  // 고객 위험 분석 without the words having to be adjacent.
  return query.split(/\s+/).every(term => text.includes(term))
}

export default function Palette({ user, approvalEnabled, onClose }: {
  user: User; approvalEnabled: boolean; onClose: () => void
}) {
  const [query, setQuery] = useState('')
  const [cursor, setCursor] = useState(0)
  const [records, setRecords] = useState<Entry[]>([])
  const [searching, setSearching] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)

  const permissions = user.permissions || []
  const can = (permission?: string) =>
    !permission || user.isBootstrap || permissions.some(p => p === 'admin:*' || p === permission)
  const canAdmin = user.isBootstrap || permissions.includes('admin:*') || permissions.includes('admin:read')

  const destinations = useMemo<Entry[]>(() => {
    const toEntry = (area: string) => (item: NavItem): Entry => ({
      id: item.to, group: area, label: item.label, hint: item.group,
      icon: navIcon[item.key] || '◇', to: item.to,
    })
    const approvals: NavItem[] = approvalEnabled
      ? [{ to: '/app/approvals', key: 'approvals', label: '검토 · 승인', keywords: 'approval 승인 반려' }] : []
    return [
      ...appNav.concat(approvals).map(toEntry('영업 업무')),
      ...meNav.map(toEntry('개인')),
      ...(canAdmin ? adminNav.map(toEntry('관리자')) : []),
    ]
  }, [approvalEnabled, canAdmin])

  const actions = useMemo<Entry[]>(() => quickActions.filter(a => can(a.permission)).map(a => ({
    id: a.key, group: '실행', label: a.label, hint: a.hint, icon: '＋', to: a.to,
  })), [user])

  // Records need the server, so they are fetched on a short debounce and only
  // once the query is worth a round trip.
  useEffect(() => {
    const term = query.trim()
    if (term.length < 2) { setRecords([]); setSearching(false); return }
    setSearching(true)
    const timer = setTimeout(() => {
      api<{ customers: any[]; opportunities: any[]; contacts: any[] }>(`/api/v1/search?q=${encodeURIComponent(term)}&limit=5`)
        .then(found => {
          setRecords([
            ...(found.customers || []).map(c => ({
              id: 'c' + c.id, group: '고객', label: c.name, hint: [c.industry, c.ownerName].filter(Boolean).join(' · '),
              icon: '◫', to: '/app/customers/' + c.id, avatar: initials(c.name, 2),
            })),
            ...(found.contacts || []).map(c => ({
              id: 'p' + c.id, group: '담당자', label: c.name, hint: [c.title, c.customerName].filter(Boolean).join(' · '),
              icon: '◍', to: '/app/customers/' + c.customerId, avatar: initials(c.name, 1),
            })),
            ...(found.opportunities || []).map(o => ({
              id: 'o' + o.id, group: '영업기회', label: o.name, hint: [o.customerName, o.stageName].filter(Boolean).join(' · '),
              icon: '◆', to: '/app/opportunities?q=' + encodeURIComponent(o.name),
            })),
          ])
        })
        .catch(() => setRecords([]))
        .finally(() => setSearching(false))
    }, 200)
    return () => clearTimeout(timer)
  }, [query])

  const entries = useMemo<Entry[]>(() => {
    if (!query.trim()) {
      const recent = readRecent()
      return [...recent, ...actions, ...destinations.filter(d => !recent.some(r => r.to === d.to))]
    }
    const hit = (entry: Entry) => matches(`${entry.label} ${entry.hint || ''}`, query)
    const navHit = destinations.filter(d => {
      const source = [...appNav, ...meNav, ...adminNav].find(n => n.to === d.to)
      return matches(`${d.label} ${d.hint || ''} ${source?.keywords || ''}`, query)
    })
    return [...navHit, ...records, ...actions.filter(hit)]
  }, [query, destinations, actions, records])

  useEffect(() => { setCursor(0) }, [query])
  useEffect(() => { input.current?.focus() }, [])
  useEffect(() => {
    listRef.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' })
  }, [cursor, entries.length])

  function choose(entry?: Entry) {
    const target = entry || entries[cursor]
    if (!target) return
    if (target.group !== '실행') rememberRecent(target)
    onClose()
    // Navigation is the caller's job so the palette does not depend on Layout.
    history.pushState({}, '', target.to)
    dispatchEvent(new PopStateEvent('popstate'))
  }

  function onKey(e: React.KeyboardEvent) {
    if (e.key === 'ArrowDown') { e.preventDefault(); setCursor(c => Math.min(c + 1, entries.length - 1)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setCursor(c => Math.max(c - 1, 0)) }
    else if (e.key === 'Enter') { e.preventDefault(); choose() }
    else if (e.key === 'Escape') { e.preventDefault(); onClose() }
    else if (e.key === 'Home') { e.preventDefault(); setCursor(0) }
    else if (e.key === 'End') { e.preventDefault(); setCursor(entries.length - 1) }
  }

  let lastGroup = ''
  return <div className="palette-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
    <div className="palette" role="dialog" aria-modal="true" aria-label="빠른 이동">
      <div className="palette-input">
        <span aria-hidden="true">⌕</span>
        <input ref={input} value={query} onChange={e => setQuery(e.target.value)} onKeyDown={onKey}
          placeholder="어디로 갈까요? 메뉴, 고객, 담당자, 영업기회를 찾습니다"
          aria-label="빠른 이동 검색" aria-controls="palette-list" role="combobox" aria-expanded="true" />
        <kbd>Esc</kbd>
      </div>
      <div className="palette-list" id="palette-list" role="listbox" ref={listRef}>
        {entries.length === 0 && <p className="palette-empty">
          {searching ? '검색 중입니다…' : `"${query}"에 해당하는 메뉴나 레코드가 없습니다.`}
        </p>}
        {entries.map((entry, index) => {
          const header = entry.group !== lastGroup ? entry.group : ''
          lastGroup = entry.group
          return <div key={entry.id + index}>
            {header && <p className="palette-group">{header}</p>}
            <div role="option" aria-selected={index === cursor} className={`palette-row ${index === cursor ? 'active' : ''}`}
              onMouseMove={() => setCursor(index)} onClick={() => choose(entry)}>
              {entry.avatar
                ? <span className="avatar tiny">{entry.avatar}</span>
                : <span className="palette-icon">{entry.icon}</span>}
              <span className="palette-copy"><b>{entry.label}</b>{entry.hint && <small>{entry.hint}</small>}</span>
              {index === cursor && <kbd>↵</kbd>}
            </div>
          </div>
        })}
        {searching && entries.length > 0 && <p className="palette-searching">레코드를 찾는 중…</p>}
      </div>
      <div className="palette-foot">
        <span><kbd>↑</kbd><kbd>↓</kbd> 이동</span>
        <span><kbd>↵</kbd> 열기</span>
        <span><kbd>Esc</kbd> 닫기</span>
        <span className="palette-hint">두 글자 이상 입력하면 고객·담당자·영업기회도 찾습니다</span>
      </div>
    </div>
  </div>
}
