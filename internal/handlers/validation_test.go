package handlers

import (
	"testing"
)

// TestValidateSystemInfo_Success 测试正常数据校验通过
func TestValidateSystemInfo_Success(t *testing.T) {
	data := map[string]interface{}{
		"system_name":             "OA办公系统",
		"deploy_location":         "6层301中心机房",
		"network_name":            "政务内网",
		"network_type":            "专网",
		"run_status":              "正式运行",
		"build_time":              "2023-06-15",
		"has_media_platform":      "微信公众号",
		"mobile_app_type":         "小程序",
		"domain_or_ip":            "10.0.0.134",
		"subsystems":              "公文管理、公告管理",
		"has_external_interface":  "是",
		"interface_scope":         "档案管理系统 API接口",
		"supervisory_dept":        "信息中心",
		"app_responsible_dept":    "信息中心",
		"network_responsible_dept": "网络运维部",
		"maintenance_mode":        "现场+远程运维",
		"construction_dept":       "信息化建设处",
		"system_contact":          "张三 152xxxx1234",
		"security_contact":        "李四 135xxxx5678",
		"admin_contact":           "王五 185xxxx9012",
		"maintenance_vendor":      "XX科技有限公司",
		"integration_vendor":      "YY系统集成公司",
		"development_vendor":      "ZZ软件开发公司",
		"data_content":            "组织数据、公文数据",
		"data_storage_location":   "MySQL数据库",
		"has_personal_info":       "是",
		"backup_type":             "数据灾备+系统灾备",
		"security_level":          "三级",
		"security_record_no":      "3301-2023-0001",
		"has_cloud_deploy":        "false",
	}

	err := validateSystemInfo(data)
	if err != nil {
		t.Errorf("预期校验通过，实际返回错误: %v", err)
	}
}

// TestValidateSystemInfo_HasExternalInterface 测试 has_external_interface 字段
func TestValidateSystemInfo_HasExternalInterface(t *testing.T) {
	tests := []struct {
		name     string
		value    interface{}
		wantErr  bool
		errMsg   string
	}{
		{"有效值-是", "是", false, ""},
		{"有效值-否", "否", false, ""},
		{"空值-nil", nil, true, "字段 是否与外部系统对接 不能为空"},
		{"空值-空字符串", "", true, "字段 是否与外部系统对接 不能为空"},
		{"非法值", "未知", true, "字段 是否与外部系统对接 的值"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := completeSystemInfoData()
			data["has_external_interface"] = tt.value

			err := validateSystemInfo(data)

			if tt.wantErr {
				if err == nil {
					t.Errorf("预期错误 '%s'，实际通过", tt.errMsg)
				} else if !containsStr(err.Error(), tt.errMsg) {
					t.Errorf("预期错误包含 '%s'，实际错误 '%v'", tt.errMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("预期通过，实际错误: %v", err)
				}
			}
		})
	}
}

// TestValidateSystemInfo_InterfaceScopeConditional 测试 interface_scope 条件校验
func TestValidateSystemInfo_InterfaceScopeConditional(t *testing.T) {
	tests := []struct {
		name            string
		hasInterface    string
		interfaceScope  string
		wantErr         bool
		errMsg          string
	}{
		{"有对接-有范围", "是", "档案系统API", false, ""},
		{"有对接-无范围", "是", "", true, "字段 对接范围和方式 不能为空"},
		{"无对接-有范围", "否", "无", false, ""},
		{"无对接-无范围", "否", "", false, ""}, // 无对接时，范围可选
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := completeSystemInfoData()
			data["has_external_interface"] = tt.hasInterface
			data["interface_scope"] = tt.interfaceScope

			err := validateSystemInfo(data)

			if tt.wantErr {
				if err == nil {
					t.Errorf("预期错误 '%s'，实际通过", tt.errMsg)
				} else if !containsStr(err.Error(), tt.errMsg) {
					t.Errorf("预期错误包含 '%s'，实际错误 '%v'", tt.errMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("预期通过，实际错误: %v", err)
				}
			}
		})
	}
}

