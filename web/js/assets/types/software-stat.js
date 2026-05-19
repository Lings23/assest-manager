/**
 * 软件信息统计类型配置
 * 包含责任部门联动逻辑
 */

// 责任部门联动hooks将在模块加载时动态设置
let deptLinkageHooks = null;

// 动态加载责任部门联动模块
async function loadDeptLinkageModule() {
    if (!deptLinkageHooks) {
        const module = await import('../../utils/dept-linkage.js');
        deptLinkageHooks = {
            loadResponsibleDepartments: module.loadResponsibleDepartments,
            populateDeptSelect: module.populateDeptSelect,
            onDeptSelectChange: module.onDeptSelectChange,
            setupPurchaseFieldListeners: module.setupPurchaseFieldListeners
        };
    }
    return deptLinkageHooks;
}

export default {
    type: 'software-stat',
    title: '软件信息统计',
    apiType: 'software-stat',
    tableHeaders: '<th>ID</th><th>年份</th><th>部门名称</th><th>负责人</th><th>正版化完成</th><th>操作</th>',
    tableFields: ['id', 'report_year', 'department_name', 'department_head', 'is_legalization_done'],
    searchFields: ['department_name', 'department_head'],

    formTemplate: `
        <div class="form-section">
            <div class="form-section-title">基本信息</div>
            <div class="form-grid">
                <div class="form-group">
                    <label><span class="required">*</span>年份</label>
                    <input type="number" id="field-report_year" min="2020" max="2100" required placeholder="如：2024">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>登记日期</label>
                    <input type="date" id="field-registration_date" required>
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>部门名称</label>
                    <select id="field-department_name" required>
                        <option value="">请选择责任部门</option>
                    </select>
                </div>
                <div class="form-group">
                    <label>负责人</label>
                    <input type="text" id="field-department_head" placeholder="自动填充">
                </div>
                <div class="form-group">
                    <label>联系电话</label>
                    <input type="text" id="field-head_phone" placeholder="自动填充">
                </div>
                <div class="form-group">
                    <label>部门传真</label>
                    <input type="text" id="field-department_fax" placeholder="自动填充">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>正版化完成</label>
                    <select id="field-is_legalization_done">
                        <option value="false">否</option>
                        <option value="true">是</option>
                    </select>
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">人员设备统计</div>
            <div class="form-grid">
                <div class="form-group">
                    <label>总人数</label>
                    <input type="number" id="field-total_staff_count" min="0" placeholder="部门总人数">
                </div>
                <div class="form-group">
                    <label>计算机使用人数</label>
                    <input type="number" id="field-computer_user_count" min="0">
                </div>
                <div class="form-group">
                    <label>服务器数量</label>
                    <input type="number" id="field-server_count" min="0">
                </div>
                <div class="form-group">
                    <label>台式机数量</label>
                    <input type="number" id="field-desktop_count" min="0">
                </div>
                <div class="form-group">
                    <label>笔记本数量</label>
                    <input type="number" id="field-laptop_count" min="0">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">采购_操作系统</div>
            <div class="form-grid">
                <div class="form-group">
                    <label>国产数量</label>
                    <input type="number" id="field-pur_os_dom_lic" min="0" value="0">
                </div>
                <div class="form-group">
                    <label>国产金额</label>
                    <input type="number" id="field-pur_os_dom_amt" min="0" step="0.01" value="0">
                </div>
                <div class="form-group">
                    <label>进口数量</label>
                    <input type="number" id="field-pur_os_for_lic" min="0" value="0">
                </div>
                <div class="form-group">
                    <label>进口金额</label>
                    <input type="number" id="field-pur_os_for_amt" min="0" step="0.01" value="0">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">采购_办公软件</div>
            <div class="form-grid">
                <div class="form-group">
                    <label>国产数量</label>
                    <input type="number" id="field-pur_office_dom_lic" min="0" value="0">
                </div>
                <div class="form-group">
                    <label>国产金额</label>
                    <input type="number" id="field-pur_office_dom_amt" min="0" step="0.01" value="0">
                </div>
                <div class="form-group">
                    <label>进口数量</label>
                    <input type="number" id="field-pur_office_for_lic" min="0" value="0">
                </div>
                <div class="form-group">
                    <label>进口金额</label>
                    <input type="number" id="field-pur_office_for_amt" min="0" step="0.01" value="0">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">采购_杀毒软件</div>
            <div class="form-grid">
                <div class="form-group">
                    <label>国产数量</label>
                    <input type="number" id="field-pur_av_dom_lic" min="0" value="0">
                </div>
                <div class="form-group">
                    <label>国产金额</label>
                    <input type="number" id="field-pur_av_dom_amt" min="0" step="0.01" value="0">
                </div>
                <div class="form-group">
                    <label>进口数量</label>
                    <input type="number" id="field-pur_av_for_lic" min="0" value="0">
                </div>
                <div class="form-group">
                    <label>进口金额</label>
                    <input type="number" id="field-pur_av_for_amt" min="0" step="0.01" value="0">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">累计_操作系统</div>
            <div class="form-grid">
                <div class="form-group">
                    <label>国产数量(累计)</label>
                    <input type="number" id="field-cum_os_dom_lic" min="0" value="0" placeholder="自动计算">
                </div>
                <div class="form-group">
                    <label>进口数量(累计)</label>
                    <input type="number" id="field-cum_os_for_lic" min="0" value="0" placeholder="自动计算">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">累计_办公软件</div>
            <div class="form-grid">
                <div class="form-group">
                    <label>国产数量(累计)</label>
                    <input type="number" id="field-cum_office_dom_lic" min="0" value="0" placeholder="自动计算">
                </div>
                <div class="form-group">
                    <label>进口数量(累计)</label>
                    <input type="number" id="field-cum_office_for_lic" min="0" value="0" placeholder="自动计算">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">累计_杀毒软件</div>
            <div class="form-grid">
                <div class="form-group">
                    <label>国产数量(累计)</label>
                    <input type="number" id="field-cum_av_dom_lic" min="0" value="0" placeholder="自动计算">
                </div>
                <div class="form-group">
                    <label>进口数量(累计)</label>
                    <input type="number" id="field-cum_av_for_lic" min="0" value="0" placeholder="自动计算">
                </div>
            </div>
        </div>
    `,

    fields: [
        { name: 'report_year', label: '年份', type: 'number', required: true, section: '基本信息' },
        { name: 'registration_date', label: '登记日期', type: 'date', required: true, section: '基本信息' },
        { name: 'department_name', label: '部门名称', type: 'select', required: true, section: '基本信息' },
        { name: 'department_head', label: '负责人', type: 'text', section: '基本信息' },
        { name: 'head_phone', label: '联系电话', type: 'text', section: '基本信息' },
        { name: 'department_fax', label: '部门传真', type: 'text', section: '基本信息' },
        { name: 'is_legalization_done', label: '正版化完成', type: 'select', options: ['false', 'true'], required: true, section: '基本信息' },
        { name: 'total_staff_count', label: '总人数', type: 'number', section: '人员设备统计' },
        { name: 'computer_user_count', label: '计算机使用人数', type: 'number', section: '人员设备统计' },
        { name: 'server_count', label: '服务器数量', type: 'number', section: '人员设备统计' },
        { name: 'desktop_count', label: '台式机数量', type: 'number', section: '人员设备统计' },
        { name: 'laptop_count', label: '笔记本数量', type: 'number', section: '人员设备统计' },
        { name: 'pur_os_dom_lic', label: '采购_操作系统_国产数量', type: 'number', section: '采购_操作系统' },
        { name: 'pur_os_dom_amt', label: '采购_操作系统_国产金额', type: 'number', section: '采购_操作系统' },
        { name: 'pur_os_for_lic', label: '采购_操作系统_进口数量', type: 'number', section: '采购_操作系统' },
        { name: 'pur_os_for_amt', label: '采购_操作系统_进口金额', type: 'number', section: '采购_操作系统' },
        { name: 'pur_office_dom_lic', label: '采购_办公软件_国产数量', type: 'number', section: '采购_办公软件' },
        { name: 'pur_office_dom_amt', label: '采购_办公软件_国产金额', type: 'number', section: '采购_办公软件' },
        { name: 'pur_office_for_lic', label: '采购_办公软件_进口数量', type: 'number', section: '采购_办公软件' },
        { name: 'pur_office_for_amt', label: '采购_办公软件_进口金额', type: 'number', section: '采购_办公软件' },
        { name: 'pur_av_dom_lic', label: '采购_杀毒软件_国产数量', type: 'number', section: '采购_杀毒软件' },
        { name: 'pur_av_dom_amt', label: '采购_杀毒软件_国产金额', type: 'number', section: '采购_杀毒软件' },
        { name: 'pur_av_for_lic', label: '采购_杀毒软件_进口数量', type: 'number', section: '采购_杀毒软件' },
        { name: 'pur_av_for_amt', label: '采购_杀毒软件_进口金额', type: 'number', section: '采购_杀毒软件' },
        { name: 'cum_os_dom_lic', label: '累计_操作系统_国产', type: 'number', section: '累计_操作系统' },
        { name: 'cum_os_for_lic', label: '累计_操作系统_进口', type: 'number', section: '累计_操作系统' },
        { name: 'cum_office_dom_lic', label: '累计_办公软件_国产', type: 'number', section: '累计_办公软件' },
        { name: 'cum_office_for_lic', label: '累计_办公软件_进口', type: 'number', section: '累计_办公软件' },
        { name: 'cum_av_dom_lic', label: '累计_杀毒软件_国产', type: 'number', section: '累计_杀毒软件' },
        { name: 'cum_av_for_lic', label: '累计_杀毒软件_进口', type: 'number', section: '累计_杀毒软件' }
    ],

    collector: null,

    // 责任部门联动hooks
    hooks: {
        beforeCreate: null,
        afterEdit: async () => {
            const linkage = await loadDeptLinkageModule();
            await linkage.loadResponsibleDepartments();
            const select = document.getElementById('field-department_name');
            if (select) {
                linkage.populateDeptSelect(select);
                select.addEventListener('change', linkage.onDeptSelectChange);
            }
            linkage.setupPurchaseFieldListeners();
        },
        beforeSave: () => {
            const select = document.getElementById('field-department_name');
            if (select && select.value) {
                // 触发联动计算
                const event = new Event('change');
                select.dispatchEvent(event);
            }
        }
    }
};