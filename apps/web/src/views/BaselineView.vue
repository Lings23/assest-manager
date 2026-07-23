<script setup lang="ts">
const capabilities = [
  'Vue 3 + TypeScript + Vite',
  'Pinia 内存会话状态',
  'Vue Router 路由基线',
  'Element Plus 组件体系',
  '统一 /api/v1 契约入口',
]
</script>

<template>
  <el-alert
    title="工程骨架已就绪，业务页面将在后续阶段由资产 Schema 生成。"
    type="success"
    :closable="false"
    show-icon
  />
  <el-descriptions class="capabilities" :column="1" border>
    <el-descriptions-item v-for="item in capabilities" :key="item" label="能力">
      {{ item }}
    </el-descriptions-item>
  </el-descriptions>
</template>
