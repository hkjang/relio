import { FormEvent, useEffect, useState } from 'react'
import { api } from '../api'
import { Confirm, Empty, Modal, Spinner, Status, rowProps } from './Layout'
import { contactRoleLabel, initials, label } from '../labels'

// Contacts drive the relationship map, the decision-maker health signal and VOC
// intake, but there was no screen to create one. This is that screen, plus the
// inline creation used wherever a contact has to be picked.

export type Contact = {
  id: string; customerId: string; name: string; title?: string; department?: string
  email?: string; phone?: string; mobile?: string
  decisionMaker: boolean; primaryContact: boolean
  relationshipRole: string; influence: string; sentiment: string
  relationshipStrength: number; decisionPower: number; lastContactAt?: string
}
type Notify = (m: string, e?: boolean) => void

export const contactRoles = ['DECISION_MAKER', 'CHAMPION', 'INFLUENCER', 'USER', 'PROCUREMENT']
export const influences = ['HIGH', 'MEDIUM', 'LOW']
export const sentiments = ['SUPPORT', 'NEUTRAL', 'OPPOSE']

export function useContacts(customerId: string, notify: Notify) {
  const [items, setItems] = useState<Contact[] | null>(null)
  const load = () => api<{ items: Contact[] }>(`/api/v1/contacts?customerId=${customerId}&limit=200`)
    .then(v => setItems(v.items)).catch(e => { notify(e instanceof Error ? e.message : '담당자를 불러오지 못했습니다.', true); setItems([]) })
  useEffect(() => { if (customerId) void load(); else setItems(null) }, [customerId])
  return { items, reload: load }
}

/** ContactsPanel is the customer's people: who decides, who supports, who blocks. */
export function ContactsPanel({ customerId, canWrite, contacts, onChanged, notify }: {
  customerId: string; canWrite: boolean
  contacts: { items: Contact[] | null; reload: () => Promise<void> }
  onChanged: () => void; notify: Notify
}) {
  const [modal, setModal] = useState<null | { contact?: Contact }>(null)
  const [remove, setRemove] = useState<Contact | null>(null)
  const [busy, setBusy] = useState(false)
  const items = contacts.items

  async function confirmRemove() {
    if (!remove) return
    setBusy(true)
    try {
      const result = await api<{ removedRelationships?: number }>(`/api/v1/contacts/${remove.id}`, { method: 'DELETE' })
      notify(result?.removedRelationships
        ? `${remove.name} 담당자와 연결된 관계 ${result.removedRelationships}건을 함께 삭제했습니다.`
        : `${remove.name} 담당자를 삭제했습니다.`)
      setRemove(null); await contacts.reload(); onChanged()
    } catch (e) { notify(e instanceof Error ? e.message : '삭제하지 못했습니다.', true) } finally { setBusy(false) }
  }

  const decisionMakers = (items || []).filter(c => c.decisionMaker).length
  const champions = (items || []).filter(c => c.relationshipRole === 'CHAMPION').length

  return <section className="panel contacts-panel">
    <div className="panel-head">
      <div><h2>고객 담당자</h2>
        <p>{items?.length
          ? `${items.length}명 · 의사결정자 ${decisionMakers}명 · 지지자 ${champions}명`
          : '의사결정 구조를 만들려면 담당자를 먼저 등록하세요'}</p></div>
      {canWrite && <button className="btn btn-sm btn-primary" onClick={() => setModal({})}>＋ 담당자 등록</button>}
    </div>
    {!items ? <Spinner /> : items.length ? <div className="contact-table">
      {items.map(c => <div key={c.id} {...(canWrite ? rowProps(() => setModal({ contact: c })) : {})}
        className="contact-row" aria-label={canWrite ? `${c.name} 담당자 편집` : undefined}>
        <span className="avatar">{initials(c.name, 1)}</span>
        <span className="contact-identity">
          <b>{c.name}{c.primaryContact && <em className="default-tag">주 담당</em>}</b>
          <small>{[c.department, c.title].filter(Boolean).join(' · ') || '직책 미입력'}</small>
        </span>
        <span className="contact-role">
          <Status value={c.relationshipRole} text={contactRoleLabel(c.relationshipRole)} />
          {c.decisionMaker && c.relationshipRole !== 'DECISION_MAKER' && <Status value="DECISION_MAKER" />}
        </span>
        <span className="contact-signal">
          <small>영향력 {label(c.influence)}</small>
          <small className={c.sentiment === 'OPPOSE' ? 'danger-text' : c.sentiment === 'SUPPORT' ? 'positive' : ''}>{label(c.sentiment)}</small>
        </span>
        <span className="contact-reach">
          <small>{c.email || '이메일 없음'}</small>
          <small>{c.mobile || c.phone || '연락처 없음'}</small>
        </span>
        {canWrite && <span className="row-menu">
          <button onClick={e => { e.stopPropagation(); setModal({ contact: c }) }}>편집</button>
          <button className="danger" onClick={e => { e.stopPropagation(); setRemove(c) }}>삭제</button>
        </span>}
      </div>)}
    </div> : <Empty icon="◍" title="등록된 담당자가 없습니다"
      description="담당자를 등록하면 의사결정 관계도를 그리고, 고객 요청을 접수할 때 요청자를 지정할 수 있습니다."
      action={canWrite ? <button className="btn btn-primary" onClick={() => setModal({})}>첫 담당자 등록</button> : undefined} />}

    {modal && <ContactModal customerId={customerId} contact={modal.contact} onClose={() => setModal(null)}
      onSaved={async () => { setModal(null); await contacts.reload(); onChanged() }} notify={notify} />}
    {remove && <Confirm title="담당자 삭제"
      description={`${remove.name} 담당자를 삭제합니다. 이 담당자가 포함된 관계 연결도 함께 제거됩니다. 접수된 고객 요청이 있으면 삭제가 거부됩니다.`}
      requireText={remove.name} busy={busy} onCancel={() => setRemove(null)} onConfirm={confirmRemove} />}
  </section>
}

