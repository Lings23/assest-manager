import { useSessionStore } from '@/stores/session'
import type { TokenResponse } from '@/types/iam'

export interface APIError {
  error: {
    code: string
    message: string
    request_id: string
  }
}

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const session = useSessionStore()
  const token = session.accessToken
  const response = await send(path, init, token)
  if (response.status === 401 && !path.startsWith('/api/v1/auth/')) {
    if (session.accessToken && session.accessToken !== token) {
      return parseResponse<T>(await send(path, init, session.accessToken))
    }
    const tokens = await refreshSession()
    if (tokens) return parseResponse<T>(await send(path, init, tokens.access_token))
  }
  return parseResponse<T>(response)
}

let refreshing: Promise<TokenResponse | null> | null = null
let refreshFailed = false

export function resetRefreshFailure() { refreshFailed = false }

// Web Locks serializes cookie rotation across tabs. Waiting tabs send the latest
// browser cookie, while same-page callers share one request. Never retry refresh.
export function refreshSession(): Promise<TokenResponse | null> {
  if (refreshing) return refreshing
  if (refreshFailed) return Promise.resolve(null)
  refreshing = (async () => {
    const session = useSessionStore()
    try {
      if (!navigator.locks) throw new Error('浏览器不支持安全会话恢复，请重新登录')
      const tokens = await navigator.locks.request('asset-session-cookie', async () => {
        const response = await fetch('/api/v1/auth/refresh', {
          method: 'POST', credentials: 'include', headers: { Accept: 'application/json' },
        })
        return parseResponse<TokenResponse>(response)
      })
      session.setSession(tokens.access_token, tokens.user)
      return tokens
    } catch {
      refreshFailed = true
      session.clear()
      return null
    }
  })().finally(() => { refreshing = null })
  return refreshing
}

async function send(path: string, init: RequestInit, accessToken: string | null) {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  if (accessToken) headers.set('Authorization', `Bearer ${accessToken}`)
  const perform = () => fetch(path, { ...init, headers, credentials: 'include' })
  if (path.startsWith('/api/v1/auth/') && init.method === 'POST' && navigator.locks) {
    return navigator.locks.request('asset-session-cookie', perform)
  }
  return perform()
}

async function parseResponse<T>(response: Response): Promise<T> {
  if (response.status === 204) return undefined as T
  const payload = (await response.json()) as T | APIError
  if (!response.ok) {
    const apiError = payload as APIError
    throw new Error(apiError.error?.message ?? '请求失败')
  }
  return payload as T
}
