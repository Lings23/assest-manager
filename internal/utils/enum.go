package utils

// EnumMapping 枚举映射配置
// 管理所有资产类型的枚举字段映射关系

// enumMappings 预置的枚举映射数据
// key: assetType.fieldName, value: code→text映射
var enumMappings = map[string]map[int]string{
	// system-info (信息系统清单)
	"system-info.run_status": {
		0: "正式运行",
		1: "试运行",
		2: "在建",
		3: "临时下线",
		4: "停用",
	},
	"system-info.network_type": {
		0: "互联网",
		1: "专网",
		2: "互联网+专网",
	},
	"system-info.mobile_app_type": {
		0: "否",
		1: "APP",
		2: "小程序",
		3: "快应用",
		4: "其他",
	},
	"system-info.maintenance_mode": {
		0: "现场运维",
		1: "远程运维",
		2: "现场+远程运维",
	},
	"system-info.backup_type": {
		0: "数据灾备",
		1: "系统灾备",
		2: "数据灾备+系统灾备",
		3: "无灾备",
	},
	"system-info.security_level": {
		0: "一级",
		1: "二级",
		2: "三级",
		3: "未定级",
	},
	"system-info.security_assessment": {
		0: "",
		1: "符合",
		2: "基本符合",
		3: "不符合",
	},
	"system-info.crypto_assessment": {
		0: "",
		1: "符合",
		2: "基本符合",
		3: "不符合",
	},
	"system-info.cloud_security_review": {
		0: "",
		1: "通过",
		2: "未通过",
		3: "未参加",
	},

	// hardware (信息化软硬件清单)
	"hardware.use_status": {
		0: "在网",
		1: "不在网",
		2: "闲置",
		3: "报废",
	},
	"hardware.device_status": {
		0: "正常",
		1: "故障",
		2: "维修中",
	},

	// data (数据资产清单)
	"data.security_level": {
		0: "一级",
		1: "二级",
		2: "三级",
	},
	"data.data_classification": {
		0: "",
		1: "重要数据",
		2: "一般3级",
		3: "一般2级",
		4: "一般1级",
	},
	"data.data_source": {
		0: "共享交换",
		1: "人工填报",
	},

	// supply-chain (供应链清单)
	"supply-chain.supplier_type": {
		0: "设计方",
		1: "开发方",
		2: "承建方",
		3: "网络安全产品提供方",
		4: "信息化产品提供方",
		5: "运维方",
		6: "安全服务提供方",
		7: "信息安全评测方",
		8: "其他参与方",
	},

	// vulnerability (风险漏洞清单)
	"vulnerability.severity": {
		0: "高",
		1: "中",
		2: "低",
	},
	"vulnerability.discovery_method": {
		0: "渗透",
		1: "漏扫",
		2: "第三方通报",
	},
}

// textToCodeMappings 文本到编码的反向映射
var textToCodeMappings = buildTextToCodeMappings()

func buildTextToCodeMappings() map[string]map[string]int {
	result := make(map[string]map[string]int)
	for key, codeMap := range enumMappings {
		textMap := make(map[string]int)
		for code, text := range codeMap {
			textMap[text] = code
		}
		result[key] = textMap
	}
	return result
}

// GetEnumMapping 获取字段枚举映射 (code→text)
func GetEnumMapping(assetType, fieldName string) map[int]string {
	key := assetType + "." + fieldName
	if mapping, ok := enumMappings[key]; ok {
		return mapping
	}
	return nil
}

// CodeToText 数字编码转文本
func CodeToText(assetType, fieldName string, code int) string {
	key := assetType + "." + fieldName
	if mapping, ok := enumMappings[key]; ok {
		if text, exists := mapping[code]; exists {
			return text
		}
	}
	return ""
}

// TextToCode 文本转数字编码
// 返回 -1 表示无法识别的文本
func TextToCode(assetType, fieldName string, text string) int {
	key := assetType + "." + fieldName
	if mapping, ok := textToCodeMappings[key]; ok {
		if code, exists := mapping[text]; exists {
			return code
		}
	}
	return -1
}

// GetEnumFieldNames 获取指定资产类型的所有枚举字段名
func GetEnumFieldNames(assetType string) []string {
	fields := []string{}
	for key := range enumMappings {
		if len(key) > len(assetType)+1 && key[:len(assetType)] == assetType && key[len(assetType)] == '.' {
			fields = append(fields, key[len(assetType)+1:])
		}
	}
	return fields
}

// IsEnumField 判断字段是否是枚举字段
func IsEnumField(assetType, fieldName string) bool {
	key := assetType + "." + fieldName
	return enumMappings[key] != nil
}