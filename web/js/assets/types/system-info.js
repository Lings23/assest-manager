/**
 * 信息系统清单类型配置
 */

export default {
    type: 'system-info',
    title: '信息系统清单',
    apiType: 'system-info',
    tableHeaders: '<th>ID</th><th>系统名称</th><th>部署地点</th><th>网络类型</th><th>运行状态</th><th>操作</th>',
    tableFields: ['id', 'system_name', 'deploy_location', 'network_type', 'run_status'],
    searchFields: ['system_name', 'deploy_location'],

    formTemplate: `
        <div class="form-section">
            <div class="form-section-title">基本信息</div>
            <div class="form-grid">
                <div class="form-group">
                    <label><span class="required">*</span>系统名称</label>
                    <input type="text" id="field-system_name" required placeholder="请输入系统名称">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>部署地点</label>
                    <input type="text" id="field-deploy_location" required placeholder="如：6层301中心机房">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>网络名称</label>
                    <input type="text" id="field-network_name" required placeholder="请输入网络名称">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>网络类型</label>
                    <select id="field-network_type" required>
                        <option value="互联网">互联网</option>
                        <option value="专网">专网</option>
                        <option value="互联网+专网">互联网+专网</option>
                    </select>
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>运行状态</label>
                    <select id="field-run_status" required>
                        <option value="正式运行">正式运行</option>
                        <option value="试运行">试运行</option>
                        <option value="在建">在建</option>
                        <option value="临时下线">临时下线</option>
                        <option value="停用">停用</option>
                    </select>
                </div>
                <div class="form-group">
                    <label>建成时间</label>
                    <input type="date" id="field-build_time" >
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>涉及政务新媒体平台</label>
                    <input type="text" id="field-has_media_platform" required placeholder="如有请填写平台名称">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>移动互联网应用程序</label>
                    <select id="field-mobile_app_type" required>
                        <option value="否">否</option>
                        <option value="APP">APP</option>
                        <option value="小程序">小程序</option>
                        <option value="快应用">快应用</option>
                        <option value="其他">其他</option>
                    </select>
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">网络与对接信息</div>
            <div class="form-grid">
                <div class="form-group">
                    <label><span class="required">*</span>域名或IP</label>
                    <input type="text" id="field-domain_or_ip" required placeholder="如：10.0.0.134">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>子系统</label>
                    <input type="text" id="field-subsystems" required placeholder="包含的子系统名称">
                </div>
                <div class="form-group full-width">
                    <label>功能模块</label>
                    <input type="text" id="field-function_modules" placeholder="如：公告、组织架构、OA流程">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>是否与外部系统对接</label>
                    <select id="field-has_interface" required>
                        <option value="否">否</option>
                        <option value="是">是</option>
                    </select>
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>对接范围和方式</label>
                    <input type="text" id="field-interface_scope" required placeholder="如：档案管理系统 API接口">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">责任部门与人员</div>
            <div class="form-grid">
                <div class="form-group">
                    <label><span class="required">*</span>主管部门</label>
                    <input type="text" id="field-supervisory_dept" required placeholder="请输入主管部门">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>应用系统运行责任部门</label>
                    <input type="text" id="field-app_responsible_dept" required>
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>基础网络运行责任部门</label>
                    <input type="text" id="field-network_responsible_dept" required>
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>运维模式</label>
                    <select id="field-maintenance_mode" required>
                        <option value="现场运维">现场运维</option>
                        <option value="远程运维">远程运维</option>
                        <option value="现场+远程运维">现场+远程运维</option>
                    </select>
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>建设部门</label>
                    <input type="text" id="field-construction_dept" required>
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>系统责任人及联系方式</label>
                    <input type="text" id="field-system_contact" required placeholder="如：张三152xxxx">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>安全管理员及联系方式</label>
                    <input type="text" id="field-security_contact" required placeholder="如：李四135xxxx">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>系统管理员及联系方式</label>
                    <input type="text" id="field-admin_contact" required placeholder="如：王五185xxxx">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>运维厂商</label>
                    <input type="text" id="field-maintenance_vendor" required placeholder="运维服务商名称">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>集成厂商</label>
                    <input type="text" id="field-integration_vendor" required placeholder="系统集成商名称">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>开发厂商</label>
                    <input type="text" id="field-development_vendor" required placeholder="软件开发商名称">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">数据与安全信息</div>
            <div class="form-grid">
                <div class="form-group full-width">
                    <label><span class="required">*</span>收集和存储数据主要内容</label>
                    <textarea id="field-data_content" required placeholder="如：组织数据、公文数据、公告数据"></textarea>
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>数据存储位置</label>
                    <input type="text" id="field-data_storage_location" required placeholder="如：数据库">
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>是否包含个人信息</label>
                    <select id="field-has_personal_info" required>
                        <option value="否">否</option>
                        <option value="是">是</option>
                    </select>
                </div>
                <div class="form-group">
                    <label>重要数据风险评估结论</label>
                    <input type="text" id="field-important_data_risk" placeholder="风险评估结论">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">备份情况</div>
            <div class="form-grid">
                <div class="form-group">
                    <label><span class="required">*</span>备份类型</label>
                    <select id="field-backup_type" required>
                        <option value="数据灾备">数据灾备</option>
                        <option value="系统灾备">系统灾备</option>
                        <option value="数据灾备+系统灾备">数据灾备+系统灾备</option>
                        <option value="无灾备">无灾备</option>
                    </select>
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>网络日志留存情况</label>
                    <input type="text" id="field-log_retention" placeholder="如：6个月">
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">等保和密评情况</div>
            <div class="form-grid">
                <div class="form-group">
                    <label><span class="required">*</span>等级保护定级情况</label>
                    <select id="field-security_level" required>
                        <option value="一级">一级</option>
                        <option value="二级">二级</option>
                        <option value="三级">三级</option>
                        <option value="未定级">未定级</option>
                    </select>
                </div>
                <div class="form-group">
                    <label><span class="required">*</span>等保备案号</label>
                    <input type="text" id="field-security_record_no" required placeholder="等保备案编号">
                </div>
                <div class="form-group">
                    <label>本年度等保测评情况</label>
                    <select id="field-security_assessment">
                        <option value="">未测评</option>
                        <option value="符合">符合</option>
                        <option value="基本符合">基本符合</option>
                        <option value="不符合">不符合</option>
                    </select>
                </div>
                <div class="form-group">
                    <label>本年度密码应用安全性评估情况</label>
                    <select id="field-crypto_assessment">
                        <option value="">未评估</option>
                        <option value="符合">符合</option>
                        <option value="基本符合">基本符合</option>
                        <option value="不符合">不符合</option>
                    </select>
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">云服务情况</div>
            <div class="form-grid">
                <div class="form-group">
                    <label><span class="required">*</span>是否涉及云计算部署</label>
                    <select id="field-has_cloud_deploy" required>
                        <option value="false">否</option>
                        <option value="true">是</option>
                    </select>
                </div>
                <div class="form-group">
                    <label>云服务提供商名称</label>
                    <input type="text" id="field-cloud_provider" placeholder="如：阿里云、腾讯云">
                </div>
                <div class="form-group">
                    <label>云服务通过云安全审查情况</label>
                    <select id="field-cloud_security_review">
                        <option value="">未涉及</option>
                        <option value="通过">通过</option>
                        <option value="未通过">未通过</option>
                        <option value="未参加">未参加</option>
                    </select>
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">供应链情况</div>
            <div class="form-grid">
                <div class="form-group full-width">
                    <label>安全设备</label>
                    <textarea id="field-security_devices" placeholder="设备名称|品牌|数量"></textarea>
                </div>
                <div class="form-group full-width">
                    <label>网络设备</label>
                    <textarea id="field-network_devices" placeholder="设备名称|品牌|数量"></textarea>
                </div>
                <div class="form-group full-width">
                    <label>操作系统|版本|数量</label>
                    <textarea id="field-os_info" placeholder="如：CentOS 7.9 x5"></textarea>
                </div>
                <div class="form-group full-width">
                    <label>数据库|版本|数量</label>
                    <textarea id="field-database_info" placeholder="如：MySQL 8.0 x2"></textarea>
                </div>
                <div class="form-group full-width">
                    <label>中间件及版本</label>
                    <textarea id="field-middleware_info" placeholder="如：Nginx 1.20、Redis 6.2"></textarea>
                </div>
                <div class="form-group full-width">
                    <label>开发框架</label>
                    <textarea id="field-dev_framework" placeholder="如：Spring Boot、Vue.js"></textarea>
                </div>
                <div class="form-group full-width">
                    <label>第三方组件</label>
                    <textarea id="field-third_party_components" placeholder="使用的开源框架、商业软件产品等"></textarea>
                </div>
                <div class="form-group full-width">
                    <label>算力租赁情况</label>
                    <textarea id="field-computing_rental" placeholder="是否租赁，租赁情况和相关单位"></textarea>
                </div>
            </div>
        </div>
        <div class="form-section">
            <div class="form-section-title">备注</div>
            <div class="form-grid">
                <div class="form-group full-width">
                    <label>备注</label>
                    <textarea id="field-remarks" placeholder="其他说明信息"></textarea>
                </div>
            </div>
        </div>
    `,

    fields: [
        // 基本信息
        { name: 'system_name', label: '系统名称', type: 'text', required: true, section: '基本信息' },
        { name: 'deploy_location', label: '部署地点', type: 'text', required: true, section: '基本信息' },
        { name: 'network_name', label: '网络名称', type: 'text', required: true, section: '基本信息' },
        { name: 'network_type', label: '网络类型', type: 'select', options: ['互联网', '专网', '互联网+专网'], required: true, section: '基本信息' },
        { name: 'run_status', label: '运行状态', type: 'select', options: ['正式运行', '试运行', '在建', '临时下线', '停用'], required: true, section: '基本信息' },
        { name: 'build_time', label: '建成时间', type: 'date', required: true, section: '基本信息' },
        { name: 'has_media_platform', label: '涉及政务新媒体平台', type: 'text', required: true, section: '基本信息' },
        { name: 'mobile_app_type', label: '移动互联网应用程序', type: 'select', options: ['否', 'APP', '小程序', '快应用', '其他'], required: true, section: '基本信息' },

        // 网络与对接信息
        { name: 'domain_or_ip', label: '域名或IP', type: 'text', required: true, section: '网络与对接信息' },
        { name: 'subsystems', label: '子系统', type: 'text', required: true, section: '网络与对接信息' },
        { name: 'function_modules', label: '功能模块', type: 'text', fullWidth: true, section: '网络与对接信息' },
        { name: 'has_interface', label: '是否与外部系统对接', type: 'select', options: ['否', '是'], required: true, section: '网络与对接信息' },
        { name: 'interface_scope', label: '对接范围和方式', type: 'text', required: true, section: '网络与对接信息' },

        // 责任部门与人员
        { name: 'supervisory_dept', label: '主管部门', type: 'text', required: true, section: '责任部门与人员' },
        { name: 'app_responsible_dept', label: '应用系统运行责任部门', type: 'text', required: true, section: '责任部门与人员' },
        { name: 'network_responsible_dept', label: '基础网络运行责任部门', type: 'text', required: true, section: '责任部门与人员' },
        { name: 'maintenance_mode', label: '运维模式', type: 'select', options: ['现场运维', '远程运维', '现场+远程运维'], required: true, section: '责任部门与人员' },
        { name: 'construction_dept', label: '建设部门', type: 'text', required: true, section: '责任部门与人员' },
        { name: 'system_contact', label: '系统责任人及联系方式', type: 'text', required: true, section: '责任部门与人员' },
        { name: 'security_contact', label: '安全管理员及联系方式', type: 'text', required: true, section: '责任部门与人员' },
        { name: 'admin_contact', label: '系统管理员及联系方式', type: 'text', required: true, section: '责任部门与人员' },
        { name: 'maintenance_vendor', label: '运维厂商', type: 'text', required: true, section: '责任部门与人员' },
        { name: 'integration_vendor', label: '集成厂商', type: 'text', required: true, section: '责任部门与人员' },
        { name: 'development_vendor', label: '开发厂商', type: 'text', required: true, section: '责任部门与人员' },

        // 数据与安全信息
        { name: 'data_content', label: '收集和存储数据主要内容', type: 'textarea', required: true, fullWidth: true, section: '数据与安全信息' },
        { name: 'data_storage_location', label: '数据存储位置', type: 'text', required: true, section: '数据与安全信息' },
        { name: 'has_personal_info', label: '是否包含个人信息', type: 'select', options: ['否', '是'], required: true, section: '数据与安全信息' },
        { name: 'important_data_risk', label: '重要数据风险评估结论', type: 'text', section: '数据与安全信息' },

        // 备份情况
        { name: 'backup_type', label: '备份类型', type: 'select', options: ['数据灾备', '系统灾备', '数据灾备+系统灾备', '无灾备'], required: true, section: '备份情况' },
        { name: 'log_retention', label: '网络日志留存情况', type: 'text', section: '备份情况' },

        // 等保和密评情况
        { name: 'security_level', label: '等保级别', type: 'select', options: ['一级', '二级', '三级', '未定级'], required: true, section: '等保和密评情况' },
        { name: 'security_record_no', label: '等保备案号', type: 'text', required: true, section: '等保和密评情况' },
        { name: 'security_assessment', label: '本年度等保测评情况', type: 'select', options: ['', '符合', '基本符合', '不符合'], section: '等保和密评情况' },
        { name: 'crypto_assessment', label: '本年度密码应用安全性评估情况', type: 'select', options: ['', '符合', '基本符合', '不符合'], section: '等保和密评情况' },

        // 云服务情况
        { name: 'has_cloud_deploy', label: '是否涉及云计算部署', type: 'select', options: ['false', 'true'], required: true, section: '云服务情况' },
        { name: 'cloud_provider', label: '云服务提供商名称', type: 'text', section: '云服务情况' },
        { name: 'cloud_security_review', label: '云服务通过云安全审查情况', type: 'select', options: ['', '通过', '未通过', '未参加'], section: '云服务情况' },

        // 供应链情况
        { name: 'security_devices', label: '安全设备', type: 'textarea', fullWidth: true, section: '供应链情况' },
        { name: 'network_devices', label: '网络设备', type: 'textarea', fullWidth: true, section: '供应链情况' },
        { name: 'os_info', label: '操作系统|版本|数量', type: 'textarea', fullWidth: true, section: '供应链情况' },
        { name: 'database_info', label: '数据库|版本|数量', type: 'textarea', fullWidth: true, section: '供应链情况' },
        { name: 'middleware_info', label: '中间件及版本', type: 'textarea', fullWidth: true, section: '供应链情况' },
        { name: 'dev_framework', label: '开发框架', type: 'textarea', fullWidth: true, section: '供应链情况' },
        { name: 'third_party_components', label: '第三方组件', type: 'textarea', fullWidth: true, section: '供应链情况' },
        { name: 'computing_rental', label: '算力租赁情况', type: 'textarea', fullWidth: true, section: '供应链情况' },

        // 备注
        { name: 'remarks', label: '备注', type: 'textarea', fullWidth: true, section: '备注' }
    ],

    collector: null,
    hooks: {
        beforeCreate: null,
        afterEdit: null,
        beforeSave: null
    }
};