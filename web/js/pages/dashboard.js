/**
 * 统计看板页面
 */

import { apiRequest } from '../api.js';
import { currentUser, logout } from '../auth.js';

/**
 * 渲染统计看板
 * @param {HTMLElement} container - 容器元素
 */
export async function renderDashboard(container) {
    container.innerHTML = '<div class="card"><p>加载中...</p></div>';

    try {
        const data = await apiRequest('/stats/summary');
        const stats = data.data;

        container.innerHTML = `
            <div class="top-bar">
                <h2>统计看板</h2>
                <div>
                    <span class="user-info">${currentUser?.username}</span>
                    <button class="logout-btn" onclick="logout()" style="margin-left: 15px;">退出</button>
                </div>
            </div>
            <div class="stats-grid">
                <div class="stat-card">
                    <div class="label">信息系统</div>
                    <div class="value">${stats.system_info_count}</div>
                </div>
                <div class="stat-card">
                    <div class="label">软硬件资产</div>
                    <div class="value">${stats.hardware_count}</div>
                </div>
                <div class="stat-card">
                    <div class="label">数据资产</div>
                    <div class="value">${stats.data_asset_count}</div>
                </div>
                <div class="stat-card">
                    <div class="label">供应链记录</div>
                    <div class="value">${stats.supply_chain_count}</div>
                </div>
                <div class="stat-card">
                    <div class="label">风险漏洞</div>
                    <div class="value">${stats.vulnerability_count}</div>
                </div>
                <div class="stat-card">
                    <div class="label">高风险漏洞</div>
                    <div class="value" style="color: #ff4d4f;">${stats.high_risk_vulns}</div>
                </div>
            </div>
        `;
    } catch (error) {
        container.innerHTML = `<div class="card"><p>加载失败: ${error.message}</p></div>`;
    }
}