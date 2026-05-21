/**
 * 供应链清单类型配置
 */

export default {
    type: 'supply-chain',
    title: '供应链清单',
    apiType: 'supply-chain',
    tableHeaders: '<th>ID</th><th>企业名称</th><th>供应商类型</th><th>省市</th><th>联系人</th><th>操作</th>',
    tableFields: ['id', 'company_name', 'supplier_type', 'province_city', 'contact_person'],
    searchFields: ['company_name', 'system_name'],

    formTemplate: `
        <div class="form-section">
            <div class="form-section-title">基本信息</div>
            <div class="form-grid">
                <div class="form-group">
                    <label><span class="required">*</span>系统名称</label>
                    <input type="text" id="field-system_name" required placeholder="关联系统名称">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>供应商类型</label>
                    <select id="field-supplier_type">
                        <option value="0">设计方</option>
                        <option value="1">开发方</option>
                        <option value="2">承建方</option>
                        <option value="3">网络安全产品提供方</option>
                        <option value="4">信息化产品提供方</option>
                        <option value="5">运维方</option>
                        <option value="6">安全服务提供方</option>
                        <option value="7">信息安全评测方</option>
                        <option value="8">其他参与方</option>
                    </select>
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>企业名称</label>
                    <input type="text" id="field-company_name" required placeholder="供应商企业全称">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>省市</label>
                    <input type="text" id="field-province_city" required placeholder="如：X省X市">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">联系信息</div>
            <div class="form-grid">
                <div class="form-group full-width">
                    <label><span class="required">*</span>详细地址</label>
                    <input type="text" id="field-address" required placeholder="X市X区X街道XX号">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>联系人</label>
                    <input type="text" id="field-contact_person" required placeholder="联系人姓名">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>联系电话</label>
                    <input type="text" id="field-contact_phone" required placeholder="联系电话">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">服务内容</div>
            <div class="form-grid">
                <div class="form-group full-width">
                    <label><span class="required">*</span>服务内容</label>
                    <textarea id="field-service_content" required placeholder="供应商提供的服务内容描述"></textarea>
                </div>
                <div class="form-group full-width">
                    <label>备注</label>
                    <textarea id="field-remarks" placeholder="其他说明"></textarea>
                </div>
            </div>
        </div>
    `,

    fields: [
        { name: 'system_name', label: '系统名称', type: 'text', required: true, section: '基本信息' },
        { name: 'supplier_type', label: '供应商类型', type: 'select', options: [0, 1, 2, 3, 4, 5, 6, 7, 8], optionLabels: ['设计方', '开发方', '承建方', '网络安全产品提供方', '信息化产品提供方', '运维方', '安全服务提供方', '信息安全评测方', '其他参与方'], required: true, section: '基本信息' },
        { name: 'company_name', label: '企业名称', type: 'text', required: true, section: '基本信息' },
        { name: 'province_city', label: '省市', type: 'text', required: true, section: '基本信息' },
        { name: 'address', label: '详细地址', type: 'text', required: true, fullWidth: true, section: '联系信息' },
        { name: 'contact_person', label: '联系人', type: 'text', required: true, section: '联系信息' },
        { name: 'contact_phone', label: '联系电话', type: 'text', required: true, section: '联系信息' },
        { name: 'service_content', label: '服务内容', type: 'textarea', required: true, fullWidth: true, section: '服务内容' },
        { name: 'remarks', label: '备注', type: 'textarea', fullWidth: true, section: '服务内容' }
    ],

    collector: null,
    hooks: {
        beforeCreate: null,
        afterEdit: null,
        beforeSave: null
    }
};