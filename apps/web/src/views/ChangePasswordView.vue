<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useSessionStore } from '@/stores/session'

const router = useRouter()
const session = useSessionStore()
const submitting = ref(false)
const form = reactive({
  currentPassword: '',
  newPassword: '',
  confirmPassword: '',
})

async function submit() {
  if (form.newPassword.length < 12) {
    ElMessage.warning('新密码至少需要 12 个字符')
    return
  }
  if (form.newPassword !== form.confirmPassword) {
    ElMessage.warning('两次输入的新密码不一致')
    return
  }
  submitting.value = true
  try {
    await session.changePassword(form.currentPassword, form.newPassword)
    ElMessage.success('密码已修改，请重新登录')
    await router.replace('/login')
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : '密码修改失败')
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
          <h1>修改初始密码</h1>
          <p>首次登录必须设置个人密码，修改后所有已有会话将失效。</p>
        </div>
      </template>
      <el-form label-position="top" @submit.prevent="submit">
        <el-form-item label="当前密码">
          <el-input
            v-model="form.currentPassword"
            type="password"
            autocomplete="current-password"
            show-password
            maxlength="256"
          />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input
            v-model="form.newPassword"
            type="password"
            autocomplete="new-password"
            show-password
            minlength="12"
            maxlength="256"
          />
        </el-form-item>
        <el-form-item label="确认新密码">
          <el-input
            v-model="form.confirmPassword"
            type="password"
            autocomplete="new-password"
            show-password
            maxlength="256"
            @keyup.enter="submit"
          />
        </el-form-item>
        <el-button type="primary" native-type="submit" :loading="submitting" class="full-width">
          修改并重新登录
        </el-button>
      </el-form>
    </el-card>
  </div>
</template>
