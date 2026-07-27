import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { request } from '@/api/client'
import type { Principal, TokenResponse } from '@/types/iam'

export const useSessionStore = defineStore('session', () => {
  // Access Token and principal only live in memory. The Refresh Token is an
  // HttpOnly Cookie and is never exposed to JavaScript.
  const accessToken = ref<string | null>(null)
  const principal = ref<Principal | null>(null)
  const initialized = ref(false)
  const authenticated = computed(() => accessToken.value !== null && principal.value !== null)
  const isAdmin = computed(() => principal.value?.roles.includes('admin') ?? false)
  const mustChangePassword = computed(() => principal.value?.must_change_password ?? false)

  const setSession = (token: string, user: Principal) => {
    accessToken.value = token
    principal.value = user
  }
  const clear = () => {
    accessToken.value = null
    principal.value = null
  }
  const login = async (username: string, password: string) => {
    const response = await request<TokenResponse>('/api/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    })
    setSession(response.access_token, response.user)
  }
  const changePassword = async (currentPassword: string, newPassword: string) => {
    await request<void>('/api/v1/auth/change-password', {
      method: 'POST',
      body: JSON.stringify({
        current_password: currentPassword,
        new_password: newPassword,
      }),
    })
    clear()
  }
  const refresh = async () => {
    try {
      const response = await request<TokenResponse>('/api/v1/auth/refresh', { method: 'POST' })
      setSession(response.access_token, response.user)
    } catch {
      clear()
    } finally {
      initialized.value = true
    }
  }
  const logout = async () => {
    try {
      await request<void>('/api/v1/auth/logout', { method: 'POST' })
    } finally {
      clear()
    }
  }
  return {
    accessToken, principal, initialized, authenticated, isAdmin, mustChangePassword,
    setSession, clear, login, changePassword, refresh, logout,
  }
})
