<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useSessionStore } from '@/stores/session'

const router = useRouter()
const session = useSessionStore()
const submitting = ref(false)
const form = reactive({ username: '', password: '' })

async function submit() {
  submitting.value = true
  try {
    await session.login(form.username, form.password)
    await router.replace(session.mustChangePassword ? '/change-password' : '/assets/responsible-department')
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : '登录失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <el-card class="login-card" shadow="never">
      <template #header>
        <div>
          <h1>资产治理平台</h1>
          <p>阶段二 · IAM 与元数据资产引擎</p>
        </div>
      </template>
      <el-form label-position="top" @submit.prevent="submit">
        <el-form-item label="用户名">
          <el-input v-model="form.username" autocomplete="username" maxlength="50" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input
            v-model="form.password"
            type="password"
            autocomplete="current-password"
            show-password
            maxlength="256"
            @keyup.enter="submit"
          />
        </el-form-item>
        <el-button type="primary" native-type="submit" :loading="submitting" class="full-width">
          登录
        </el-button>
      </el-form>
    </el-card>
  </div>
</template>
