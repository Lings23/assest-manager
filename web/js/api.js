/**
 * API调用封装模块
 * 提供统一的HTTP请求接口，自动处理认证和错误
 */

const API_BASE = '/api';

// Access Token只保存在当前页面内存中，刷新页面后需要重新登录。
let accessToken = null;

export function setAccessToken(token) {
    accessToken = token || null;
}

export function getAccessToken() {
    return accessToken;
}

export function clearAccessToken() {
    accessToken = null;
}

/**
 * 封装fetch请求，自动添加认证token和错误处理
 * @param {string} url - API路径（不含/api前缀）
 * @param {object} options - fetch选项
 * @returns {Promise<object>} - API响应数据
 */
export async function apiRequest(url, options = {}) {
    const headers = {
        'Content-Type': 'application/json',
        ...options.headers
    };

    // 添加认证token
	const token = getAccessToken();
    if (token) {
        headers['Authorization'] = `Bearer ${token}`;
    }

    const response = await fetch(`${API_BASE}${url}`, {
        ...options,
        headers
    });

    const data = await response.json();
    if (!response.ok) {
        throw new Error(data.message || '请求失败');
    }
    return data;
}

/**
 * 带文件的API请求（用于导入）
 * @param {string} url - API路径
 * @param {FormData} formData - 表单数据
 * @returns {Promise<object>} - API响应数据
 */
export async function apiRequestWithFile(url, formData) {
	const token = getAccessToken();
    const headers = {};
    if (token) {
        headers['Authorization'] = `Bearer ${token}`;
    }

    const response = await fetch(`${API_BASE}${url}`, {
        method: 'POST',
        headers,
        body: formData
    });

    const data = await response.json();
    if (!response.ok) {
        throw new Error(data.message || '导入请求失败');
    }
    return data;
}

/**
 * 下载文件的API请求（用于导出和模板下载）
 * @param {string} url - API路径
 * @param {string} filename - 下载文件名
 */
export async function apiDownload(url, filename) {
	const token = getAccessToken();
	const headers = {};
	if (token) headers.Authorization = `Bearer ${token}`;
	const response = await fetch(`${API_BASE}${url}`, { headers });

    if (!response.ok) {
        throw new Error('下载失败');
    }

    const blob = await response.blob();
    const blobUrl = window.URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = blobUrl;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    window.URL.revokeObjectURL(blobUrl);
}