// TestValidateSystemInfo_MissingRequiredFields 测试必填字段缺失
func TestValidateSystemInfo_MissingRequiredFields(t *testing.T) {
	requiredFields := []struct {
		field    string
		label    string
	}{
		{"system_name", "系统名称"},
		{"deploy_location", "部署地点"},
		{"network_name", "网络名称"},
		{"network_type", "网络类型"},
		{"run_status", "运行状态"},
		{"build_time", "建成时间"},
		{"domain_or_ip", "域名或IP"},
		{"subsystems", "子系统"},
		{"has_external_interface", "是否与外部系统对接"},
		{"supervisory_dept", "主管部门"},
		{"app_responsible_dept", "应用系统运行责任部门"},
		{"security_level", "等级保护定级情况"},
		{"security_record_no", "等保备案号"},
	}

	for _, rf := range requiredFields {
		t.Run("缺失_"+rf.field, func(t *testing.T) {
			data := completeSystemInfoData()
			data[rf.field] = ""

			err := validateSystemInfo(data)

			if err == nil {
				t.Errorf("字段 %s 缺失时预期错误，实际通过", rf.field)
			} else {
				expectedMsg := "字段 " + rf.label + " 不能为空"
				if !containsStr(err.Error(), expectedMsg) {
					t.Errorf("预期错误 '%s'，实际 '%v'", expectedMsg, err)
				}
			}
		})
	}
}

// TestValidateSystemInfo_EnumValues 测试枚举值校验
func TestValidateSystemInfo_EnumValues(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		value    string
		wantErr  bool
	}{
		{"network_type合法-互联网", "network_type", "互联网", false},
		{"network_type合法-专网", "network_type", "专网", false},
		{"network_type非法", "network_type", "内网", true},
		{"run_status合法", "run_status", "正式运行", false},
		{"run_status非法", "run_status", "运行中", true},
		{"mobile_app_type合法", "mobile_app_type", "APP", false},
		{"mobile_app_type非法", "mobile_app_type", "应用", true},
		{"maintenance_mode合法", "maintenance_mode", "现场运维", false},
		{"maintenance_mode非法", "maintenance_mode", "混合运维", true},
		{"has_personal_info合法", "has_personal_info", "否", false},
		{"has_personal_info非法", "has_personal_info", "包含", true},
		{"backup_type合法", "backup_type", "无灾备", false},
		{"backup_type非法", "backup_type", "本地备份", true},
		{"security_level合法", "security_level", "二级", false},
		{"security_level非法", "security_level", "四级", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := completeSystemInfoData()
			data[tt.field] = tt.value

			err := validateSystemInfo(data)

			if tt.wantErr && err == nil {
				t.Errorf("字段 %s 值 '%s' 预期校验失败，实际通过", tt.field, tt.value)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("字段 %s 值 '%s' 预期通过，实际错误: %v", tt.field, tt.value, err)
			}
		})
	}
}

// TestValidateSystemInfo_MinimalData 测试最小合法数据
func TestValidateSystemInfo_MinimalData(t *testing.T) {
	data := map[string]interface{}{
		"system_name":             "测试系统",
		"deploy_location":         "机房",
		"network_name":            "内网",
		"network_type":            "专网",
		"run_status":              "正式运行",
		"build_time":              "2023-01-01",
		"has_media_platform":      "无",
		"mobile_app_type":         "否",
		"domain_or_ip":            "10.0.0.1",
		"subsystems":              "无",
		"has_external_interface":  "否",
		"interface_scope":         "无对接",
		"supervisory_dept":        "信息中心",
		"app_responsible_dept":    "信息中心",
		"network_responsible_dept": "网络部",
		"maintenance_mode":        "现场运维",
		"construction_dept":       "建设部",
		"system_contact":          "张三 123",
		"security_contact":        "李四 456",
		"admin_contact":           "王五 789",
		"maintenance_vendor":      "厂商A",
		"integration_vendor":      "厂商B",
		"development_vendor":      "厂商C",
		"data_content":            "测试数据",
		"data_storage_location":   "数据库",
		"has_personal_info":       "否",
		"backup_type":             "无灾备",
		"security_level":          "未定级",
		"security_record_no":      "无",
		"has_cloud_deploy":        "false",
	}

	err := validateSystemInfo(data)
	if err != nil {
		t.Errorf("最小合法数据预期通过，实际错误: %v", err)
	}
}

