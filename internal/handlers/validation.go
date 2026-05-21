package handlers

import (
	"fmt"
	"asset-manager/internal/utils"
)

// ValidateAssetData 通用资产数据校验
// 根据 assetType 调用对应的校验规则
func ValidateAssetData(assetType string, data map[string]interface{}) error {
	switch assetType {
	case "system-info":
		return validateSystemInfo(data)
	case "hardware":
		return validateHardware(data)
	case "data":
		return validateData(data)
	case "supply-chain":
		return validateSupplyChain(data)
	case "vulnerability":
		return validateVulnerability(data)
	case "software-stat":
		return validateSoftwareStat(data)
	case "responsible-dept":
		return validateResponsibleDept(data)
	default:
		return nil
	}
}

// validateSystemInfo 校验信息系统清单
func validateSystemInfo(data map[string]interface{}) error {
	// 必填字段列表（匹配前端 required: true 配置）
	// 注意：interface_scope 是条件必填，单独处理
	requiredFields := []string{
		"system_name", "deploy_location", "network_name", "network_type",
		"run_status", "build_time", "has_media_platform", "mobile_app_type",
		"domain_or_ip", "subsystems", "has_external_interface",
		"supervisory_dept", "app_responsible_dept", "network_responsible_dept",
		"maintenance_mode", "construction_dept", "system_contact",
		"security_contact", "admin_contact", "maintenance_vendor",
		"integration_vendor", "development_vendor", "data_content",
		"data_storage_location", "has_personal_info", "backup_type",
		"security_level", "security_record_no", "has_cloud_deploy",
	}

	// 校验必填
	for _, field := range requiredFields {
		val, exists := data[field]
		if !exists || val == nil || val == "" {
			return fmt.Errorf("字段 %s 不能为空", getFieldLabel(field))
		}
	}

	// 条件校验：当 has_external_interface 为"是"时，interface_scope 必填
	hasInterfaceVal := data["has_external_interface"]
	if hasInterfaceVal != nil {
		strVal := fmt.Sprintf("%v", hasInterfaceVal)
		if strVal == "是" {
			interfaceScope, exists := data["interface_scope"]
			if !exists || interfaceScope == nil || interfaceScope == "" {
				return fmt.Errorf("字段 对接范围和方式 不能为空")
			}
		}
	}

	// 枚举值校验（验证数字编码范围）
	enumFields := []string{
		"network_type",
		"run_status",
		"mobile_app_type",
		"maintenance_mode",
		"backup_type",
		"security_level",
	}

	for _, field := range enumFields {
		val, exists := data[field]
		if exists && val != nil {
			if err := validateEnumValue("system-info", field, val); err != nil {
				return err
			}
		}
	}

	return nil
}

// validateHardware 校验信息化软硬件清单
func validateHardware(data map[string]interface{}) error {
	// 必填字段列表（匹配前端 required: true 配置）
	requiredFields := []string{
		"asset_name", "category", "brand", "model", "quantity",
		"department", "responsible_person", "location", "use_status",
		"mac_address", "os_version", "start_use_date", "asset_life",
	}

	for _, field := range requiredFields {
		val, exists := data[field]
		if !exists || val == nil || val == "" {
			return fmt.Errorf("字段 %s 不能为空", getFieldLabel(field))
		}
	}

	// 数量校验
	if qty, ok := data["quantity"]; ok {
		switch v := qty.(type) {
		case int:
			if v < 1 {
				return fmt.Errorf("数量必须大于0")
			}
		case float64:
			if v < 1 {
				return fmt.Errorf("数量必须大于0")
			}
		}
	}

	// 枚举校验（验证数字编码）
	enumFields := []string{"use_status", "device_status"}

	for _, field := range enumFields {
		if val, exists := data[field]; exists && val != nil {
			if err := validateEnumValue("hardware", field, val); err != nil {
				return err
			}
		}
	}

	return nil
}

// validateData 校验数据资产清单（无必填字段要求）
func validateData(data map[string]interface{}) error {
	// 该模块无需必填字段校验
	return nil
}

