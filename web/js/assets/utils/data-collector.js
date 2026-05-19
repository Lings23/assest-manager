/**
 * 数据收集器
 * 根据类型配置收集表单数据
 */

import { getAssetConfig } from '../index.js';

/**
 * 收集表单数据
 * @param {string} type - 资产类型
 * @returns {Object} - 收集的数据对象
 */
export function collectFormData(type) {
    const config = getAssetConfig(type);

    // 如果有自定义收集器，使用它
    if (config?.collector) {
        return config.collector();
    }

    // 默认收集器：遍历所有 field-* 元素
    return defaultCollector();
}

/**
 * 默认数据收集器
 * 自动收集所有以 field- 开头的输入元素
 * @returns {Object} - 收集的数据对象
 */
function defaultCollector() {
    const data = {};

    document.querySelectorAll('[id^="field-"]').forEach(el => {
        const key = el.id.replace('field-', '');

        // 类型转换处理
        if (el.type === 'number') {
            const value = parseFloat(el.value);
            data[key] = isNaN(value) ? 0 : value;
        } else if (el.type === 'checkbox') {
            data[key] = el.checked;
        } else if (el.tagName === 'SELECT') {
            // select 元素，处理布尔值选项
            const value = el.value;
            if (value === 'true' || value === 'false') {
                data[key] = value === 'true';
            } else {
                data[key] = value;
            }
        } else {
            data[key] = el.value;
        }
    });

    return data;
}

/**
 * 填充表单数据（用于编辑）
 * @param {string} type - 资产类型
 * @param {Object} data - 要填充的数据对象
 */
export function fillFormData(type, data) {
    Object.keys(data).forEach(key => {
        const field = document.getElementById(`field-${key}`);
        if (field && data[key] !== null && data[key] !== undefined) {
            // 处理不同类型元素
            if (field.type === 'checkbox') {
                field.checked = Boolean(data[key]);
            } else if (field.tagName === 'SELECT') {
                // select 元素，处理布尔值
                if (typeof data[key] === 'boolean') {
                    field.value = data[key] ? 'true' : 'false';
                } else {
                    field.value = String(data[key]);
                }
            } else {
                field.value = data[key];
            }
        }
    });
}

/**
 * 清空表单数据
 * @param {string} type - 资产类型
 */
export function clearFormData(type) {
    document.querySelectorAll('[id^="field-"]').forEach(el => {
        if (el.type === 'checkbox') {
            el.checked = false;
        } else if (el.type === 'number') {
            el.value = el.getAttribute('value') || '0';
        } else {
            el.value = '';
        }
    });
}

/**
 * 清除字段错误样式
 */
export function clearFieldErrors() {
    document.querySelectorAll('.field-error').forEach(el => {
        el.classList.remove('field-error');
    });
    document.querySelectorAll('.form-group.has-error').forEach(el => {
        el.classList.remove('has-error');
    });
    // 清除旧的toast提示
    document.querySelectorAll('.validation-toast').forEach(el => {
        el.remove();
    });
}

/**
 * 验证表单数据
 * @param {string} type - 资产类型
 * @returns {Object} - {valid: boolean, errors: Array<{field, label, message, element}>}
 */
export function validateFormData(type) {
    const config = getAssetConfig(type);
    const errors = [];

    if (!config?.fields) {
        return { valid: true, errors: [] };
    }

    // 先清除之前的错误样式
    clearFieldErrors();

    // 检查必填字段
    config.fields.forEach(field => {
        if (field.required) {
            const el = document.getElementById(`field-${field.name}`);

            if (!el) {
                errors.push({
                    field: field.name,
                    label: field.label,
                    message: `${field.label}为必填项`,
                    element: null
                });
                return;
            }

            // 根据元素类型进行校验
            const value = el.value;
            let isEmpty = false;

            if (el.type === 'checkbox') {
                // checkbox 必填校验：检查是否勾选
                isEmpty = !el.checked;
            } else if (el.tagName === 'SELECT') {
                // select 必填校验：检查是否选择了有效值（排除空字符串）
                isEmpty = value === '' || value === null;
            } else {
                // input/textarea 必填校验：检查trim后是否为空
                isEmpty = !value || !value.trim();
            }

            if (isEmpty) {
                errors.push({
                    field: field.name,
                    label: field.label,
                    message: `${field.label}为必填项`,
                    element: el
                });

                // 添加错误样式
                el.classList.add('field-error');
                const parent = el.closest('.form-group');
                if (parent) {
                    parent.classList.add('has-error');
                }
            }
        }
    });

    return {
        valid: errors.length === 0,
        errors
    };
}

/**
 * 显示验证错误提示（toast方式）
 * @param {Array} errors - 错误列表
 */
export function showValidationError(errors) {
    if (!errors || errors.length === 0) return;

    // 创建toast
    const toast = document.createElement('div');
    toast.className = 'validation-toast';

    const messages = errors.slice(0, 5).map(e => `<li>${e.message}</li>`); // 最多显示5个
    const more = errors.length > 5 ? `<li>还有 ${errors.length - 5} 个必填项...</li>` : '';

    toast.innerHTML = `
        <div class="toast-header">
            <span class="toast-icon">⚠</span>
            <span>请填写以下必填项</span>
            <button class="toast-close" onclick="this.parentElement.parentElement.remove()">×</button>
        </div>
        <div class="toast-body">
            <ul>${messages.join('')}${more}</ul>
        </div>
    `;

    document.body.appendChild(toast);

    // 5秒后自动消失
    setTimeout(() => {
        if (toast.parentElement) {
            toast.remove();
        }
    }, 5000);

    // 定位到第一个错误字段
    if (errors.length > 0 && errors[0].element) {
        errors[0].element.focus();
        errors[0].element.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }
}