// TestValidateAssetData_TypeDispatch 测试类型分发
func TestValidateAssetData_TypeDispatch(t *testing.T) {
	// 空数据测试：只有 data 类型无必填字段，其他类型应报错
	tests := []struct {
		name    string
		assetType string
		data    map[string]interface{}
		wantErr bool
	}{
		{"system-info空数据", "system-info", map[string]interface{}{}, true},
		{"hardware空数据", "hardware", map[string]interface{}{}, true},
		{"data空数据", "data", map[string]interface{}{}, false}, // 无必填字段
		{"supply-chain空数据", "supply-chain", map[string]interface{}{}, true},
		{"vulnerability空数据", "vulnerability", map[string]interface{}{}, true},
		{"software-stat空数据", "software-stat", map[string]interface{}{}, true},
		{"responsible-dept空数据", "responsible-dept", map[string]interface{}{}, true},
		{"未知类型", "unknown", map[string]interface{}{}, false}, // 未知类型返回 nil
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAssetData(tt.assetType, tt.data)

			if tt.wantErr && err == nil {
				t.Errorf("类型 %s 空数据预期错误，实际通过", tt.assetType)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("类型 %s 预期通过，实际错误: %v", tt.assetType, err)
			}
		})
	}
}

// TestValidateHardware 测试硬件校验
func TestValidateHardware(t *testing.T) {
	t.Run("正常数据", func(t *testing.T) {
		data := map[string]interface{}{
			"asset_name":        "服务器",
			"category":          "服务器",
			"brand":             "华为",
			"model":             "RH2288H",
			"quantity":          2,
			"department":        "信息中心",
			"responsible_person": "张三",
			"location":          "机房",
			"use_status":        "在网",
			"mac_address":       "00:11:22:33:44:55",
			"os_version":        "CentOS 7.9",
			"start_use_date":    "2023-01-01",
			"asset_life":        "5年",
		}
		err := validateHardware(data)
		if err != nil {
			t.Errorf("预期通过，实际错误: %v", err)
		}
	})

	t.Run("数量小于1", func(t *testing.T) {
		data := map[string]interface{}{
			"asset_name":        "服务器",
			"category":          "服务器",
			"brand":             "华为",
			"model":             "RH2288H",
			"quantity":          0,
			"department":        "信息中心",
			"responsible_person": "张三",
			"location":          "机房",
			"use_status":        "在网",
			"mac_address":       "00:11:22:33:44:55",
			"os_version":        "CentOS 7.9",
			"start_use_date":    "2023-01-01",
			"asset_life":        "5年",
		}
		err := validateHardware(data)
		if err == nil {
			t.Error("数量为0时预期错误，实际通过")
		}
	})
}

