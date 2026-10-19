export type SortValue = string | number | boolean | null | undefined
export type TableSort = { key: string; direction: 'asc' | 'desc' } | null
export type SortColumn<T> = { key: string; value?: (row: T) => SortValue; initialDirection?: 'asc' | 'desc' }

const collator = new Intl.Collator('ko', { numeric: true, sensitivity: 'base' })
const missing = (value: SortValue) => value == null || value === '' || (typeof value === 'number' && !Number.isFinite(value))

/** Empty values stay last in either direction; ties retain the server order. */
export function sortRows<T>(rows: readonly T[], columns: readonly SortColumn<T>[], sort: TableSort): T[] {
  const column = columns.find(c => c.key === sort?.key)
  if (!sort || !column?.value) return [...rows]
  const value = column.value
  return rows.map((row, index) => ({ row, index })).sort((a, b) => {
    const left = value(a.row), right = value(b.row)
    if (missing(left) || missing(right)) return missing(left) === missing(right) ? a.index - b.index : missing(left) ? 1 : -1
    const order = typeof left === 'number' && typeof right === 'number' ? left - right
      : typeof left === 'boolean' && typeof right === 'boolean' ? Number(left) - Number(right)
      : collator.compare(String(left), String(right))
    return (sort.direction === 'desc' ? -order : order) || a.index - b.index
  }).map(item => item.row)
}

export function nextSort<T>(current: TableSort, column: SortColumn<T>): TableSort {
  const first = column.initialDirection || 'asc'
  if (current?.key !== column.key) return { key: column.key, direction: first }
  return current.direction === first ? { key: column.key, direction: first === 'asc' ? 'desc' : 'asc' } : null
}

export function timestamp(value?: string | null): number | null {
  if (!value) return null
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? parsed : null
}