// validateSupplyChain 校验供应链清单
func validateSupplyChain(data map[string]interface{}) error {
	requiredFields := []string{
		"system_name", "supplier_type", "company_name",
		"province_city", "address", "contact_person",
		"contact_phone", "service_content",
	}

	for _, field := range requiredFields {
		val, exists := data[field]
		if !exists || val == nil || val == "" {
			return fmt.Errorf("字段 %s 不能为空", getFieldLabel(field))
		}
	}

	return nil
}

// validateVulnerability 校验风险漏洞清单
func validateVulnerability(data map[string]interface{}) error {
	requiredFields := []string{
		"system_name", "discovery_date", "discovery_method",
		"affected_device", "vulnerability_name", "severity",
		"risk_description", "risk_impact", "completion_date",
	}

	for _, field := range requiredFields {
		val, exists := data[field]
		if !exists || val == nil || val == "" {
			return fmt.Errorf("字段 %s 不能为空", getFieldLabel(field))
		}
	}

	// 枚举校验（验证数字编码）
	enumFields := []string{"severity", "discovery_method"}

	for _, field := range enumFields {
		if val, exists := data[field]; exists && val != nil {
			if err := validateEnumValue("vulnerability", field, val); err != nil {
				return err
			}
		}
	}

	return nil
}

// validateSoftwareStat 校验软件信息统计
func validateSoftwareStat(data map[string]interface{}) error {
	requiredFields := []string{
		"report_year", "department_name",
	}

	for _, field := range requiredFields {
		val, exists := data[field]
		if !exists || val == nil || val == "" {
			return fmt.Errorf("字段 %s 不能为空", getFieldLabel(field))
		}
	}

	// 年份校验
	if year, ok := data["report_year"]; ok {
		switch v := year.(type) {
		case int:
			if v < 2000 || v > 2100 {
				return fmt.Errorf("统计年份必须在2000-2100之间")
			}
		case float64:
			if v < 2000 || v > 2100 {
				return fmt.Errorf("统计年份必须在2000-2100之间")
			}
		}
	}

	return nil
}

// validateResponsibleDept 校验责任部门
func validateResponsibleDept(data map[string]interface{}) error {
	requiredFields := []string{
		"department_name",
	}

	for _, field := range requiredFields {
		val, exists := data[field]
		if !exists || val == nil || val == "" {
			return fmt.Errorf("字段 %s 不能为空", getFieldLabel(field))
		}
	}

	return nil
}