// TestValidateVulnerability 测试漏洞校验
func TestValidateVulnerability(t *testing.T) {
	t.Run("正常数据", func(t *testing.T) {
		data := map[string]interface{}{
			"system_name":        "OA系统",
			"discovery_date":     "2023-06-01",
			"discovery_method":   "漏扫",
			"affected_device":    "Web服务器",
			"vulnerability_name": "SQL注入",
			"severity":           "高",
			"risk_description":   "存在SQL注入漏洞",
			"risk_impact":        "数据泄露风险",
			"completion_date":    "2023-07-01",
		}
		err := validateVulnerability(data)
		if err != nil {
			t.Errorf("预期通过，实际错误: %v", err)
		}
	})

	t.Run("severity枚举校验", func(t *testing.T) {
		data := map[string]interface{}{
			"system_name":        "OA系统",
			"discovery_date":     "2023-06-01",
			"discovery_method":   "漏扫",
			"affected_device":    "Web服务器",
			"vulnerability_name": "SQL注入",
			"severity":           "高",
			"risk_description":   "存在SQL注入漏洞",
			"risk_impact":        "数据泄露风险",
			"completion_date":    "2023-07-01",
		}
		err := validateVulnerability(data)
		if err != nil {
			t.Errorf("severity='高' 预期通过，实际错误: %v", err)
		}
	})

	t.Run("severity非法值", func(t *testing.T) {
		data := completeVulnerabilityData()
		data["severity"] = "未知"
		err := validateVulnerability(data)
		if err == nil {
			t.Error("severity='未知' 预期错误，实际通过")
		}
	})
}

// TestValidateSoftwareStat 测试软件统计校验
func TestValidateSoftwareStat(t *testing.T) {
	t.Run("年份范围合法", func(t *testing.T) {
		data := map[string]interface{}{
			"report_year":     2023,
			"department_name": "信息中心",
		}
		err := validateSoftwareStat(data)
		if err != nil {
			t.Errorf("预期通过，实际错误: %v", err)
		}
	})

	t.Run("年份太小", func(t *testing.T) {
		data := map[string]interface{}{
			"report_year":     1999,
			"department_name": "信息中心",
		}
		err := validateSoftwareStat(data)
		if err == nil {
			t.Error("年份=1999 预期错误，实际通过")
		}
	})

	t.Run("年份太大", func(t *testing.T) {
		data := map[string]interface{}{
			"report_year":     2101,
			"department_name": "信息中心",
		}
		err := validateSoftwareStat(data)
		if err == nil {
			t.Error("年份=2101 预期错误，实际通过")
		}
	})
}

// TestValidateSupplyChain 测试供应链校验
func TestValidateSupplyChain(t *testing.T) {
	t.Run("正常数据", func(t *testing.T) {
		data := map[string]interface{}{
			"system_name":     "OA系统",
			"supplier_type":   "运维服务商",
			"company_name":    "XX科技有限公司",
			"province_city":   "北京市海淀区",
			"address":         "中关村大街1号",
			"contact_person":  "张三",
			"contact_phone":   "13800138000",
			"service_content": "系统运维服务",
		}
		err := validateSupplyChain(data)
		if err != nil {
			t.Errorf("预期通过，实际错误: %v", err)
		}
	})

	t.Run("缺失必填字段", func(t *testing.T) {
		requiredFields := []struct {
			field string
			label string
		}{
			{"system_name", "系统名称"},
			{"supplier_type", "供应商类型"},
			{"company_name", "企业名称"},
			{"province_city", "省市"},
			{"address", "详细地址"},
			{"contact_person", "联系人"},
			{"contact_phone", "数据安全负责人联系电话"}, // 注意：全局字段标签冲突，实际使用data模块的标签
			{"service_content", "服务内容"},
		}

		for _, rf := range requiredFields {
			t.Run("缺失_"+rf.field, func(t *testing.T) {
				data := map[string]interface{}{
					"system_name":     "OA系统",
					"supplier_type":   "运维服务商",
					"company_name":    "XX公司",
					"province_city":   "北京",
					"address":         "地址",
					"contact_person":  "张三",
					"contact_phone":   "1380000",
					"service_content": "服务",
				}
				data[rf.field] = ""

				err := validateSupplyChain(data)
				if err == nil {
					t.Errorf("字段 %s 缺失时预期错误，实际通过", rf.field)
				} else {
					expectedMsg := "字段 " + rf.label + " 不能为空"
					if !containsStr(err.Error(), expectedMsg) {
						t.Errorf("预期错误 '%s'，实际 '%v'", expectedMsg, err)
					}
				}
			})
		}
	})
}

