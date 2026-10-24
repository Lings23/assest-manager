import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { refreshSession, request, resetRefreshFailure } from './client'

const { session } = vi.hoisted(() => ({ session: {
  accessToken: 'expired' as string | null,
  setSession: vi.fn(), clear: vi.fn(),
} }))
vi.mock('@/stores/session', () => ({ useSessionStore: () => session }))

beforeEach(() => {
  resetRefreshFailure()
  session.accessToken = 'expired'
  session.setSession.mockImplementation((token: string) => { session.accessToken = token })
  session.clear.mockImplementation(() => { session.accessToken = null })
  vi.stubGlobal('navigator', { locks: { request: vi.fn((_name, operation) => operation()) } })
})
afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks() })

describe('session refresh coordination', () => {
  it('shares one refresh between parallel 401s and startup session restoration', async () => {
    let release!: (response: Response) => void
    const waiting = new Promise<Response>((resolve) => { release = resolve })
    const fetchMock = vi.fn((path: string, init: RequestInit) => {
      if (path === '/api/v1/auth/refresh') return waiting
      const current = new Headers(init.headers).get('Authorization') === 'Bearer current'
      return Promise.resolve(Response.json(current ? { ok: true } : { error: { message: 'expired' } }, { status: current ? 200 : 401 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    const first = request('/api/v1/assets/a')
    const second = request('/api/v1/assets/b')
    const restore = refreshSession()
    await Promise.resolve()
    release(Response.json({ access_token: 'current', user: {} }))
    expect(await first).toEqual({ ok: true })
    expect(await second).toEqual({ ok: true })
    await restore
    expect(fetchMock.mock.calls.filter(([path]) => path === '/api/v1/auth/refresh')).toHaveLength(1)
    expect(navigator.locks.request).toHaveBeenCalledWith('asset-session-cookie', expect.any(Function))
  })

  it('clears the session after a lost refresh response without replaying it', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new Error('response lost'))
    vi.stubGlobal('fetch', fetchMock)
    expect(await refreshSession()).toBeNull()
    expect(await refreshSession()).toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(session.accessToken).toBeNull()
  })

  it('requires reauthentication when cross-tab locking is unavailable', async () => {
    vi.stubGlobal('navigator', {})
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    expect(await refreshSession()).toBeNull()
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
