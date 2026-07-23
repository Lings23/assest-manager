import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useSessionStore = defineStore('session', () => {
  // Access Token only lives in memory. Refresh Token will be an HttpOnly cookie.
  const accessToken = ref<string | null>(null)
  const setAccessToken = (value: string | null) => {
    accessToken.value = value
  }
  return { accessToken, setAccessToken }
})