// TestValidateResponsibleDept 测试责任部门校验
func TestValidateResponsibleDept(t *testing.T) {
	t.Run("正常数据", func(t *testing.T) {
		data := map[string]interface{}{
			"department_name": "信息中心",
		}
		err := validateResponsibleDept(data)
		if err != nil {
			t.Errorf("预期通过，实际错误: %v", err)
		}
	})

	t.Run("缺失部门名称", func(t *testing.T) {
		data := map[string]interface{}{
			"department_name": "",
		}
		err := validateResponsibleDept(data)
		if err == nil {
			t.Error("部门名称缺失时预期错误，实际通过")
		}
	})

	t.Run("空对象", func(t *testing.T) {
		data := map[string]interface{}{}
		err := validateResponsibleDept(data)
		if err == nil {
			t.Error("空对象预期错误，实际通过")
		}
	})
}

// TestValidateData 测试数据资产校验
func TestValidateData(t *testing.T) {
	t.Run("空数据应通过", func(t *testing.T) {
		data := map[string]interface{}{}
		err := validateData(data)
		if err != nil {
			t.Errorf("data类型无必填字段，预期通过，实际错误: %v", err)
		}
	})

	t.Run("部分数据应通过", func(t *testing.T) {
		data := map[string]interface{}{
			"data_name": "用户数据",
			"data_size": 100,
		}
		err := validateData(data)
		if err != nil {
			t.Errorf("data类型无必填字段，预期通过，实际错误: %v", err)
		}
	})
}

// TestValidateHardware_MissingRequired 测试硬件缺失必填字段
func TestValidateHardware_MissingRequired(t *testing.T) {
	requiredFields := []struct {
		field string
		label string
	}{
		{"asset_name", "资产名称"},
		{"category", "类别名称"},
		{"brand", "品牌"},
		{"model", "品牌规格型号"},
		{"quantity", "数量"},
		{"department", "使用部门"},
		{"responsible_person", "责任人"},
		{"location", "存放地点/部署位置"},
		{"use_status", "使用状态"},
		{"mac_address", "MAC地址"},
		{"os_version", "操作系统名称及版本"},
		{"start_use_date", "开始使用日期"},
		{"asset_life", "资产使用期限"},
	}

	for _, rf := range requiredFields {
		t.Run("缺失_"+rf.field, func(t *testing.T) {
			data := completeHardwareData()
			data[rf.field] = ""

			err := validateHardware(data)
			if err == nil {
				t.Errorf("字段 %s 缺失时预期错误，实际通过", rf.field)
			} else {
				expectedMsg := "字段 " + rf.label + " 不能为空"
				if !containsStr(err.Error(), expectedMsg) {
					t.Errorf("预期错误 '%s'，实际 '%v'", expectedMsg, err)
				}
			}
		})
	}
}

// TestValidateHardware_EnumValues 测试硬件枚举值
func TestValidateHardware_EnumValues(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		value   string
		wantErr bool
	}{
		{"use_status合法-在网", "use_status", "在网", false},
		{"use_status合法-闲置", "use_status", "闲置", false},
		{"use_status非法", "use_status", "使用中", true},
		{"device_status合法", "device_status", "正常", false},
		{"device_status非法", "device_status", "损坏", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := completeHardwareData()
			data[tt.field] = tt.value

			err := validateHardware(data)

			if tt.wantErr && err == nil {
				t.Errorf("字段 %s 值 '%s' 预期校验失败，实际通过", tt.field, tt.value)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("字段 %s 值 '%s' 预期通过，实际错误: %v", tt.field, tt.value, err)
			}
		})
	}
}