export function ContactModal({ customerId, contact, onClose, onSaved, notify }: {
  customerId: string; contact?: Contact; onClose: () => void; onSaved: (created?: Contact) => void; notify: Notify
}) {
  const editing = Boolean(contact?.id)
  const [busy, setBusy] = useState(false)
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const f = new FormData(e.currentTarget); setBusy(true)
    const body = {
      customerId, name: f.get('name'), title: f.get('title'), department: f.get('department'),
      email: f.get('email'), phone: f.get('phone'), mobile: f.get('mobile'),
      decisionMaker: f.get('decisionMaker') === 'on', primaryContact: f.get('primaryContact') === 'on',
      relationshipRole: f.get('relationshipRole'), influence: f.get('influence'), sentiment: f.get('sentiment'),
      relationshipStrength: Number(f.get('relationshipStrength')), decisionPower: Number(f.get('decisionPower')),
    }
    try {
      const saved = await api<Contact>(editing ? `/api/v1/contacts/${contact!.id}` : '/api/v1/contacts',
        { method: editing ? 'PUT' : 'POST', body: JSON.stringify(body) })
      notify(editing ? `${body.name} 담당자를 저장했습니다.` : `${body.name} 담당자를 등록했습니다.`)
      onSaved(saved)
    } catch (err) { notify(err instanceof Error ? err.message : '저장하지 못했습니다.', true) } finally { setBusy(false) }
  }
  return <Modal title={editing ? `${contact!.name} 담당자 편집` : '담당자 등록'} onClose={onClose} wide>
    <form className="form" onSubmit={submit}>
      <div className="form-grid">
        <label>이름 *<input name="name" required autoFocus defaultValue={contact?.name || ''} placeholder="김도현" /></label>
        <label>직책<input name="title" defaultValue={contact?.title || ''} placeholder="구매본부장" /></label>
        <label>부서<input name="department" defaultValue={contact?.department || ''} placeholder="구매본부" /></label>
        <label>이메일<input name="email" type="email" defaultValue={contact?.email || ''} /></label>
        <label>휴대전화<input name="mobile" defaultValue={contact?.mobile || ''} placeholder="010-0000-0000" /></label>
        <label>유선전화<input name="phone" defaultValue={contact?.phone || ''} /></label>
        <label>관계 역할<select name="relationshipRole" defaultValue={contact?.relationshipRole || 'USER'}>
          {contactRoles.map(x => <option key={x} value={x}>{contactRoleLabel(x)}</option>)}</select>
          <small>지지자는 우리를 대신해 내부를 설득하는 사람입니다.</small></label>
        <label>영향력<select name="influence" defaultValue={contact?.influence || 'MEDIUM'}>
          {influences.map(x => <option key={x} value={x}>{label(x)}</option>)}</select></label>
        <label>우리에 대한 성향<select name="sentiment" defaultValue={contact?.sentiment || 'NEUTRAL'}>
          {sentiments.map(x => <option key={x} value={x}>{label(x)}</option>)}</select></label>
        <label>관계 강도 (0~100)<input name="relationshipStrength" type="number" min="0" max="100" defaultValue={contact?.relationshipStrength ?? 50} /></label>
        <label>결정력 (0~100)<input name="decisionPower" type="number" min="0" max="100" defaultValue={contact?.decisionPower ?? 50} /></label>
        <label className="check-label"><input type="checkbox" name="decisionMaker" defaultChecked={contact?.decisionMaker} /> 의사결정 권한 보유</label>
        <label className="check-label"><input type="checkbox" name="primaryContact" defaultChecked={contact?.primaryContact} /> 주 담당자</label>
      </div>
      <div className="modal-actions"><button type="button" className="btn btn-ghost" onClick={onClose}>취소</button>
        <button className="btn btn-primary" disabled={busy}>{busy ? '저장 중…' : editing ? '변경 저장' : '담당자 등록'}</button></div>
    </form>
  </Modal>
}

