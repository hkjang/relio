import { Children, cloneElement, Fragment, isValidElement, ReactNode, useId, useState } from 'react'
import { nextSort, sortRows, SortColumn, TableSort } from '../table'

export type TableColumn<T> = SortColumn<T> & { label: string; align?: 'right'; className?: string }

// Flatten conditional cell fragments without adding wrappers inside <tr>.
function cells(children: ReactNode): ReactNode[] {
  return Children.toArray(children).flatMap(child => isValidElement<{ children?: ReactNode }>(child) && child.type === Fragment ? cells(child.props.children) : [child])
}

/** Sorting is explicitly on the loaded rows. Server cursors remain owned by the page. */
export function DataTable<T>({ rows, columns, renderRow, label, loadedLimit, searchValue, emptyMessage = '표시할 항목이 없습니다.' }: {
  rows: readonly T[]; columns: TableColumn<T>[]; renderRow: (row: T) => ReactNode; label: string
  loadedLimit?: number; searchValue?: (row: T) => string; emptyMessage?: string
}) {
  const [sort, setSort] = useState<TableSort>(null)
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(25)
  const [compact, setCompact] = useState(false)
  const [query, setQuery] = useState('')
  const id = useId()
  const filtered = searchValue && query.trim()
    ? rows.filter(row => searchValue(row).toLocaleLowerCase('ko').includes(query.trim().toLocaleLowerCase('ko'))) : rows
  const sorted = sortRows(filtered, columns, sort)
  const pages = Math.max(1, Math.ceil(sorted.length / pageSize))
  const currentPage = Math.min(page, pages - 1)
  const start = currentPage * pageSize
  const visible = sorted.slice(start, start + pageSize)
  const sortedColumn = columns.find(column => column.key === sort?.key)
  const changeSort = (column: TableColumn<T>) => { setSort(nextSort(sort, column)); setPage(0) }
  const render = (row: T, index: number) => {
    const element = renderRow(row)
    if (!isValidElement<{ children?: ReactNode; role?: string }>(element)) return element
    // Keep native row/cell semantics even when the row is keyboard-activatable.
    return cloneElement(element, { key: element.key ?? start + index, role: element.type === 'tr' ? undefined : element.props.role }, cells(element.props.children).map((cell, i) => {
      if (!isValidElement<{ className?: string }>(cell)) return cell
      return cloneElement(cell, { key: i, className: [cell.props.className, columns[i]?.align === 'right' ? 'cell-number' : ''].filter(Boolean).join(' ') })
    }))
  }
  return <div className={`data-table ${compact ? 'data-table-compact' : ''}`}>
    <div className="data-table-toolbar">
      {searchValue && <label className="data-table-search"><span aria-hidden="true">⌕</span><input type="search" aria-label={`${label}에서 검색`} placeholder="불러온 목록에서 검색" value={query} onChange={e => { setQuery(e.target.value); setPage(0) }} /></label>}
      <span className="data-table-order" aria-live="polite">{sortedColumn && sort ? `${sortedColumn.label} · ${sort.direction === 'asc' ? '오름차순' : '내림차순'}` : '열 제목을 눌러 정렬'}</span>
      {sort && <button type="button" className="table-reset" onClick={() => { setSort(null); setPage(0) }}>기본 순서</button>}
      <button type="button" className="table-density" aria-pressed={compact} onClick={() => setCompact(v => !v)} title="행 간격 줄이기">촘촘히 보기</button>
    </div>
    <div className="data-table-scroll" role="region" aria-label={`${label} 표`} tabIndex={0} aria-describedby={`${id}-summary`}>
      <table>
        <caption className="sr-only">{label}</caption>
        <thead><tr>{columns.map(column => <th key={column.key} scope="col" className={[column.className, column.align === 'right' ? 'cell-number' : ''].filter(Boolean).join(' ')} aria-sort={column.value ? sort?.key === column.key ? sort.direction === 'asc' ? 'ascending' : 'descending' : 'none' : undefined}>
          {column.value ? <button type="button" className="table-sort" onClick={() => changeSort(column)} aria-label={`${column.label} 정렬 변경`}>
            <span>{column.label}</span><span aria-hidden="true" className={`table-sort-icon ${sort?.key === column.key ? 'is-active' : ''}`}>{sort?.key === column.key ? sort.direction === 'asc' ? '↑' : '↓' : '↕'}</span>
          </button> : column.label || <span className="sr-only">작업</span>}
        </th>)}</tr></thead>
        <tbody>{visible.length ? visible.map(render) : <tr><td colSpan={columns.length}><div className="data-table-empty"><b>{query ? '검색 결과가 없습니다' : emptyMessage}</b>{query && <button type="button" className="btn btn-secondary btn-sm" onClick={() => { setQuery(''); setPage(0) }}>검색 초기화</button>}</div></td></tr>}</tbody>
      </table>
    </div>
    <div className="data-table-footer">
      <div id={`${id}-summary`} className="data-table-summary" role="status">
        <span>{sorted.length ? `${start + 1}–${Math.min(start + pageSize, sorted.length)}` : '0'} / {sorted.length.toLocaleString()}건{query && ` · 불러온 ${rows.length.toLocaleString()}건`}</span>
        {loadedLimit && rows.length >= loadedLimit && <small>불러온 {rows.length.toLocaleString()}건 안에서 정렬합니다. 검색 조건으로 범위를 좁힐 수 있습니다.</small>}
      </div>
      <label className="table-page-size">표시<select aria-label={`${label} 페이지당 표시 건수`} value={pageSize} onChange={e => { setPageSize(Number(e.target.value)); setPage(0) }}>{[25, 50, 100].map(size => <option key={size} value={size}>{size}건</option>)}</select></label>
      <nav className="table-pagination" aria-label={`${label} 페이지 이동`}>
        <button type="button" aria-label="이전 페이지" disabled={currentPage === 0} onClick={() => setPage(currentPage - 1)}>‹</button>
        <span>{currentPage + 1} / {pages}</span>
        <button type="button" aria-label="다음 페이지" disabled={currentPage + 1 >= pages} onClick={() => setPage(currentPage + 1)}>›</button>
      </nav>
    </div>
  </div>
}
