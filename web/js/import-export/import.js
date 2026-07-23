/**
 * 导入功能模块
 */

import { apiRequestWithFile } from '../api.js';
import { downloadTemplate } from './export.js';
import { escapeHTML } from '../utils/html.js';

/**
 * 显示导入对话框
 * @param {string} type - 资产类型
 */
export function showImportDialog(type) {
    const content = `
        <div style="margin-bottom: 20px;">
            <p style="margin-bottom: 10px; color: #666;">请先下载模板，按照模板格式填写数据后上传：</p>
            <button class="btn-sm btn-primary" onclick="window.downloadTemplate('${type}')">
                下载模板
            </button>
        </div>
        <div class="form-group">
            <label style="display: block; margin-bottom: 8px; font-weight: bold;">选择CSV文件</label>
            <input type="file" id="import-file" accept=".csv" style="width: 100%; padding: 8px; border: 1px solid #ddd; border-radius: 4px;" />
        </div>
        <div id="import-result" style="margin-top: 15px; padding: 10px; border-radius: 4px;"></div>
    `;

    // 使用全局openModal函数
    window.openModal('导入数据', content, () => doImport(type));

    // 设置全局下载模板函数
    window.downloadTemplate = downloadTemplate;
}

/**
 * 执行导入
 * @param {string} type - 资产类型
 */
async function doImport(type) {
    const fileInput = document.getElementById('import-file');
    if (!fileInput.files.length) {
        alert('请选择要导入的文件');
        return;
    }

    const file = fileInput.files[0];
    if (!file.name.endsWith('.csv')) {
        alert('请选择CSV文件');
        return;
    }

    const formData = new FormData();
    formData.append('file', file);

    const resultDiv = document.getElementById('import-result');
    resultDiv.innerHTML = '<div style="color: #666;">正在导入...</div>';

    try {
        const result = await apiRequestWithFile(`/assets/${type}/import`, formData);

        if (result.code === 200) {
            let html = `<div style="color: green;">
				<p><strong>${escapeHTML(result.message)}</strong></p>`;

            if (result.errors && result.errors.length > 0) {
                html += `<p style="margin-top: 10px; color: #ff6b6b;">错误详情：</p>
                    <ul style="max-height: 200px; overflow-y: auto; padding-left: 20px;">`;
                result.errors.forEach(err => {
					html += `<li style="margin-bottom: 5px;">${escapeHTML(err)}</li>`;
                });
                html += '</ul>';
            }

            html += '</div>';
            resultDiv.innerHTML = html;

            // 刷新列表
            setTimeout(() => {
                window.loadAssetList(type, 1);
            }, 1500);
        } else {
			resultDiv.innerHTML = `<div style="color: red;"><strong>导入失败</strong><br>${escapeHTML(result.message)}</div>`;
        }
    } catch (error) {
		resultDiv.innerHTML = `<div style="color: red;">导入失败: ${escapeHTML(error.message)}</div>`;
    }
}
