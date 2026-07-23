/**
 * 表格渲染器
 * 根据类型配置渲染表格表头和数据行
 */

import { getAssetConfig } from '../index.js';
import { getEnumText, isEnumField } from '../enum-mappings.js';
import { currentUser } from '../../auth.js';
import { displayText, escapeHTML } from '../../utils/html.js';

function booleanText(value) {
    if (value === true || value === 1 || value === '1' || value === '是' || value === 'true') return '是';
    if (value === false || value === 0 || value === '0' || value === '否' || value === 'false') return '否';
    return '-';
}

/**
 * 获取表格表头HTML
 * @param {string} type - 资产类型
 * @returns {string} - 表头HTML字符串
 */
export function getTableHeaders(type) {
    const config = getAssetConfig(type);
    if (config?.tableHeaders) {
        return config.tableHeaders;
    }

    // 默认表头
    return '<th>ID</th><th>名称</th><th>操作</th>';
}

/**
 * 渲染单个表格行
 * @param {string} type - 资产类型
 * @param {Object} asset - 资产数据对象
 * @returns {string} - 行HTML字符串
 */
export function renderTableRow(type, asset) {
    const config = getAssetConfig(type);
    if (!config) {
        return '<tr><td colspan="6" style="text-align:center">无配置</td></tr>';
    }

    const displayFields = config.tableFields || ['id', 'name'];
    const cells = displayFields.map(field => {
        const value = asset[field];

        // 处理枚举字段：将数字编码转换为中文文本
        if (isEnumField(type, field)) {
            const text = getEnumText(type, field, value);
			return `<td>${displayText(text)}</td>`;
        }

        // 处理布尔字段
        if (field === 'is_legalization_done' || field === 'is_critical_infra' ||
            field === 'has_external_interface' || field === 'has_personal_info' ||
            field === 'has_cloud_deploy' || field === 'has_media_platform') {
			return `<td>${booleanText(value)}</td>`;
        }

		return `<td>${displayText(value)}</td>`;
	});
	const safeType = escapeHTML(type);
	const numericID = Number.isSafeInteger(Number(asset.id)) ? Number(asset.id) : 0;
	const deleteButton = currentUser?.role === 'admin'
		? `<button class="btn-sm btn-danger" onclick="window.deleteAsset('${safeType}', ${numericID})">删除</button>`
		: '';

    return `
        <tr>
            ${cells.join('')}
            <td>
				<button class="btn-sm btn-warning" onclick="window.editAsset('${safeType}', ${numericID})">编辑</button>
				${deleteButton}
            </td>
        </tr>
    `;
}

/**
 * 渲染所有表格行
 * @param {string} type - 资产类型
 * @param {Array} assets - 资产数据数组
 * @returns {string} - 所有行HTML字符串
 */
export function renderAllTableRows(type, assets) {
    if (!assets || assets.length === 0) {
        const config = getAssetConfig(type);
        const colCount = config?.tableFields?.length || 6;
        return `<tr><td colspan="${colCount + 1}" style="text-align:center;padding:20px;color:#999;">暂无数据</td></tr>`;
    }

    return assets.map(asset => renderTableRow(type, asset)).join('');
}

/**
 * 获取表格显示字段列表
 * @param {string} type - 资产类型
 * @returns {string[]} - 显示字段名称数组
 */
export function getDisplayFields(type) {
    const config = getAssetConfig(type);
    return config?.tableFields || [];
}

/**
 * 获取搜索字段列表
 * @param {string} type - 资产类型
 * @returns {string[]} - 搜索字段名称数组
 */
export function getSearchFields(type) {
    const config = getAssetConfig(type);
    return config?.searchFields || [];
}