// TestValidateHardware_QuantityTypes 测试数量字段类型
func TestValidateHardware_QuantityTypes(t *testing.T) {
	tests := []struct {
		name    string
		value   interface{}
		wantErr bool
	}{
		{"整数1", 1, false},
		{"整数100", 100, false},
		{"整数0", 0, true},
		{"整数-1", -1, true},
		{"浮点数1.0", 1.0, false},
		{"浮点数0.5", 0.5, true},
		{"字符串不校验", "abc", false}, // 非数字类型不触发数量校验
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := completeHardwareData()
			data["quantity"] = tt.value

			err := validateHardware(data)

			if tt.wantErr && err == nil {
				t.Errorf("数量 %v 预期错误，实际通过", tt.value)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("数量 %v 预期通过，实际错误: %v", tt.value, err)
			}
		})
	}
}

// TestValidateVulnerability_MissingRequired 测试漏洞缺失必填字段
func TestValidateVulnerability_MissingRequired(t *testing.T) {
	requiredFields := []struct {
		field string
		label string
	}{
		{"system_name", "系统名称"},
		{"discovery_date", "发现日期"},
		{"discovery_method", "发现方式"},
		{"affected_device", "涉及设备"},
		{"vulnerability_name", "漏洞名称"},
		{"severity", "漏洞等级"},
		{"risk_description", "风险描述"},
		{"risk_impact", "风险影响"},
		{"completion_date", "整改完成日期"},
	}

	for _, rf := range requiredFields {
		t.Run("缺失_"+rf.field, func(t *testing.T) {
			data := completeVulnerabilityData()
			data[rf.field] = ""

			err := validateVulnerability(data)
			if err == nil {
				t.Errorf("字段 %s 缺失时预期错误，实际通过", rf.field)
			} else {
				expectedMsg := "字段 " + rf.label + " 不能为空"
				if !containsStr(err.Error(), expectedMsg) {
					t.Errorf("预期错误 '%s'，实际 '%v'", expectedMsg, err)
				}
			}
		})
	}
}

// TestValidateVulnerability_EnumValues 测试漏洞枚举值
func TestValidateVulnerability_EnumValues(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		value   string
		wantErr bool
	}{
		{"severity合法-高", "severity", "高", false},
		{"severity合法-中", "severity", "中", false},
		{"severity合法-低", "severity", "低", false},
		{"severity非法", "severity", "未知", true},
		{"discovery_method合法-渗透", "discovery_method", "渗透", false},
		{"discovery_method合法-漏扫", "discovery_method", "漏扫", false},
		{"discovery_method合法-第三方通报", "discovery_method", "第三方通报", false},
		{"discovery_method非法", "discovery_method", "自动发现", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := completeVulnerabilityData()
			data[tt.field] = tt.value

			err := validateVulnerability(data)

			if tt.wantErr && err == nil {
				t.Errorf("字段 %s 值 '%s' 预期校验失败，实际通过", tt.field, tt.value)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("字段 %s 值 '%s' 预期通过，实际错误: %v", tt.field, tt.value, err)
			}
		})
	}
}

// TestValidateSoftwareStat_MissingRequired 测试软件统计缺失必填字段
func TestValidateSoftwareStat_MissingRequired(t *testing.T) {
	t.Run("缺失年份", func(t *testing.T) {
		data := map[string]interface{}{
			"department_name": "信息中心",
		}
		err := validateSoftwareStat(data)
		if err == nil {
			t.Error("缺失年份预期错误，实际通过")
		}
	})

	t.Run("缺失部门名称", func(t *testing.T) {
		data := map[string]interface{}{
			"report_year": 2023,
		}
		err := validateSoftwareStat(data)
		if err == nil {
			t.Error("缺失部门名称预期错误，实际通过")
		}
	})
}

