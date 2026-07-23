<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

const route = useRoute()
const title = computed(() => String(route.meta.title ?? '工程基线'))
</script>

<template>
  <el-container class="app-shell">
    <el-header class="app-header">
      <div>
        <strong>资产治理平台</strong>
        <span class="phase-tag">阶段一工程基线</span>
      </div>
    </el-header>
    <el-main>
      <el-card shadow="never">
        <template #header>{{ title }}</template>
        <router-view />
      </el-card>
    </el-main>
  </el-container>
</template>