/** ContactSelect picks a contact and can create one without leaving the form,
 *  which is what makes the relationship map and VOC intake usable on a brand new
 *  customer that has nobody registered yet. */
export function ContactSelect({ customerId, contacts, name, value, onChange, required, label: fieldLabel, placeholder, notify, disabled, disabledHint }: {
  customerId: string
  contacts: { items: Contact[] | null; reload: () => Promise<void> }
  name: string
  value?: string
  onChange?: (id: string) => void
  required?: boolean
  label: string
  placeholder?: string
  notify: Notify
  disabled?: boolean
  disabledHint?: string
}) {
  const [creating, setCreating] = useState(false)
  const items = contacts.items || []
  const empty = !disabled && items.length === 0
  return <label className="contact-select">
    {fieldLabel}{required ? ' *' : ''}
    <span className="contact-select-row">
      <select name={name} required={required} value={value} disabled={disabled || empty}
        onChange={e => onChange?.(e.target.value)}>
        <option value="">{empty ? '등록된 담당자가 없습니다' : placeholder || '담당자 선택'}</option>
        {items.map(c => <option key={c.id} value={c.id}>
          {c.name}{c.title ? ` · ${c.title}` : ''}{c.decisionMaker ? ' (의사결정자)' : ''}
        </option>)}
      </select>
      {!disabled && customerId && <button type="button" className="btn btn-sm btn-secondary" onClick={() => setCreating(true)}>＋ 새 담당자</button>}
    </span>
    {disabled ? disabledHint && <small>{disabledHint}</small>
      : empty ? <small>등록된 담당자가 없습니다. 오른쪽 <b>＋ 새 담당자</b>로 이 화면에서 바로 추가할 수 있습니다.</small>
      : <small>{items.length}명 등록됨</small>}
    {creating && <ContactModal customerId={customerId} onClose={() => setCreating(false)}
      onSaved={async created => { setCreating(false); await contacts.reload(); if (created) onChange?.(created.id) }} notify={notify} />}
  </label>
}
