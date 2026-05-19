/**
 * 责任部门联动模块
 * 处理责任部门选择时的自动填充和累计计算
 */

import { apiRequest } from '../api.js';

// 责任部门列表缓存
let responsibleDeptList = [];

/**
 * 加载责任部门列表
 * @returns {Promise<Array>} - 责任部门列表
 */
export async function loadResponsibleDepartments() {
    try {
        const response = await apiRequest('/assets/responsible-dept?page=1&page_size=1000');
        responsibleDeptList = response.data.data || [];
        return responsibleDeptList;
    } catch (error) {
        console.error('加载责任部门失败:', error);
        return [];
    }
}

/**
 * 填充责任部门下拉框
 * @param {HTMLSelectElement} selectElement - 下拉框元素
 */
export function populateDeptSelect(selectElement) {
    selectElement.innerHTML = '<option value="">请选择责任部门</option>';
    responsibleDeptList.forEach(dept => {
        const option = document.createElement('option');
        option.value = dept.department_name;
        option.textContent = dept.department_name;
        option.dataset.head = dept.department_head || '';
        option.dataset.phone = dept.head_phone || '';
        option.dataset.fax = dept.department_fax || '';
        selectElement.appendChild(option);
    });

    // 添加"其他"选项用于手动输入
    const customOption = document.createElement('option');
    customOption.value = '__custom__';
    customOption.textContent = '其他（手动输入）';
    selectElement.appendChild(customOption);
}

/**
 * 责任部门选择变化处理
 * 自动填充负责人、联系电话、传真字段
 */
export function onDeptSelectChange() {
    const select = document.getElementById('field-department_name');
    const selectedOption = select.options[select.selectedIndex];

    const headField = document.getElementById('field-department_head');
    const phoneField = document.getElementById('field-head_phone');
    const faxField = document.getElementById('field-department_fax');

    if (selectedOption.value === '__custom__') {
        // 解锁字段允许手动输入
        headField.readOnly = false;
        phoneField.readOnly = false;
        faxField.readOnly = false;
        headField.value = '';
        phoneField.value = '';
        faxField.value = '';
        headField.placeholder = '如：张三';
        phoneField.placeholder = '如：13812345678';
        faxField.placeholder = '传真号码';
    } else if (selectedOption.value === '') {
        // 未选择，清空字段
        headField.value = '';
        phoneField.value = '';
        faxField.value = '';
        headField.placeholder = '选择部门后自动填充';
        phoneField.placeholder = '选择部门后自动填充';
        faxField.placeholder = '选择部门后自动填充';
    } else {
        // 自动填充并锁定
        headField.readOnly = true;
        phoneField.readOnly = true;
        faxField.readOnly = true;
        headField.value = selectedOption.dataset.head || '';
        phoneField.value = selectedOption.dataset.phone || '';
        faxField.value = selectedOption.dataset.fax || '';
    }

    // 触发累计信息计算
    triggerCumulativeCalc();
}

/**
 * 触发累计信息计算
 */
export function triggerCumulativeCalc() {
    const deptName = document.getElementById('field-department_name')?.value;
    const regDate = document.getElementById('field-registration_date')?.value;

    // 条件：部门已选择（非空、非"其他"）且日期已填写
    if (deptName && deptName !== '__custom__' && regDate) {
        calculateCumulative(deptName, regDate);
    }
}

/**
 * 计算累计信息
 * @param {string} deptName - 部门名称
 * @param {string} regDate - 登记日期
 */
async function calculateCumulative(deptName, regDate) {
    try {
        // 获取当前采购值
        const currentPurchase = {
            pur_os_dom_lic: parseInt(document.getElementById('field-pur_os_dom_lic')?.value) || 0,
            pur_os_for_lic: parseInt(document.getElementById('field-pur_os_for_lic')?.value) || 0,
            pur_office_dom_lic: parseInt(document.getElementById('field-pur_office_dom_lic')?.value) || 0,
            pur_office_for_lic: parseInt(document.getElementById('field-pur_office_for_lic')?.value) || 0,
            pur_av_dom_lic: parseInt(document.getElementById('field-pur_av_dom_lic')?.value) || 0,
            pur_av_for_lic: parseInt(document.getElementById('field-pur_av_for_lic')?.value) || 0
        };

        const response = await apiRequest(
            `/assets/software-stat/calculate-cumulative?department_name=${encodeURIComponent(deptName)}&registration_date=${regDate}`,
            { method: 'POST', body: JSON.stringify(currentPurchase) }
        );

        const cumulative = response.data;

        // 填充累计字段
        const fields = ['cum_os_dom_lic', 'cum_os_for_lic', 'cum_office_dom_lic', 'cum_office_for_lic', 'cum_av_dom_lic', 'cum_av_for_lic'];
        fields.forEach(field => {
            const el = document.getElementById(`field-${field}`);
            if (el) {
                el.value = cumulative[field] || '';
            }
        });
    } catch (error) {
        console.error('累计计算失败:', error);
    }
}

/**
 * 为采购字段添加onChange监听
 */
export function setupPurchaseFieldListeners() {
    const purFields = ['pur_os_dom_lic', 'pur_os_for_lic', 'pur_office_dom_lic', 'pur_office_for_lic', 'pur_av_dom_lic', 'pur_av_for_lic'];
    purFields.forEach(field => {
        const el = document.getElementById(`field-${field}`);
        if (el) {
            el.addEventListener('change', triggerCumulativeCalc);
        }
    });
}