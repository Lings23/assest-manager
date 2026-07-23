import { createRouter, createWebHistory } from 'vue-router'
import BaselineView from '@/views/BaselineView.vue'

export default createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      component: BaselineView,
      meta: { title: '新平台工程基线' },
    },
  ],
})
