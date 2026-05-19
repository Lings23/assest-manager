/**
 * 数据资产清单类型配置
 */

export default {
    type: 'data',
    title: '数据资产清单',
    apiType: 'data',
    tableHeaders: '<th>ID</th><th>数据名称</th><th>来源系统</th><th>数据级别</th><th>处理者</th><th>操作</th>',
    tableFields: ['id', 'data_name', 'source_system', 'data_classification', 'processor_name'],
    searchFields: ['data_name', 'source_system'],

    // 根据后端模型字段生成表单
    formTemplate: `
        <div class="form-section">
            <div class="form-section-title">网络安全等保和关键信息基础设施安全保护情况</div>
            <div class="form-grid">
                <div class="form-group">
                    <label>数据来源信息系统名称</label>
                    <input type="text" id="field-source_system" placeholder="数据来源的系统名称">
                </div>
                <div class="form-group">
                    <label>数据来源信息系统等保级别</label>
                    <select id="field-security_level">
                        <option value="一级">一级</option>
                        <option value="二级">二级</option>
                        <option value="三级">三级</option>
                    </select>
                </div>
                <div class="form-group">
                    <label>是否关键信息基础设施</label>
                    <select id="field-is_critical_infra">
                        <option value="false">否</option>
                        <option value="true">是</option>
                    </select>
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">数据基本情况</div>
            <div class="form-grid">
                <div class="form-group">
                    <label>数据名称</label>
                    <input type="text" id="field-data_name" placeholder="数据资产名称">
                </div>
                <div class="form-group full-width">
                    <label>数据项</label>
                    <textarea id="field-data_items" placeholder="具体数据项内容"></textarea>
                </div>
                <div class="form-group">
                    <label>数据级别</label>
                    <select id="field-data_classification">
                        <option value="">无</option>
                        <option value="重要数据">重要数据</option>
                        <option value="一般3级">一般3级</option>
                        <option value="一般2级">一般2级</option>
                        <option value="一般1级">一般1级</option>
                    </select>
                </div>
                <div class="form-group">
                    <label>数据载体</label>
                    <input type="text" id="field-data_carrier" placeholder="如：数据库、文件系统">
                </div>
                <div class="form-group">
                    <label>数据来源</label>
                    <select id="field-data_source">
                        <option value="共享交换">共享交换</option>
                        <option value="人工填报">人工填报</option>
                    </select>
                </div>
                <div class="form-group">
                    <label>数据规模(GB)</label>
                    <input type="number" id="field-data_size" min="0" placeholder="数据大小">
                </div>
                <div class="form-group">
                    <label>数据条数</label>
                    <input type="number" id="field-data_count" min="0" placeholder="数据记录数">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">责任人员</div>
            <div class="form-grid">
                <div class="form-group">
                    <label>数据处理者名称</label>
                    <input type="text" id="field-processor_name" placeholder="数据处理单位">
                </div>
                <div class="form-group">
                    <label>主要负责人</label>
                    <input type="text" id="field-main_leader" placeholder="负责人姓名">
                </div>
                <div class="form-group">
                    <label>数据安全负责人姓名</label>
                    <input type="text" id="field-security_leader" placeholder="安全负责人姓名">
                </div>
                <div class="form-group">
                    <label>数据安全负责人联系电话</label>
                    <input type="text" id="field-contact_phone" placeholder="联系电话">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">数据处理情况</div>
            <div class="form-grid">
                <div class="form-group full-width">
                    <label>数据处理目的</label>
                    <textarea id="field-processing_purpose" placeholder="数据处理目的说明"></textarea>
                </div>
                <div class="form-group full-width">
                    <label>数据使用范围</label>
                    <textarea id="field-usage_scope" placeholder="数据使用范围说明"></textarea>
                </div>
                <div class="form-group full-width">
                    <label>数据共享范围和方式</label>
                    <textarea id="field-sharing_scope" placeholder="数据共享范围说明"></textarea>
                </div>
                <div class="form-group">
                    <label>数据是否出境</label>
                    <select id="field-is_cross_border">
                        <option value="false">否</option>
                        <option value="true">是</option>
                    </select>
                </div>
                <div class="form-group">
                    <label>是否开展数据出境安全评估</label>
                    <select id="field-has_cross_border_assessment">
                        <option value="false">否</option>
                        <option value="true">是</option>
                    </select>
                </div>
                <div class="form-group">
                    <label>数据出境安全评估结果</label>
                    <input type="text" id="field-assessment_result" placeholder="评估结论">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">个人信息基本情况</div>
            <div class="form-grid">
                <div class="form-group">
                    <label>包含个人信息要素</label>
                    <select id="field-has_personal_info_elements">
                        <option value="false">否</option>
                        <option value="true">是</option>
                    </select>
                </div>
                <div class="form-group">
                    <label>个人信息规模（人）</label>
                    <input type="number" id="field-personal_info_scale" min="0" placeholder="涉及人数">
                </div>
                <div class="form-group">
                    <label>是否包含敏感个人信息</label>
                    <select id="field-has_sensitive_personal">
                        <option value="false">否</option>
                        <option value="true">是</option>
                    </select>
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">安全措施</div>
            <div class="form-grid">
                <div class="form-group full-width">
                    <label>数据安全防护措施</label>
                    <textarea id="field-security_measures" placeholder="数据安全保护措施"></textarea>
                </div>
                <div class="form-group full-width">
                    <label>备注</label>
                    <textarea id="field-remarks" placeholder="其他说明"></textarea>
                </div>
            </div>
        </div>
    `,

    fields: [
        // 网络安全等保和关键信息基础设施安全保护情况
        { name: 'source_system', label: '数据来源信息系统名称', type: 'text', section: '网络安全等保和关键信息基础设施安全保护情况' },
        { name: 'security_level', label: '数据来源信息系统等保级别', type: 'select', options: ['一级', '二级', '三级'], section: '网络安全等保和关键信息基础设施安全保护情况' },
        { name: 'is_critical_infra', label: '是否关键信息基础设施', type: 'select', options: ['false', 'true'], section: '网络安全等保和关键信息基础设施安全保护情况' },

        // 数据基本情况
        { name: 'data_name', label: '数据名称', type: 'text', section: '数据基本情况' },
        { name: 'data_items', label: '数据项', type: 'textarea', fullWidth: true, section: '数据基本情况' },
        { name: 'data_classification', label: '数据级别', type: 'select', options: ['', '重要数据', '一般3级', '一般2级', '一般1级'], section: '数据基本情况' },
        { name: 'data_carrier', label: '数据载体', type: 'text', section: '数据基本情况' },
        { name: 'data_source', label: '数据来源', type: 'select', options: ['共享交换', '人工填报'], section: '数据基本情况' },
        { name: 'data_size', label: '数据规模(GB)', type: 'number', section: '数据基本情况' },
        { name: 'data_count', label: '数据条数', type: 'number', section: '数据基本情况' },

        // 责任人员
        { name: 'processor_name', label: '数据处理者名称', type: 'text', section: '责任人员' },
        { name: 'main_leader', label: '主要负责人', type: 'text', section: '责任人员' },
        { name: 'security_leader', label: '数据安全负责人姓名', type: 'text', section: '责任人员' },
        { name: 'contact_phone', label: '数据安全负责人联系电话', type: 'text', section: '责任人员' },

        // 数据处理情况
        { name: 'processing_purpose', label: '数据处理目的', type: 'textarea', fullWidth: true, section: '数据处理情况' },
        { name: 'usage_scope', label: '数据使用范围', type: 'textarea', fullWidth: true, section: '数据处理情况' },
        { name: 'sharing_scope', label: '数据共享范围和方式', type: 'textarea', fullWidth: true, section: '数据处理情况' },
        { name: 'is_cross_border', label: '数据是否出境', type: 'select', options: ['false', 'true'], section: '数据处理情况' },
        { name: 'has_cross_border_assessment', label: '是否开展数据出境安全评估', type: 'select', options: ['false', 'true'], section: '数据处理情况' },
        { name: 'assessment_result', label: '数据出境安全评估结果', type: 'text', section: '数据处理情况' },

        // 个人信息基本情况
        { name: 'has_personal_info_elements', label: '包含个人信息要素', type: 'select', options: ['false', 'true'], section: '个人信息基本情况' },
        { name: 'personal_info_scale', label: '个人信息规模（人）', type: 'number', section: '个人信息基本情况' },
        { name: 'has_sensitive_personal', label: '是否包含敏感个人信息', type: 'select', options: ['false', 'true'], section: '个人信息基本情况' },

        // 安全措施
        { name: 'security_measures', label: '数据安全防护措施', type: 'textarea', fullWidth: true, section: '安全措施' },
        { name: 'remarks', label: '备注', type: 'textarea', fullWidth: true, section: '安全措施' }
    ],

    collector: null,
    hooks: {
        beforeCreate: null,
        afterEdit: null,
        beforeSave: null
    }
};