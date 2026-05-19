/**
 * 表格组件
 * 处理资产列表的表头和行渲染
 * 使用资产类型注册表获取配置
 */

import { renderPagination } from './pagination.js';
import { getTableHeaders, renderAllTableRows } from '../assets/utils/table-renderer.js';

/**
 * 设置表格表头
 * @param {string} type - 资产类型
 */
export function setTableHeader(type) {
    const tableHeader = document.getElementById('table-header');
    if (tableHeader) {
        tableHeader.innerHTML = getTableHeaders(type);
    }
}

/**
 * 渲染表格数据行
 * @param {string} type - 资产类型
 * @param {Array} assets - 资产数据数组
 */
export function renderTableRows(type, assets) {
    const tbody = document.getElementById('asset-table-body');
    if (tbody) {
        tbody.innerHTML = renderAllTableRows(type, assets);
    }
}

/**
 * 渲染完整表格（包括表头、数据和分页）
 * @param {string} type - 资产类型
 * @param {object} data - API返回的数据
 * @param {number} currentPage - 当前页码
 * @param {function} onPageChange - 页码变化回调
 */
export function renderTable(type, data, currentPage, onPageChange) {
    const assets = data.data || [];
    const total = data.total || 0;
    const pageSize = data.page_size || 20;

    setTableHeader(type);
    renderTableRows(type, assets);
    renderPagination(total, pageSize, currentPage, onPageChange);
}

// 导出工具函数供其他模块使用
export { getTableHeaders, renderAllTableRows } from '../assets/utils/table-renderer.js';