import { FormEvent, ReactNode, useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../api'
import { persistSettings, Setting, SettingChange } from '../adminSettings'
import { User } from '../types'
import { Empty, Spinner } from './Layout'

export function canManageSettings(user: User) {
  return Boolean(user.isBootstrap || (user.permissions || []).some(p => p === 'admin:*' || p === 'admin:write'))
}
const message = (e: unknown) => e instanceof Error ? e.message : '요청을 완료하지 못했습니다.'

export function useSettings(namespace?: string) {
  const [items, setItems] = useState<Setting[] | null>(null)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const load = useCallback(async () => {
    setError('')
    try {
      const result = await api<{ items: Setting[] }>(`/api/v1/admin/settings${namespace ? '?namespace=' + namespace : ''}`)
      setItems(result.items); setRevision(v => v + 1)
    } catch (e) { setError(message(e)); throw e }
  }, [namespace])
  useEffect(() => { void load().catch(() => {}) }, [load])
  const save = async (changes: SettingChange[]) => {
    if (!items || error) throw new Error('설정을 다시 불러온 뒤 저장하세요.')
    await persistSettings(items, changes, change => api(`/api/v1/admin/settings/${change.namespace}/${change.key}`, { method: 'PUT', body: JSON.stringify(change) }))
    try { await load() } catch { throw new Error('저장은 완료했지만 저장 결과를 불러오지 못했습니다. 다시 불러와 확인하세요.') }
  }
  return { items, error, load, save, revision }
}

export function AdminLoadState({ error, retry }: { error?: string; retry: () => Promise<unknown> }) {
  return error ? <section className="panel admin-load-error" role="alert"><Empty icon="!" title="설정을 불러오지 못했습니다" description={`${error} 안전을 위해 기본값으로 대체하지 않습니다.`} action={<button className="btn btn-secondary" onClick={() => void retry().catch(() => {})}>다시 불러오기</button>} /></section> : <Spinner />
}

type FormProps = { children: ReactNode; user: User; onSave: (data: FormData) => Promise<void>; onReload: () => Promise<unknown>; submitLabel?: string; onDirtyChange?: (dirty: boolean) => void; actions?: ReactNode }
export function SettingsForm({ children, user, onSave, onReload, submitLabel = '변경 사항 저장', onDirtyChange, actions }: FormProps) {
  const form = useRef<HTMLFormElement>(null)
  const initial = useRef('')
  const lock = useRef(false)
  const [dirty, setDirty] = useState(false), [busy, setBusy] = useState(false), [failure, setFailure] = useState('')
  const writable = canManageSettings(user)
  const snapshot = () => JSON.stringify(Array.from(new FormData(form.current!).entries()))
  useEffect(() => { initial.current = snapshot() }, [])
  useEffect(() => { onDirtyChange?.(dirty); return () => onDirtyChange?.(false) }, [dirty, onDirtyChange])
  useEffect(() => {
    if (!dirty && !busy) return
    const unload = (e: BeforeUnloadEvent) => { e.preventDefault(); e.returnValue = '' }
    const navigate = (e: Event) => { if (busy || !window.confirm('저장하지 않은 변경 사항이 있습니다. 이 화면을 나갈까요?')) e.preventDefault() }
    window.addEventListener('beforeunload', unload); window.addEventListener('relio:navigate', navigate)
    return () => { window.removeEventListener('beforeunload', unload); window.removeEventListener('relio:navigate', navigate) }
  }, [dirty, busy])
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (lock.current || !dirty || !writable || failure) return
    const data = new FormData(e.currentTarget)
    lock.current = true; setBusy(true)
    try { await onSave(data); setDirty(false); if (form.current) initial.current = snapshot() }
    catch (e) { setFailure(message(e)) }
    finally { lock.current = false; setBusy(false) }
  }
  async function reload() {
    if (lock.current || (dirty && !window.confirm('입력한 변경 사항을 버리고 서버의 최신 설정을 불러올까요?'))) return
    lock.current = true; setBusy(true)
    try { await onReload(); setFailure(''); setDirty(false) }
    catch (e) { setFailure(message(e)) }
    finally { lock.current = false; setBusy(false) }
  }
  return <form ref={form} className="settings-stack admin-settings-form" onSubmit={submit} onChange={() => setDirty(snapshot() !== initial.current)} aria-busy={busy}>
    <fieldset className="settings-fields" disabled={busy || !writable}>{children}</fieldset>
    {failure && <div className="alert alert-error settings-failure" role="alert"><b>저장 결과를 확인하세요</b><span>{failure}</span><span>자동으로 덮어쓰지 않습니다. 아래 ‘저장된 값 다시 불러오기’로 확인 후 다시 편집하세요.</span></div>}
    <div className={`sticky-save ${dirty ? 'dirty' : ''}`}>
      <span role="status">{!writable ? '조회 전용 · 변경하려면 관리자 쓰기 권한이 필요합니다.' : busy ? '설정을 처리하는 중입니다…' : dirty ? '저장하지 않은 변경 사항이 있습니다.' : '저장된 설정입니다. 변경하면 저장 버튼이 활성화됩니다.'}</span>
      <div className="settings-save-actions">{actions}<button type="button" className="btn btn-ghost" disabled={busy} onClick={() => void reload()}>{failure ? '저장된 값 다시 불러오기' : '다시 불러오기'}</button><button className="btn btn-primary" disabled={!writable || !dirty || busy || Boolean(failure)}>{busy ? '처리 중…' : submitLabel}</button></div>
    </div>
  </form>
}
