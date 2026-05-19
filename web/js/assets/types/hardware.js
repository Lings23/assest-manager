/**
 * 信息化软硬件类型配置
 */

export default {
    type: 'hardware',
    title: '信息化软硬件清单',
    apiType: 'hardware',
    tableHeaders: '<th>ID</th><th>资产名称</th><th>类别</th><th>品牌</th><th>部门</th><th>操作</th>',
    tableFields: ['id', 'asset_name', 'category', 'brand', 'department'],
    searchFields: ['asset_name', 'brand', 'category'],

    formTemplate: `
        <div class="form-section">
            <div class="form-section-title">基本信息</div>
            <div class="form-grid">
                <div class="form-group">
                    <label><span class="required">*</span>资产名称</label>
                    <input type="text" id="field-asset_name" required placeholder="如：接入交换机">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>类别名称</label>
                    <input type="text" id="field-category" required placeholder="如：交换机">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>品牌</label>
                    <input type="text" id="field-brand" required placeholder="如：华为">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>品牌规格型号</label>
                    <input type="text" id="field-model" required placeholder="如：S5720S">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>数量</label>
                    <input type="number" id="field-quantity" value="1" min="1" required>
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>使用部门</label>
                    <input type="text" id="field-department" required placeholder="如：科技信息部">
                </div>
                <div class="form-group">
                    <label>供应商全称</label>
                    <input type="text" id="field-supplier" placeholder="如：华为技术有限公司">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">人员与位置信息</div>
            <div class="form-grid">
                <div class="form-group">
                    <label><span class="required">*</span>责任人</label>
                    <input type="text" id="field-responsible_person" required placeholder="如：张三">
                </div>
                <div class="form-group">
                    <label>使用人</label>
                    <input type="text" id="field-user" placeholder="实际使用人">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>存放地点/部署位置</label>
                    <input type="text" id="field-location" required placeholder="如：核心机房">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>使用状态</label>
                    <select id="field-use_status" required>
                        <option value="在网">在网</option>
                        <option value="不在网">不在网</option>
                        <option value="闲置">闲置</option>
                        <option value="报废">报废</option>
                    </select>
                </div>
                <div class="form-group">
                    <label>设备状态</label>
                    <select id="field-device_status">
                        <option value="正常">正常</option>
                        <option value="故障">故障</option>
                        <option value="维修中">维修中</option>
                    </select>
                </div>
                <div class="form-group">
                    <label>运行网络</label>
                    <input type="text" id="field-network" placeholder="如：办公网">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">网络与系统信息</div>
            <div class="form-grid">
                <div class="form-group">
                    <label>IP地址</label>
                    <input type="text" id="field-ip_address" placeholder="如：192.168.1.134">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>MAC地址</label>
                    <input type="text" id="field-mac_address" required placeholder="如：3F-54-13-3D-F2-AE">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>操作系统名称及版本</label>
                    <input type="text" id="field-os_version" required placeholder="如：VRP V200R">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">时间信息</div>
            <div class="form-grid">
                <div class="form-group">
                    <label><span class="required">*</span>开始使用日期</label>
                    <input type="date" id="field-start_use_date" required>
                </div>
                <div class="form-group">
                    <label>保修截止日期</label>
                    <input type="date" id="field-warranty_end_date">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>资产使用期限</label>
                    <input type="text" id="field-asset_life" required placeholder="如：永久、5年">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">备注信息</div>
            <div class="form-grid">
                <div class="form-group full-width">
                    <label>备注</label>
                    <textarea id="field-remarks" placeholder="其他说明信息"></textarea>
                </div>
            </div>
        </div>
    `,

    fields: [
        { name: 'asset_name', label: '资产名称', type: 'text', required: true, section: '基本信息' },
        { name: 'category', label: '类别名称', type: 'text', required: true, section: '基本信息' },
        { name: 'brand', label: '品牌', type: 'text', required: true, section: '基本信息' },
        { name: 'model', label: '品牌规格型号', type: 'text', required: true, section: '基本信息' },
        { name: 'quantity', label: '数量', type: 'number', required: true, section: '基本信息' },
        { name: 'department', label: '使用部门', type: 'text', required: true, section: '基本信息' },
        { name: 'supplier', label: '供应商全称', type: 'text', section: '基本信息' },
        { name: 'responsible_person', label: '责任人', type: 'text', required: true, section: '人员与位置信息' },
        { name: 'user', label: '使用人', type: 'text', section: '人员与位置信息' },
        { name: 'location', label: '存放地点/部署位置', type: 'text', required: true, section: '人员与位置信息' },
        { name: 'use_status', label: '使用状态', type: 'select', options: ['在网', '不在网', '闲置', '报废'], required: true, section: '人员与位置信息' },
        { name: 'device_status', label: '设备状态', type: 'select', options: ['正常', '故障', '维修中'], section: '人员与位置信息' },
        { name: 'network', label: '运行网络', type: 'text', section: '人员与位置信息' },
        { name: 'ip_address', label: 'IP地址', type: 'text', section: '网络与系统信息' },
        { name: 'mac_address', label: 'MAC地址', type: 'text', required: true, section: '网络与系统信息' },
        { name: 'os_version', label: '操作系统名称及版本', type: 'text', required: true, section: '网络与系统信息' },
        { name: 'start_use_date', label: '开始使用日期', type: 'date', required: true, section: '时间信息' },
        { name: 'warranty_end_date', label: '保修截止日期', type: 'date', section: '时间信息' },
        { name: 'asset_life', label: '资产使用期限', type: 'text', required: true, section: '时间信息' },
        { name: 'remarks', label: '备注', type: 'textarea', fullWidth: true, section: '备注信息' }
    ],

    // 自定义收集器处理数量字段转换
    collector: () => ({
        asset_name: document.getElementById('field-asset_name')?.value,
        category: document.getElementById('field-category')?.value,
        brand: document.getElementById('field-brand')?.value,
        model: document.getElementById('field-model')?.value,
        quantity: parseInt(document.getElementById('field-quantity')?.value) || 1,
        department: document.getElementById('field-department')?.value,
        supplier: document.getElementById('field-supplier')?.value,
        responsible_person: document.getElementById('field-responsible_person')?.value,
        user: document.getElementById('field-user')?.value,
        location: document.getElementById('field-location')?.value,
        use_status: document.getElementById('field-use_status')?.value,
        device_status: document.getElementById('field-device_status')?.value,
        network: document.getElementById('field-network')?.value,
        ip_address: document.getElementById('field-ip_address')?.value,
        mac_address: document.getElementById('field-mac_address')?.value,
        os_version: document.getElementById('field-os_version')?.value,
        start_use_date: document.getElementById('field-start_use_date')?.value,
        warranty_end_date: document.getElementById('field-warranty_end_date')?.value,
        asset_life: document.getElementById('field-asset_life')?.value,
        remarks: document.getElementById('field-remarks')?.value
    }),

    hooks: {
        beforeCreate: null,
        afterEdit: null,
        beforeSave: null
    }
};