let csrfToken = ''
export function setCSRF(token?: string) { csrfToken = token || '' }

export class APIError extends Error {
  status: number
  code: string
  constructor(status: number, code: string, message: string) { super(message); this.status = status; this.code = code }
}

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  if (options.method && !['GET','HEAD'].includes(options.method.toUpperCase()) && csrfToken) headers.set('X-CSRF-Token', csrfToken)
  const response = await fetch(path, { ...options, headers, credentials: 'same-origin' })
  if (response.status === 204) return undefined as T
  const body = await response.json().catch(() => ({}))
  if (!response.ok) {
    const err = body?.error
    throw new APIError(response.status, err?.code || 'request_failed', err?.message || `요청 실패 (${response.status})`)
  }
  return body as T
}

export const money = (value?: number) => new Intl.NumberFormat('ko-KR', { style: 'currency', currency: 'KRW', maximumFractionDigits: 0 }).format(value || 0)
export const currencyMoney = (value?: number, currency = 'KRW') => new Intl.NumberFormat('ko-KR', { style: 'currency', currency, maximumFractionDigits: currency === 'KRW' ? 0 : 2 }).format(value || 0)
export const number = (value?: number) => new Intl.NumberFormat('ko-KR').format(value || 0)
export const date = (value?: string) => value ? new Intl.DateTimeFormat('ko-KR', { year: 'numeric', month: 'short', day: 'numeric' }).format(new Date(value)) : '—'
export const relative = (value?: string) => {
  if (!value) return '기록 없음'
  const days = Math.floor((Date.now() - new Date(value).getTime()) / 86400000)
  if (days <= 0) return '오늘'
  if (days === 1) return '어제'
  if (days < 30) return `${days}일 전`
  return date(value)
}
