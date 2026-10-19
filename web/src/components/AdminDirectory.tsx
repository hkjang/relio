import { useState } from 'react'
import { adminNav, matchesNav, navIcon } from '../nav'
import { Empty, Link } from './Layout'

export default function AdminDirectory() {
  const [query, setQuery] = useState(''), [group, setGroup] = useState('')
  const groups = [...new Set(adminNav.map(item => item.group!))]
  const items = adminNav.filter(item => (!group || item.group === group) && matchesNav(item, query))
  return <section className="panel admin-directory" aria-labelledby="admin-directory-title">
    <div className="panel-head"><div><p className="eyebrow">설정 찾아가기</p><h2 id="admin-directory-title">어떤 작업을 하시겠어요?</h2><p>업무별 메뉴에서 설정과 관리 도구를 찾으세요.</p></div><span className="count-badge" role="status">{items.length} / {adminNav.length}</span></div>
    <div className="admin-directory-controls"><label className="admin-directory-search"><span aria-hidden="true">⌕</span><input type="search" value={query} onChange={e => setQuery(e.target.value)} placeholder="예: SSO, 회원사, 권한, 메일" aria-label="전체 관리 메뉴 검색"/></label><div className="admin-group-filters" aria-label="관리 메뉴 분류">{['', ...groups].map(value => <button key={value} type="button" aria-pressed={group === value} onClick={() => setGroup(value)}>{value || '전체'}</button>)}</div></div>
    {items.length ? <div className="admin-menu-grid">{items.map(item => <Link key={item.to} to={item.to} className="admin-menu-card"><span className="admin-menu-icon" aria-hidden="true">{navIcon[item.key]}</span><span><small>{item.group}</small><b>{item.label}</b><p>{item.description}</p></span><span aria-hidden="true">↗</span></Link>)}</div> : <Empty icon="⌕" title="일치하는 관리 메뉴가 없습니다" description="다른 검색어 또는 분류로 다시 찾아보세요." action={<button className="btn btn-secondary" onClick={() => { setQuery(''); setGroup('') }}>검색과 분류 초기화</button>}/>}
  </section>
}