// getFieldLabel 获取字段的中文名称
func getFieldLabel(field string) string {
	labels := map[string]string{
		// system-info
		"system_name":              "系统名称",
		"deploy_location":          "部署地点",
		"network_name":             "网络名称",
		"network_type":             "网络类型",
		"run_status":               "运行状态",
		"build_time":               "建成时间",
		"has_media_platform":       "涉及政务新媒体平台",
		"mobile_app_type":          "移动互联网应用程序",
		"domain_or_ip":             "域名或IP",
		"subsystems":               "子系统",
		"has_external_interface":   "是否与外部系统对接",
		"interface_scope":          "对接范围和方式",
		"supervisory_dept":         "主管部门",
		"app_responsible_dept":     "应用系统运行责任部门",
		"network_responsible_dept": "基础网络运行责任部门",
		"maintenance_mode":         "运维模式",
		"construction_dept":        "建设部门",
		"system_contact":           "系统责任人及联系方式",
		"security_contact":         "安全管理员及联系方式",
		"admin_contact":            "系统管理员及联系方式",
		"maintenance_vendor":       "运维厂商",
		"integration_vendor":       "集成厂商",
		"development_vendor":       "开发厂商",
		"data_content":             "收集和存储数据主要内容",
		"data_storage_location":    "数据存储位置",
		"has_personal_info":        "是否包含个人信息",
		"backup_type":              "备份类型",
		"log_retention":            "网络日志留存情况",
		"security_level":           "等级保护定级情况",
		"security_record_no":       "等保备案号",
		"has_cloud_deploy":         "是否涉及云计算部署",
		// hardware
		"asset_name":         "资产名称",
		"category":           "类别名称",
		"brand":              "品牌",
		"model":              "品牌规格型号",
		"quantity":           "数量",
		"department":         "使用部门",
		"supplier":           "供应商全称",
		"responsible_person": "责任人",
		"user":               "使用人",
		"location":           "存放地点/部署位置",
		"use_status":         "使用状态",
		"device_status":      "设备状态",
		"network":            "运行网络",
		"ip_address":         "IP地址",
		"mac_address":        "MAC地址",
		"os_version":         "操作系统名称及版本",
		"start_use_date":     "开始使用日期",
		"warranty_end_date":  "保修截止日期",
		"asset_life":         "资产使用期限",
		"remarks":            "备注",
		// data
		"source_system":               "数据来源信息系统名称",
		"is_critical_infra":           "是否关键信息基础设施",
		"data_name":                   "数据名称",
		"data_items":                  "数据项",
		"data_classification":         "数据级别",
		"data_carrier":                "数据载体",
		"data_source":                 "数据来源",
		"data_size":                   "数据规模(GB)",
		"data_count":                  "数据条数",
		"processor_name":              "数据处理者名称",
		"main_leader":                 "主要负责人",
		"security_leader":             "数据安全负责人姓名",
		"contact_phone":               "数据安全负责人联系电话",
		"processing_purpose":          "数据处理目的",
		"usage_scope":                 "数据使用范围",
		"sharing_scope":               "数据共享范围和方式",
		"is_cross_border":             "数据是否出境",
		"has_cross_border_assessment": "是否开展数据出境安全评估",
		"assessment_result":           "数据出境安全评估结果",
		"has_personal_info_elements":  "包含个人信息要素",
		"personal_info_scale":         "个人信息规模（人）",
		"has_sensitive_personal":      "是否包含敏感个人信息",
		"security_measures":           "数据安全防护措施",
		// supply-chain
		"supplier_type":   "供应商类型",
		"company_name":    "企业名称",
		"province_city":   "省市",
		"address":         "详细地址",
		"contact_person":  "联系人",
		"service_content": "服务内容",
		// vulnerability
		"discovery_date":         "发现日期",
		"discovery_method":       "发现方式",
		"affected_device":        "涉及设备",
		"vulnerability_name":     "漏洞名称",
		"severity":               "漏洞等级",
		"risk_description":       "风险描述",
		"risk_impact":            "风险影响",
		"remediation_suggestion": "整改建议",
		"completion_date":        "整改完成日期",
		// software-stat
		"report_year":     "统计年份",
		"department_name": "责任部门名称",
	}
	if label, ok := labels[field]; ok {
		return label
	}
	return field
}

// validateEnumCode 验证枚举数字编码是否在有效范围内
// 返回 nil 表示有效，返回 error 表示无效
func validateEnumCode(assetType, fieldName string, code int) error {
	mapping := utils.GetEnumMapping(assetType, fieldName)
	if mapping == nil {
		// 不是枚举字段，跳过验证
		return nil
	}

	// 检查编码是否在映射中存在
	if _, exists := mapping[code]; exists {
		return nil
	}

	// 编码无效，返回错误
	return fmt.Errorf("字段 %s 的值 %d 不合法", getFieldLabel(fieldName), code)
}

// validateEnumValue 验证枚举值（支持 int、float64 和 string 类型）
func validateEnumValue(assetType, fieldName string, val interface{}) error {
	var code int
	switch v := val.(type) {
	case int:
		code = v
	case int64:
		code = int(v)
	case float64:
		code = int(v)
	case string:
		// 字符串类型的数字编码，尝试转换
		// 如果是中文文本，则转换为编码
		if codeFromText := utils.TextToCode(assetType, fieldName, v); codeFromText >= 0 {
			code = codeFromText
		} else {
			// 尝试解析为数字
			var err error
			code, err = parseStringToInt(v)
			if err != nil {
				return fmt.Errorf("字段 %s 的值 '%s' 不是有效的枚举值", getFieldLabel(fieldName), v)
			}
		}
	default:
		// 不是支持的类型
		return fmt.Errorf("字段 %s 的值类型不正确", getFieldLabel(fieldName))
	}
	return validateEnumCode(assetType, fieldName, code)
}
