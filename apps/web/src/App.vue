<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useSessionStore } from '@/stores/session'

const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const title = computed(() => String(route.meta.title ?? '资产治理平台'))

onMounted(async () => {
  await session.refresh()
  if (!session.authenticated && route.path !== '/login') await router.replace('/login')
  if (session.authenticated && session.mustChangePassword && route.path !== '/change-password') {
    await router.replace('/change-password')
  } else if (session.authenticated && route.path === '/login') {
    await router.replace('/')
  }
})

async function logout() {
  await session.logout()
  await router.replace('/login')
}
</script>

<template>
  <el-container v-if="session.initialized" class="app-shell">
    <el-header class="app-header">
      <div>
        <strong>资产治理平台</strong>
        <span class="phase-tag">阶段二 · IAM 与元数据资产引擎</span>
      </div>
      <div v-if="session.authenticated" class="session-actions">
        <span>{{ session.principal?.display_name }}</span>
        <el-button size="small" plain @click="logout">退出</el-button>
      </div>
    </el-header>
    <el-main>
      <el-card v-if="route.path !== '/login' && route.path !== '/change-password'" shadow="never">
        <template #header>{{ title }}</template>
        <router-view />
      </el-card>
      <router-view v-else />
    </el-main>
  </el-container>
  <div v-else class="boot-loading">正在恢复安全会话…</div>
</template>
