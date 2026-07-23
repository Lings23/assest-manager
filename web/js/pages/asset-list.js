/**
 * 资产列表页面
 * 统一处理所有资产类型的列表、创建、编辑、删除
 * 使用资产类型注册表和通用工具函数
 */

import { apiRequest } from '../api.js';
import { currentUser, logout } from '../auth.js';
import { openModal, closeModal } from '../components/modal.js';
import { renderTable } from '../components/table.js';
import { getAssetConfig } from '../assets/index.js';
import { generateForm } from '../assets/utils/form-generator.js';
import { collectFormData, fillFormData, validateFormData, showValidationError, clearFieldErrors } from '../assets/utils/data-collector.js';
import { exportData, downloadTemplate } from '../import-export/export.js';
import { showImportDialog } from '../import-export/import.js';
import { escapeHTML } from '../utils/html.js';

/**
 * 渲染资产列表页面
 * @param {HTMLElement} container - 容器元素
 * @param {string} type - 资产类型
 * @param {string} title - 页面标题
 */
export async function renderAssetList(container, type, title) {
	const safeType = escapeHTML(type);
	const safeTitle = escapeHTML(title);
	const safeUsername = escapeHTML(currentUser?.username || '');
	container.innerHTML = `
        <div class="top-bar">
			<h2>${safeTitle}</h2>
            <div>
				<span class="user-info">${safeUsername}</span>
                <button class="logout-btn" onclick="logout()" style="margin-left: 15px;">退出</button>
            </div>
        </div>
        <div class="card">
            <div class="btn-group">
				<button class="btn-sm btn-primary" onclick="showCreateForm('${safeType}')">新增</button>
				<button class="btn-sm btn-success" onclick="exportData('${safeType}')">导出</button>
				<button class="btn-sm btn-warning" onclick="showImportDialog('${safeType}')">导入</button>
            </div>
            <div class="search-bar">
                <input type="text" id="search-input" placeholder="搜索...">
				<button class="btn-sm btn-primary" onclick="loadAssetList('${safeType}', 1)">搜索</button>
            </div>
            <div class="table-container">
                <table>
                    <thead>
                        <tr id="table-header">
                            <!-- 动态表头 -->
                        </tr>
                    </thead>
                    <tbody id="asset-table-body">
                        <tr><td colspan="6">加载中...</td></tr>
                    </tbody>
                </table>
            </div>
            <div class="pagination" id="pagination"></div>
        </div>
    `;

    await loadAssetList(type, 1);
}

/**
 * 加载资产列表数据
 * @param {string} type - 资产类型
 * @param {number} page - 页码
 */
export async function loadAssetList(type, page) {
    const search = document.getElementById('search-input')?.value || '';

    try {
		const data = await apiRequest(`/assets/${type}?page=${page}&page_size=20&search=${encodeURIComponent(search)}`);
        renderTable(type, data.data, page, (newPage) => loadAssetList(type, newPage));
    } catch (error) {
        document.getElementById('asset-table-body').innerHTML =
			`<tr><td colspan="6">加载失败: ${escapeHTML(error.message)}</td></tr>`;
    }
}

/**
 * 显示创建表单
 * @param {string} type - 资产类型
 */
export async function showCreateForm(type) {
    const config = getAssetConfig(type);

    openModal('新增资产', generateForm(type), () => saveAsset(type));

    // 执行 beforeCreate hook
    if (config?.hooks?.beforeCreate) {
        config.hooks.beforeCreate();
    }

    // 执行 afterEdit hook（用于表单初始化，如责任部门下拉）
    if (config?.hooks?.afterEdit) {
        await config.hooks.afterEdit();
    }
}

/**
 * 保存资产
 * @param {string} type - 资产类型
 */
export async function saveAsset(type) {
    const config = getAssetConfig(type);

    // 执行 beforeSave hook
    if (config?.hooks?.beforeSave) {
        config.hooks.beforeSave();
    }

    // 验证数据
    const validation = validateFormData(type);
    if (!validation.valid) {
        showValidationError(validation.errors);
        return;
    }

    const data = collectFormData(type);

    try {
        await apiRequest(`/assets/${type}`, {
            method: 'POST',
            body: JSON.stringify(data)
        });
        alert('创建成功');
        closeModal();
        loadAssetList(type, 1);
    } catch (error) {
        alert('创建失败: ' + error.message);
    }
}

/**
 * 编辑资产
 * @param {string} type - 资产类型
 * @param {number} id - 资产ID
 */
export async function editAsset(type, id) {
    const config = getAssetConfig(type);

    try {
        const response = await apiRequest(`/assets/${type}/${id}`);
        const asset = response.data;

        openModal('编辑资产', generateForm(type), () => updateAsset(type, id));

        // 执行 afterEdit hook
        if (config?.hooks?.afterEdit) {
            await config.hooks.afterEdit();
        }

        // 填充表单数据
        setTimeout(() => {
            fillFormData(type, asset);

            // 执行 beforeSave hook（用于联动计算）
            if (config?.hooks?.beforeSave) {
                config.hooks.beforeSave();
            }
        }, 100);
    } catch (error) {
        alert('加载失败: ' + error.message);
    }
}

/**
 * 更新资产
 * @param {string} type - 资产类型
 * @param {number} id - 资产ID
 */
export async function updateAsset(type, id) {
    const config = getAssetConfig(type);

    // 执行 beforeSave hook
    if (config?.hooks?.beforeSave) {
        config.hooks.beforeSave();
    }

    // 验证数据
    const validation = validateFormData(type);
    if (!validation.valid) {
        showValidationError(validation.errors);
        return;
    }

    const data = collectFormData(type);

    try {
        await apiRequest(`/assets/${type}/${id}`, {
            method: 'PUT',
            body: JSON.stringify(data)
        });
        alert('更新成功');
        closeModal();
        loadAssetList(type, 1);
    } catch (error) {
        alert('更新失败: ' + error.message);
    }
}

/**
 * 删除资产
 * @param {string} type - 资产类型
 * @param {number} id - 资产ID
 */
export async function deleteAsset(type, id) {
    if (!confirm('确定要删除此记录吗?')) return;

    try {
        await apiRequest(`/assets/${type}/${id}`, { method: 'DELETE' });
        alert('删除成功');
        loadAssetList(type, 1);
    } catch (error) {
        alert('删除失败: ' + error.message);
    }
}
