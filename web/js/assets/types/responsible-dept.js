/**
 * 责任部门类型配置
 */

export default {
    type: 'responsible-dept',
    title: '责任部门',
    apiType: 'responsible-dept',
    tableHeaders: '<th>ID</th><th>部门名称</th><th>部门编码</th><th>负责人</th><th>联系电话</th><th>部门传真</th><th>操作</th>',
    tableFields: ['id', 'department_name', 'department_code', 'department_head', 'head_phone', 'department_fax'],
    searchFields: ['department_name', 'department_head'],

    formTemplate: `
        <div class="form-section">
            <div class="form-section-title">基本信息</div>
            <div class="form-grid">
                <div class="form-group">
                    <label><span class="required">*</span>部门名称</label>
                    <input type="text" id="field-department_name" required placeholder="责任部门全称">
                </div>
                <div class="form-group">
                    <label>部门编码</label>
                    <input type="text" id="field-department_code" placeholder="部门编码">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>负责人</label>
                    <input type="text" id="field-department_head" required placeholder="负责人姓名">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>联系电话</label>
                    <input type="text" id="field-head_phone" required placeholder="联系电话">
                </div>
                <div class="form-group">
                    <label>部门传真</label>
                    <input type="text" id="field-department_fax" placeholder="传真号码">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">备注信息</div>
            <div class="form-grid">
                <div class="form-group full-width">
                    <label>备注</label>
                    <textarea id="field-remarks" placeholder="其他说明"></textarea>
                </div>
            </div>
        </div>
    `,

    fields: [
        { name: 'department_name', label: '部门名称', type: 'text', required: true, section: '基本信息' },
        { name: 'department_code', label: '部门编码', type: 'text', section: '基本信息' },
        { name: 'department_head', label: '负责人', type: 'text', required: true, section: '基本信息' },
        { name: 'head_phone', label: '联系电话', type: 'text', required: true, section: '基本信息' },
        { name: 'department_fax', label: '部门传真', type: 'text', section: '基本信息' },
        { name: 'remarks', label: '备注', type: 'textarea', fullWidth: true, section: '备注信息' }
    ],

    collector: null,
    hooks: {
        beforeCreate: null,
        afterEdit: null,
        beforeSave: null
    }
};