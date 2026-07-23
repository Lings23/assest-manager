/**
 * 认证模块
 * 处理登录、登出、token管理、用户状态
 */

import { apiRequest, clearAccessToken, getAccessToken, setAccessToken } from './api.js';

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

	// Access Token仅保存于内存，避免持久化XSS读取浏览器存储。
	setAccessToken(data.data.token);
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

	clearAccessToken();
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
	return Boolean(getAccessToken());
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
			clearAccessToken();
            return false;
        }
    }
    return false;
}
