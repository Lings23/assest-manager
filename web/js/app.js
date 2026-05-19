/**
 * Asset Manager - 应用入口
 * 初始化应用，整合所有模块
 * 使用资产类型注册表获取配置
 */

import { login, logout, currentUser, initAuth } from './auth.js';
import { openModal, closeModal } from './components/modal.js';
import { exportData, downloadTemplate } from './import-export/export.js';
import { showImportDialog } from './import-export/import.js';
import { renderDashboard } from './pages/dashboard.js';
import { renderAssetList, loadAssetList, showCreateForm, editAsset, deleteAsset } from './pages/asset-list.js';
import { getAssetConfig, getAllAssetTypes } from './assets/index.js';

// 当前页面
let currentPage = 'dashboard';

/**
 * 显示主页面
 */
function showMainPage() {
    document.getElementById('login-page').classList.add('hidden');
    document.getElementById('main-page').classList.remove('hidden');
    loadPage('dashboard');
}

/**
 * 加载页面
 * @param {string} page - 页面标识
 */
function loadPage(page) {
    currentPage = page;

    // 更新菜单激活状态
    document.querySelectorAll('.sidebar nav a').forEach(a => {
        a.classList.remove('active');
        if (a.dataset.page === page) a.classList.add('active');
    });

    const contentArea = document.getElementById('content-area');

    // 统计看板页面
    if (page === 'dashboard') {
        renderDashboard(contentArea);
        return;
    }

    // 资产列表页面 - 从注册表获取配置
    const config = getAssetConfig(page);
    if (config) {
        const title = config.title || page;
        const apiType = config.apiType || page;
        renderAssetList(contentArea, apiType, title);
    } else {
        // 未注册类型，使用默认处理
        const defaultTitles = {
            'system-info': '信息系统清单',
            'hardware': '信息化软硬件清单',
            'data-assets': '数据资产清单',
            'supply-chain': '供应链清单',
            'vulnerability': '风险漏洞清单',
            'software-stat': '软件信息统计',
            'responsible-dept': '责任部门'
        };
        const apiTypeMap = {
            'data-assets': 'data',
            'supply-chain': 'supply-chain',
            'software-stat': 'software-stat',
            'responsible-dept': 'responsible-dept'
        };
        const type = apiTypeMap[page] || page;
        const title = defaultTitles[page] || page;
        renderAssetList(contentArea, type, title);
    }
}

/**
 * 应用初始化
 */
async function init() {
    // 全局函数注册（供模板中的onclick调用）
    window.editAsset = editAsset;
    window.deleteAsset = deleteAsset;
    window.openModal = openModal;
    window.closeModal = closeModal;
    window.logout = logout;
    window.exportData = exportData;
    window.downloadTemplate = downloadTemplate;
    window.showImportDialog = showImportDialog;
    window.showCreateForm = showCreateForm;
    window.loadAssetList = loadAssetList;
    window.loadPage = loadPage;

    // 登录表单处理
    document.getElementById('login-form').addEventListener('submit', async (e) => {
        e.preventDefault();
        const username = document.getElementById('username').value;
        const password = document.getElementById('password').value;

        try {
            await login(username, password);
            showMainPage();
        } catch (error) {
            alert(error.message);
        }
    });

    // 侧边栏导航点击事件
    document.querySelectorAll('.sidebar nav a').forEach(link => {
        link.addEventListener('click', (e) => {
            e.preventDefault();
            loadPage(e.target.dataset.page);
        });
    });

    // 检查登录状态
    const loggedIn = await initAuth();
    if (loggedIn) {
        showMainPage();
    }
}

// 启动应用
console.log('[Asset Manager] app.js loaded, starting init...');
console.log('[Asset Manager] registered types:', getAllAssetTypes());
init().then(() => {
    console.log('[Asset Manager] init completed');
}).catch(err => {
    console.error('[Asset Manager] init failed:', err);
});