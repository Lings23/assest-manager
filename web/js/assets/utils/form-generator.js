/**
 * 表单生成器
 * 根据类型配置动态生成表单HTML
 */

import { getAssetConfig } from '../index.js';

/**
 * 根据类型生成表单
 * @param {string} type - 资产类型
 * @returns {string} - 表单HTML字符串
 */
export function generateForm(type) {
    const config = getAssetConfig(type);
    if (!config) {
        return '<div class="form-group"><p>暂无该类型的表单配置</p></div>';
    }

    // 如果有预定义模板，直接返回
    if (config.formTemplate) {
        return config.formTemplate;
    }

    // 否则根据 fields 动态生成
    if (config.fields && config.fields.length > 0) {
        return generateFormFromFields(config.fields);
    }

    return '<div class="form-group"><p>该类型缺少表单定义</p></div>';
}

/**
 * 根据字段定义动态生成表单
 * @param {FieldConfig[]} fields - 字段配置数组
 * @returns {string} - 表单HTML字符串
 */
function generateFormFromFields(fields) {
    const sections = groupBySection(fields);

    return Object.entries(sections).map(([sectionName, sectionFields]) => `
        <div class="form-section">
            <div class="form-section-title">${sectionName}</div>
            <div class="form-grid">
                ${sectionFields.map(field => generateField(field)).join('')}
            </div>
        </div>
    `).join('');
}

/**
 * 按分组归类字段
 * @param {FieldConfig[]} fields - 字段配置数组
 * @returns {Object} - 分组后的字段对象
 */
function groupBySection(fields) {
    const sections = {};
    fields.forEach(field => {
        const section = field.section || '基本信息';
        if (!sections[section]) {
            sections[section] = [];
        }
        sections[section].push(field);
    });
    return sections;
}

/**
 * 生成单个字段HTML
 * @param {FieldConfig} field - 字段配置
 * @returns {string} - 字段HTML字符串
 */
function generateField(field) {
    const required = field.required ? '<span class="required">*</span>' : '';
    const requiredAttr = field.required ? 'required' : '';
    const placeholder = field.placeholder || '';
    const fullWidthClass = field.fullWidth ? 'full-width' : '';
    const label = `<label>${required}${field.label}</label>`;

    let inputHtml = '';

    switch (field.type) {
        case 'select':
            const options = (field.options || []).map(opt =>
                `<option value="${opt}">${opt}</option>`
            ).join('');
            inputHtml = `<select id="field-${field.name}" ${requiredAttr}>${options}</select>`;
            break;

        case 'textarea':
            inputHtml = `<textarea id="field-${field.name}" ${requiredAttr} placeholder="${placeholder}"></textarea>`;
            break;

        case 'date':
            inputHtml = `<input type="date" id="field-${field.name}" ${requiredAttr}>`;
            break;

        case 'number':
            const minAttr = field.min !== undefined ? `min="${field.min}"` : '';
            const stepAttr = field.step !== undefined ? `step="${field.step}"` : '';
            const valueAttr = field.value !== undefined ? `value="${field.value}"` : '';
            inputHtml = `<input type="number" id="field-${field.name}" ${requiredAttr} ${minAttr} ${stepAttr} ${valueAttr} placeholder="${placeholder}">`;
            break;

        default: // text
            inputHtml = `<input type="text" id="field-${field.name}" ${requiredAttr} placeholder="${placeholder}">`;
    }

    return `<div class="form-group ${fullWidthClass}">${label}${inputHtml}</div>`;
}

/**
 * 获取表单字段列表（用于校验）
 * @param {string} type - 资产类型
 * @returns {FieldConfig[]} - 字段配置数组
 */
export function getFormFields(type) {
    const config = getAssetConfig(type);
    return config?.fields || [];
}

/**
 * 获取必填字段列表
 * @param {string} type - 资产类型
 * @returns {string[]} - 必填字段名称数组
 */
export function getRequiredFields(type) {
    const fields = getFormFields(type);
    return fields.filter(f => f.required).map(f => f.name);
}