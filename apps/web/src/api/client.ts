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
  const response = await send(path, init, session.accessToken)
  if (response.status === 401 && !path.startsWith('/api/v1/auth/')) {
    const refreshed = await fetch('/api/v1/auth/refresh', {
      method: 'POST',
      credentials: 'include',
      headers: { Accept: 'application/json' },
    })
    if (refreshed.ok) {
      const tokens = (await refreshed.json()) as TokenResponse
      session.setSession(tokens.access_token, tokens.user)
      return parseResponse<T>(await send(path, init, tokens.access_token))
    }
    session.clear()
  }
  return parseResponse<T>(response)
}

async function send(path: string, init: RequestInit, accessToken: string | null) {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  if (accessToken) headers.set('Authorization', `Bearer ${accessToken}`)
  return fetch(path, { ...init, headers, credentials: 'include' })
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