// TestValidateSoftwareStat_YearTypes 测试年份字段类型
func TestValidateSoftwareStat_YearTypes(t *testing.T) {
	tests := []struct {
		name    string
		value   interface{}
		wantErr bool
	}{
		{"整数合法", 2023, false},
		{"整数太小", 1999, true},
		{"整数太大", 2101, true},
		{"浮点数合法", 2023.0, false},
		{"浮点数太小", 1999.5, true},
		{"边界值2000", 2000, false},
		{"边界值2100", 2100, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := map[string]interface{}{
				"report_year":     tt.value,
				"department_name": "信息中心",
			}
			err := validateSoftwareStat(data)

			if tt.wantErr && err == nil {
				t.Errorf("年份 %v 预期错误，实际通过", tt.value)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("年份 %v 预期通过，实际错误: %v", tt.value, err)
			}
		})
	}
}

// TestGetFieldLabel 测试字段标签获取
func TestGetFieldLabel(t *testing.T) {
	tests := []struct {
		field    string
		expected string
	}{
		{"system_name", "系统名称"},
		{"has_external_interface", "是否与外部系统对接"},
		{"asset_name", "资产名称"},
		{"use_status", "使用状态"},
		{"severity", "漏洞等级"},
		{"report_year", "统计年份"},
		{"unknown_field", "unknown_field"}, // 未定义字段返回原值
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			label := getFieldLabel(tt.field)
			if label != tt.expected {
				t.Errorf("字段 %s 预期标签 '%s'，实际 '%s'", tt.field, tt.expected, label)
			}
		})
	}
}

// completeHardwareData 返回完整的硬件测试数据
func completeHardwareData() map[string]interface{} {
	return map[string]interface{}{
		"asset_name":        "服务器",
		"category":          "服务器",
		"brand":             "华为",
		"model":             "RH2288H",
		"quantity":          2,
		"department":        "信息中心",
		"responsible_person": "张三",
		"location":          "机房",
		"use_status":        "在网",
		"mac_address":       "00:11:22:33:44:55",
		"os_version":        "CentOS 7.9",
		"start_use_date":    "2023-01-01",
		"asset_life":        "5年",
	}
}

// completeVulnerabilityData 返回完整的漏洞测试数据
func completeVulnerabilityData() map[string]interface{} {
	return map[string]interface{}{
		"system_name":        "OA系统",
		"discovery_date":     "2023-06-01",
		"discovery_method":   "漏扫",
		"affected_device":    "Web服务器",
		"vulnerability_name": "SQL注入",
		"severity":           "高",
		"risk_description":   "存在SQL注入漏洞",
		"risk_impact":        "数据泄露风险",
		"completion_date":    "2023-07-01",
	}
}

// completeSystemInfoData 返回完整的测试数据
func completeSystemInfoData() map[string]interface{} {
	return map[string]interface{}{
		"system_name":             "OA办公系统",
		"deploy_location":         "6层301中心机房",
		"network_name":            "政务内网",
		"network_type":            "专网",
		"run_status":              "正式运行",
		"build_time":              "2023-06-15",
		"has_media_platform":      "微信公众号",
		"mobile_app_type":         "小程序",
		"domain_or_ip":            "10.0.0.134",
		"subsystems":              "公文管理",
		"has_external_interface":  "是",
		"interface_scope":         "档案系统",
		"supervisory_dept":        "信息中心",
		"app_responsible_dept":    "信息中心",
		"network_responsible_dept": "网络运维部",
		"maintenance_mode":        "现场+远程运维",
		"construction_dept":       "信息化建设处",
		"system_contact":          "张三 152xxxx",
		"security_contact":        "李四 135xxxx",
		"admin_contact":           "王五 185xxxx",
		"maintenance_vendor":      "XX公司",
		"integration_vendor":      "YY公司",
		"development_vendor":      "ZZ公司",
		"data_content":            "组织数据",
		"data_storage_location":   "MySQL",
		"has_personal_info":       "是",
		"backup_type":             "数据灾备+系统灾备",
		"security_level":          "三级",
		"security_record_no":      "3301-2023-0001",
		"has_cloud_deploy":        "false",
	}
}

// containsStr 检查字符串是否包含子串
func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}