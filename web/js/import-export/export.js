/**
 * 导出功能模块
 */

import { apiDownload } from '../api.js';

/**
 * 导出资产数据
 * @param {string} type - 资产类型
 */
export async function exportData(type) {
    const timestamp = new Date().toISOString().slice(0, 10);
    const filename = `${type}_${timestamp}.csv`;
    try {
        await apiDownload(`/assets/${type}/export`, filename);
    } catch (error) {
        alert('导出失败: ' + error.message);
    }
}

/**
 * 下载导入模板
 * @param {string} type - 资产类型
 */
export async function downloadTemplate(type) {
    const filename = `${type}_导入模板.csv`;
    try {
        await apiDownload(`/assets/${type}/template`, filename);
        alert('模板下载成功！');
    } catch (error) {
        alert('下载模板失败: ' + error.message);
    }
}