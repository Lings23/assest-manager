/**
 * 认证模块
 * 处理登录、登出、token管理、用户状态
 */

import { apiRequest } from './api.js';

// 当前用户信息
export let currentUser = null;

/**
 * 登录
 * @param {string} username - 用户名
 * @param {string} password - 密码
 * @returns {Promise<object>} - 登录结果
 */
export async function login(username, password) {
    const data = await apiRequest('/auth/login', {
        method: 'POST',
        body: JSON.stringify({ username, password })
    });

    // 存储token和用户信息
    localStorage.setItem('token', data.data.token);
    currentUser = data.data.user;

    return data;
}

/**
 * 登出
 */
export async function logout() {
    try {
        await apiRequest('/auth/logout', { method: 'POST' });
    } catch (e) {
        // 忽略登出API错误
    }

    // 清除本地存储
    localStorage.removeItem('token');
    currentUser = null;

    // 刷新页面
    location.reload();
}

/**
 * 获取当前用户信息
 * @returns {Promise<object>} - 用户信息
 */
export async function getCurrentUser() {
    const data = await apiRequest('/auth/me');
    currentUser = data.data;
    return currentUser;
}

/**
 * 检查是否已登录
 * @returns {boolean}
 */
export function isLoggedIn() {
    return localStorage.getItem('token') !== null;
}

/**
 * 初始化认证状态
 * 如果有token则获取用户信息
 */
export async function initAuth() {
    if (isLoggedIn()) {
        try {
            await getCurrentUser();
            return true;
        } catch (e) {
            localStorage.removeItem('token');
            return false;
        }
    }
    return false;
}