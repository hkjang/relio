import { useEffect, useState } from 'react'
import { api } from '../api'

export type SavedView = { id: string; resource: string; name: string; query: string; pinned: boolean; displayOrder: number }

/** SavedViews keeps the filter combinations a user actually reuses. The query is
 *  stored verbatim and replayed through the same list endpoint, so a saved view
 *  can never widen what its owner is allowed to see. */
export function SavedViews({ resource, current, onApply, notify }: {
  resource: string
  /** The querystring describing what is on screen right now, without the "?". */
  current: string
  onApply: (query: string) => void
  notify: (m: string, e?: boolean) => void
}) {
  const [items, setItems] = useState<SavedView[]>([])
  const [naming, setNaming] = useState(false)
  const [name, setName] = useState('')
  const load = () => api<{ items: SavedView[] }>(`/api/v1/me/views?resource=${resource}`)
    .then(v => setItems(v.items)).catch(() => setItems([]))
  useEffect(() => { void load() }, [resource])

  async function save() {
    const trimmed = name.trim()
    if (!trimmed) { notify('이름을 입력하세요.', true); return }
    try {
      await api('/api/v1/me/views', { method: 'POST', body: JSON.stringify({ resource, name: trimmed, query: current, pinned: true }) })
      notify(`'${trimmed}' 조건을 저장했습니다.`); setName(''); setNaming(false); await load()
    } catch (e) { notify(e instanceof Error ? e.message : '저장에 실패했습니다.', true) }
  }
  async function remove(view: SavedView) {
    try { await api(`/api/v1/me/views/${view.id}`, { method: 'DELETE' }); notify(`'${view.name}'을 삭제했습니다.`); await load() }
    catch (e) { notify(e instanceof Error ? e.message : '삭제에 실패했습니다.', true) }
  }

  return <div className="saved-views">
    <span className="saved-views-label">저장된 조건</span>
    {items.length ? items.map(v => <span className="saved-view-chip" key={v.id}>
      <button onClick={() => onApply(v.query)} title={v.query || '조건 없음'}>{v.name}</button>
      <button className="saved-view-remove" aria-label={`${v.name} 삭제`} onClick={() => remove(v)}>×</button>
    </span>) : <span className="saved-views-empty">아직 없습니다</span>}
    {naming
      ? <span className="saved-view-new">
          <input autoFocus value={name} maxLength={40} onChange={e => setName(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); void save() } if (e.key === 'Escape') setNaming(false) }}
            placeholder="예: 내 A등급 고객" aria-label="저장할 조건 이름" />
          <button className="btn btn-sm btn-primary" onClick={save}>저장</button>
          <button className="btn btn-sm btn-ghost" onClick={() => setNaming(false)}>취소</button>
        </span>
      : <button className="saved-view-add" onClick={() => setNaming(true)}>＋ 현재 조건 저장</button>}
  </div>
}

/** FavoriteStar toggles a per-user bookmark on any record. */
export function FavoriteStar({ resource, resourceId, favorited, onChange, notify }: {
  resource: string; resourceId: string; favorited: boolean
  onChange: (next: boolean) => void
  notify: (m: string, e?: boolean) => void
}) {
  const [busy, setBusy] = useState(false)
  async function toggle(e: React.MouseEvent) {
    // The star usually sits inside a clickable row.
    e.stopPropagation()
    setBusy(true)
    try {
      const r = await api<{ favorited: boolean }>('/api/v1/me/favorites', { method: 'POST', body: JSON.stringify({ resource, resourceId }) })
      onChange(r.favorited)
    } catch (err) { notify(err instanceof Error ? err.message : '즐겨찾기를 변경하지 못했습니다.', true) }
    finally { setBusy(false) }
  }
  return <button className={`favorite-star ${favorited ? 'on' : ''}`} disabled={busy} onClick={toggle}
    aria-pressed={favorited} aria-label={favorited ? '즐겨찾기 해제' : '즐겨찾기 추가'}>{favorited ? '★' : '☆'}</button>
}

/** useFavorites loads the starred ids for one resource so a list can render the
 *  star state without a request per row. */
export function useFavorites(resource: string) {
  const [ids, setIds] = useState<Set<string>>(new Set())
  useEffect(() => {
    api<{ ids: string[] }>(`/api/v1/me/favorites?resource=${resource}`)
      .then(v => setIds(new Set(v.ids))).catch(() => {})
  }, [resource])
  const set = (id: string, on: boolean) => setIds(prev => {
    const next = new Set(prev)
    if (on) next.add(id); else next.delete(id)
    return next
  })
  return { ids, set }
}
