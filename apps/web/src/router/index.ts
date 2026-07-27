import { createRouter, createWebHistory } from 'vue-router'
import AssetsView from '@/views/AssetsView.vue'
import LoginView from '@/views/LoginView.vue'
import ChangePasswordView from '@/views/ChangePasswordView.vue'

export default createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      redirect: '/assets/responsible-department',
    },
    {
      path: '/login',
      component: LoginView,
      meta: { title: '登录', public: true },
    },
    {
      path: '/change-password',
      component: ChangePasswordView,
      meta: { title: '修改初始密码' },
    },
    {
      path: '/assets/:type',
      component: AssetsView,
      meta: { title: '资产管理' },
    },
  ],
})
