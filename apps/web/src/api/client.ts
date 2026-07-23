import { useSessionStore } from '@/stores/session'

export interface APIError {
  error: {
    code: string
    message: string
    request_id: string
  }
}

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const session = useSessionStore()
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (session.accessToken) headers.set('Authorization', `Bearer ${session.accessToken}`)
  const response = await fetch(path, { ...init, headers, credentials: 'include' })
  const payload = (await response.json()) as T | APIError
  if (!response.ok) {
    const apiError = payload as APIError
    throw new Error(apiError.error?.message ?? '请求失败')
  }
  return payload as T
}
