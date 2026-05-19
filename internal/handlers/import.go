package handlers

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// ImportAsset 通用资产导入
func ImportAsset(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetType := c.Param("type")
		config := getAssetConfig(assetType)
		if config == nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "无效的资产类型"})
			return
		}

		userID, _ := c.Get("user_id")

		// 获取上传的文件
		file, err := c.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "请上传文件"})
			return
		}

		// 打开文件
		src, err := file.Open()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "打开文件失败"})
			return
		}
		defer src.Close()

		// 读取全部内容
		content, err := io.ReadAll(src)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "读取文件失败"})
			return
		}

		// 检测并转换编码：如果不是有效的UTF-8，尝试从GBK转换
		if !isValidUTF8(content) {
			decoder := simplifiedchinese.GBK.NewDecoder()
			converted, convErr := decoder.Bytes(content)
			if convErr == nil {
				content = converted
			}
		}

		// 移除BOM头
		content = bytes.TrimPrefix(content, []byte{0xEF, 0xBB, 0xBF})

		// 解析CSV
		reader := csv.NewReader(bytes.NewReader(content))
		reader.LazyQuotes = true
		reader.TrimLeadingSpace = true

		// 读取表头
		headers, err := reader.Read()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "文件格式错误"})
			return
		}

		// 获取资产配置中的字段
		columns := getAssetColumns(assetType)

		// 验证表头
		if !validateHeaders(headers, columns) {
			// 提供更详细的错误信息
			errDetail := fmt.Sprintf("表头格式错误：期望%d列，实际%d列。请下载模板后按照模板格式填写", len(columns), len(headers))
			if len(headers) == len(columns) {
				// 列数匹配但内容不匹配，找出第一个不匹配的列
				for i, header := range headers {
					headerName := header
					if len(header) > 0 && header[len(header)-1] == '*' {
						headerName = header[:len(header)-1]
					}
					if headerName != columns[i].name {
						errDetail = fmt.Sprintf("表头第%d列错误：期望'%s'，实际'%s'。请下载模板后按照模板格式填写", i+1, columns[i].name, headerName)
						break
					}
				}
			}
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    400,
				"message": errDetail,
			})
			return
		}

		// 读取并验证数据
		var successCount int
		var errorCount int
		var errors []string
		now := time.Now().Format("2006-01-02 15:04:05")

		lineNum := 1
		for {
			lineNum++
			record, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				errors = append(errors, fmt.Sprintf("第%d行：读取失败", lineNum))
				errorCount++
				continue
			}

			// 验证必填字段
			if errMsg := validateRequiredFields(record, columns, lineNum); errMsg != "" {
				errors = append(errors, errMsg)
				errorCount++
				continue
			}

			// 构建插入语句
			columnsForInsert := []string{}
			values := []interface{}{}
			placeholders := []string{}

			for i, col := range columns {
				if col.required && i < len(record) && record[i] == "" {
					errors = append(errors, fmt.Sprintf("第%d行：字段%s不能为空", lineNum, col.name))
					errorCount++
					continue
				}
				if i < len(record) && record[i] != "" {
					columnsForInsert = append(columnsForInsert, col.dbColumn)
					values = append(values, record[i])
					placeholders = append(placeholders, "?")
				}
			}

			// 添加系统字段
			columnsForInsert = append(columnsForInsert, "created_by", "created_at", "updated_at")
			values = append(values, userID, now, now)
			placeholders = append(placeholders, "?", "?", "?")

			// 执行插入
			query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
				config.TableName,
				joinStrings(columnsForInsert, ", "),
				joinStrings(placeholders, ", "))

			_, err = db.Exec(query, values...)
			if err != nil {
				errors = append(errors, fmt.Sprintf("第%d行：插入失败 - %s", lineNum, err.Error()))
				errorCount++
				continue
			}

			successCount++
		}

		// 返回结果
		result := gin.H{
			"code":          200,
			"message":       fmt.Sprintf("导入完成：成功%d条，失败%d条", successCount, errorCount),
			"success_count": successCount,
			"error_count":   errorCount,
		}

		if len(errors) > 0 {
			result["errors"] = errors
		}

		c.JSON(http.StatusOK, result)
	}
}

// DownloadTemplate 下载导入模板
func DownloadTemplate(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetType := c.Param("type")
		config := getAssetConfig(assetType)
		if config == nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "无效的资产类型"})
			return
		}

		columns := getAssetColumns(assetType)

		// 设置响应头
		filename := fmt.Sprintf("%s_导入模板.csv", config.TypeName)
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))

		// 写入BOM头，使Excel正确识别UTF-8
		c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})

		// 写入表头
		headerNames := []string{}
		for _, col := range columns {
			if col.required {
				headerNames = append(headerNames, col.name+"*")
			} else {
				headerNames = append(headerNames, col.name)
			}
		}
		writer := csv.NewWriter(c.Writer)
		writer.Write(headerNames)

		// 写入示例数据
		exampleRow := getExampleData(assetType)
		writer.Write(exampleRow)
		writer.Flush()
	}
}

// ColumnInfo 列信息
type ColumnInfo struct {
	name      string
	dbColumn  string
	required  bool
	fieldType string
}

// getAssetColumns 获取资产类型的字段配置
func getAssetColumns(assetType string) []ColumnInfo {
	switch assetType {
	case "system-info":
		return []ColumnInfo{
			// 基本信息 (8字段)
			{"系统名称", "system_name", true, "text"},
			{"部署地点", "deploy_location", true, "text"},
			{"网络名称", "network_name", true, "text"},
			{"网络类型", "network_type", true, "select"},
			{"运行状态", "run_status", true, "select"},
			{"建成时间", "build_time", true, "date"},
			{"政务新媒体平台", "has_media_platform", true, "text"},
			{"移动互联网应用", "mobile_app_type", true, "select"},
			// 网络与对接信息 (5字段)
			{"域名或IP", "domain_or_ip", true, "text"},
			{"子系统", "subsystems", true, "text"},
			{"功能模块", "function_modules", false, "text"},
			{"是否外部对接", "has_external_interface", true, "select"},
			{"对接范围", "interface_scope", true, "text"},
			// 责任部门与人员 (11字段)
			{"主管部门", "supervisory_dept", true, "text"},
			{"应用责任部门", "app_responsible_dept", true, "text"},
			{"网络责任部门", "network_responsible_dept", true, "text"},
			{"运维模式", "maintenance_mode", true, "select"},
			{"建设部门", "construction_dept", true, "text"},
			{"系统责任人", "system_contact", true, "text"},
			{"安全管理员", "security_contact", true, "text"},
			{"系统管理员", "admin_contact", true, "text"},
			{"运维厂商", "maintenance_vendor", true, "text"},
			{"集成厂商", "integration_vendor", true, "text"},
			{"开发厂商", "development_vendor", true, "text"},
			// 数据与安全信息 (4字段)
			{"数据内容", "data_content", true, "text"},
			{"存储位置", "data_storage_location", true, "text"},
			{"是否含个人信息", "has_personal_info", true, "select"},
			{"重要数据风险评估", "important_data_risk", false, "text"},
			// 备份情况 (2字段)
			{"备份类型", "backup_type", true, "select"},
			{"日志留存", "log_retention", false, "text"},
			// 等保和密评情况 (4字段)
			{"等保级别", "security_level", true, "select"},
			{"等保备案号", "security_record_no", true, "text"},
			{"等保测评情况", "security_assessment", false, "select"},
			{"密评情况", "crypto_assessment", false, "select"},
			// 云服务情况 (3字段)
			{"是否云部署", "has_cloud_deploy", true, "select"},
			{"云服务商", "cloud_provider", false, "text"},
			{"云安全审查", "cloud_security_review", false, "select"},
			// 供应链情况 (8字段)
			{"安全设备", "security_devices", false, "text"},
			{"网络设备", "network_devices", false, "text"},
			{"操作系统", "os_info", false, "text"},
			{"数据库", "database_info", false, "text"},
			{"中间件", "middleware_info", false, "text"},
			{"开发框架", "dev_framework", false, "text"},
			{"第三方组件", "third_party_components", false, "text"},
			{"算力租赁", "computing_rental", false, "text"},
			// 备注 (1字段)
			{"备注", "remarks", false, "text"},
		}
	case "hardware":
		return []ColumnInfo{
			// 基本信息 (7字段)
			{"资产名称", "asset_name", true, "text"},
			{"类别名称", "category", true, "text"},
			{"品牌", "brand", true, "text"},
			{"品牌规格型号", "model", true, "text"},
			{"数量", "quantity", true, "number"},
			{"使用部门", "department", true, "text"},
			{"供应商全称", "supplier", false, "text"},
			// 人员与位置信息 (6字段)
			{"责任人", "responsible_person", true, "text"},
			{"使用人", "user", false, "text"},
			{"存放地点/部署位置", "location", true, "text"},
			{"使用状态", "use_status", true, "select"},
			{"设备状态", "device_status", false, "select"},
			{"运行网络", "network", false, "text"},
			// 网络与系统信息 (3字段)
			{"IP地址", "ip_address", false, "text"},
			{"MAC地址", "mac_address", true, "text"},
			{"操作系统名称及版本", "os_version", true, "text"},
			// 时间信息 (3字段)
			{"开始使用日期", "start_use_date", true, "date"},
			{"保修截止日期", "warranty_end_date", false, "date"},
			{"资产使用期限", "asset_life", true, "text"},
			// 备注信息 (1字段)
			{"备注", "remarks", false, "text"},
		}
	case "data":
		return []ColumnInfo{
			// 网络安全等保和关键信息基础设施安全保护情况 (3字段)
			{"数据来源信息系统名称", "source_system", false, "text"},
			{"数据来源信息系统等保级别", "security_level", false, "select"},
			{"是否关键信息基础设施", "is_critical_infra", false, "select"},
			// 数据基本情况 (7字段)
			{"数据名称", "data_name", false, "text"},
			{"数据项", "data_items", false, "text"},
			{"数据级别", "data_classification", false, "select"},
			{"数据载体", "data_carrier", false, "text"},
			{"数据来源", "data_source", false, "select"},
			{"数据规模(GB)", "data_size", false, "number"},
			{"数据条数", "data_count", false, "number"},
			// 责任人员 (4字段)
			{"数据处理者名称", "processor_name", false, "text"},
			{"主要负责人", "main_leader", false, "text"},
			{"数据安全负责人姓名", "security_leader", false, "text"},
			{"数据安全负责人联系电话", "contact_phone", false, "text"},
			// 数据处理情况 (6字段)
			{"数据处理目的", "processing_purpose", false, "text"},
			{"数据使用范围", "usage_scope", false, "text"},
			{"数据共享范围和方式", "sharing_scope", false, "text"},
			{"数据是否出境", "is_cross_border", false, "select"},
			{"是否开展数据出境安全评估", "has_cross_border_assessment", false, "select"},
			{"数据出境安全评估结果", "assessment_result", false, "text"},
			// 个人信息基本情况 (3字段)
			{"包含个人信息要素", "has_personal_info_elements", false, "select"},
			{"个人信息规模（人）", "personal_info_scale", false, "number"},
			{"是否包含敏感个人信息", "has_sensitive_personal", false, "select"},
			// 安全措施 (2字段)
			{"数据安全防护措施", "security_measures", false, "text"},
			{"备注", "remarks", false, "text"},
		}
	case "supply-chain":
		return []ColumnInfo{
			{"系统名称", "system_name", true, "text"},
			{"供应商类型", "supplier_type", true, "select"},
			{"企业名称", "company_name", true, "text"},
			{"省市", "province_city", true, "text"},
			{"详细地址", "address", true, "text"},
			{"联系人", "contact_person", true, "text"},
			{"联系电话", "contact_phone", true, "text"},
			{"服务内容", "service_content", true, "text"},
			{"备注", "remarks", false, "text"},
		}
	case "vulnerability":
		return []ColumnInfo{
			{"系统名称", "system_name", true, "text"},
			{"漏洞名称", "vulnerability_name", true, "text"},
			{"发现日期", "discovery_date", true, "date"},
			{"发现方式", "discovery_method", false, "select"},
			{"涉及设备", "affected_device", true, "text"},
			{"漏洞等级", "severity", false, "select"},
			{"风险描述", "risk_description", true, "text"},
			{"风险影响", "risk_impact", true, "text"},
			{"整改建议", "remediation_suggestion", false, "text"},
			{"CVE编号", "cve_number", false, "text"},
			{"CNVD编号", "cnvd_number", false, "text"},
			{"域名", "domain", false, "text"},
			{"IP地址", "ip_address", false, "text"},
			{"协议", "protocol", false, "text"},
			{"端口", "port", false, "number"},
			{"漏洞类型", "vuln_type", false, "text"},
			{"整改措施", "remediation_measure", false, "text"},
			{"整改完成时间", "completion_date", false, "date"},
		}
	case "software-stat":
		return []ColumnInfo{
			// 基本信息
			{"统计年份", "report_year", true, "number"},
			{"责任部门名称", "department_name", true, "text"},
			{"部门负责人", "department_head", false, "text"},
			{"负责人电话", "head_phone", false, "text"},
			{"部门传真", "department_fax", false, "text"},
			{"填报日期", "registration_date", false, "date"},
			{"是否完成正版化", "is_legalization_done", false, "boolean"},

			// 人员设备统计
			{"单位总人数", "total_staff_count", false, "number"},
			{"计算机使用人数", "computer_user_count", false, "number"},
			{"服务器数量", "server_count", false, "number"},
			{"台式电脑数量", "desktop_count", false, "number"},
			{"笔记本电脑数量", "laptop_count", false, "number"},

			// 采购单项 (当年)
			{"操作系统(国产)采购数量", "pur_os_dom_lic", false, "number"},
			{"操作系统(国产)采购金额", "pur_os_dom_amt", false, "number"},
			{"操作系统(非国产)采购数量", "pur_os_for_lic", false, "number"},
			{"操作系统(非国产)采购金额", "pur_os_for_amt", false, "number"},

			{"办公软件(国产)采购数量", "pur_office_dom_lic", false, "number"},
			{"办公软件(国产)采购金额", "pur_office_dom_amt", false, "number"},
			{"办公软件(非国产)采购数量", "pur_office_for_lic", false, "number"},
			{"办公软件(非国产)采购金额", "pur_office_for_amt", false, "number"},

			{"杀毒软件(国产)采购数量", "pur_av_dom_lic", false, "number"},
			{"杀毒软件(国产)采购金额", "pur_av_dom_amt", false, "number"},
			{"杀毒软件(非国产)采购数量", "pur_av_for_lic", false, "number"},
			{"杀毒软件(非国产)采购金额", "pur_av_for_amt", false, "number"},

			// 累计授权项
			{"操作系统(国产)累计数量", "cum_os_dom_lic", false, "number"},
			{"操作系统(非国产)累计数量", "cum_os_for_lic", false, "number"},

			{"办公软件(国产)累计数量", "cum_office_dom_lic", false, "number"},
			{"办公软件(非国产)累计数量", "cum_office_for_lic", false, "number"},

			{"杀毒软件(国产)累计数量", "cum_av_dom_lic", false, "number"},
			{"杀毒软件(非国产)累计数量", "cum_av_for_lic", false, "number"},

			// 日志与审计
			{"修改日志", "modification_log", false, "text"},
		}
	default:
		return []ColumnInfo{}
	}
}

// validateHeaders 验证表头
func validateHeaders(headers []string, columns []ColumnInfo) bool {
	if len(headers) != len(columns) {
		return false
	}
	for i, header := range headers {
		// 移除*号后比较
		headerName := header
		if len(header) > 0 && header[len(header)-1] == '*' {
			headerName = header[:len(header)-1]
		}
		if headerName != columns[i].name {
			return false
		}
	}
	return true
}

// validateRequiredFields 验证必填字段
func validateRequiredFields(record []string, columns []ColumnInfo, lineNum int) string {
	for i, col := range columns {
		if col.required && (i >= len(record) || record[i] == "") {
			return fmt.Sprintf("第%d行：字段%s不能为空", lineNum, col.name)
		}
	}
	return ""
}

// getExampleData 获取示例数据
func getExampleData(assetType string) []string {
	switch assetType {
	case "system-info":
		return []string{
			// 基本信息 (8字段)
			"OA系统", "6层301中心机房", "办公网", "专网", "正式运行",
			"2024-01-15", "无", "否",
			// 网络与对接信息 (5字段)
			"10.0.0.134", "人事系统", "公告、组织架构", "否", "无",
			// 责任部门与人员 (11字段)
			"办公室", "办公室", "信息部", "现场运维", "办公室",
			"张三152xxxx", "李四135xxxx", "王五185xxxx", "XX科技公司", "XX集成商", "XX开发商",
			// 数据与安全信息 (4字段)
			"组织数据、公文数据", "数据库", "否", "",
			// 备份情况 (2字段)
			"数据灾备", "6个月",
			// 等保和密评情况 (4字段)
			"二级", "备案号12345", "符合", "符合",
			// 云服务情况 (3字段)
			"false", "", "",
			// 供应链情况 (8字段)
			"防火墙|华为|2", "交换机|华为|5", "CentOS 7.9 x3", "MySQL 8.0 x2",
			"Nginx 1.20", "Spring Boot", "Redis 6.2", "",
			// 备注 (1字段)
			"",
		}
	case "hardware":
		return []string{
			// 基本信息 (7字段)
			"接入交换机", "交换机", "华为", "S5720S", "2", "科技信息部", "华为技术有限公司",
			// 人员与位置信息 (6字段)
			"张三", "张三", "核心机房", "在网", "正常", "办公网",
			// 网络与系统信息 (3字段)
			"192.168.1.134", "3F-54-13-3D-F2-AE", "VRP V200R",
			// 时间信息 (3字段)
			"2024-01-15", "2025-01-15", "永久",
			// 备注信息 (1字段)
			"无",
		}
	case "data":
		return []string{
			// 网络安全等保和关键信息基础设施安全保护情况 (3字段)
			"××系统", "二级", "false",
			// 数据基本情况 (7字段)
			"出租车辆信息", "车牌号、运营证编号", "一般3级", "数据库", "共享交换", "86.52", "9712",
			// 责任人员 (4字段)
			"××市××单位", "张三", "李四", "18512345678",
			// 数据处理情况 (6字段)
			"营运车辆数据管理", "××市交通管理部门", "共享交管部门", "false", "false", "",
			// 个人信息基本情况 (3字段)
			"true", "5000", "false",
			// 安全措施 (2字段)
			"加密存储、定期异地备份", "无",
		}
	case "supply-chain":
		return []string{
			"OA系统", "运维方", "××科技有限公司", "×省×市", "×市×区×号",
			"张三", "13800000000", "系统运维服务", "无",
		}
	case "vulnerability":
		return []string{
			"×机房", "SSL RC4加密套件支持检测", "2025-02-28", "漏洞扫描",
			"网络安全设备", "中", "远程主机支持RC4...", "攻击者可利用密文推测明文",
			"重新配置", "CVE-2015-2808", "——", "abcd.efg.com", "123.456.789.100",
			"TCP", "3389", "信息泄露", "检测拦截", "2025-03-07",
		}
	case "software-stat":
		return []string{
			"2026", "信息技术部", "张三", "13800000000", "010-12345678", "2026-05-11", "true",
			"500", "480", "10", "400", "100",
			"50", "25000", "0", "0",
			"50", "15000", "0", "0",
			"500", "50000", "0", "0",
			"400", "100", "400", "100", "500", "0",
			"初始导入导入测试数据",
		}
	default:
		return []string{}
	}
}

// ExportAssetToCSV 导出资产为CSV（带BOM头）
func ExportAssetToCSV(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetType := c.Param("type")
		config := getAssetConfig(assetType)
		if config == nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "无效的资产类型"})
			return
		}

		userID, _ := c.Get("user_id")
		role, _ := c.Get("role")

		whereClause := "is_deleted = 0"
		args := []interface{}{}

		if role != "admin" {
			whereClause += " AND created_by = ?"
			args = append(args, userID)
		}

		querySQL := fmt.Sprintf("SELECT * FROM %s WHERE %s ORDER BY created_at DESC", config.TableName, whereClause)

		rows, err := db.Query(querySQL, args...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "查询失败"})
			return
		}
		defer rows.Close()

		columns, _ := rows.Columns()

		// 设置响应头，带BOM头
		c.Header("Content-Type", "text/csv; charset=utf-8")
		filename := fmt.Sprintf("%s_%s.csv", config.TypeName, time.Now().Format("20060102_150405"))
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))

		// 写入BOM头
		c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})

		writer := csv.NewWriter(c.Writer)

		// 写入表头
		writer.Write(columns)

		// 写入数据
		for rows.Next() {
			values := make([]interface{}, len(columns))
			valuePtrs := make([]interface{}, len(columns))
			for i := range values {
				valuePtrs[i] = &values[i]
			}
			rows.Scan(valuePtrs...)

			strValues := []string{}
			for _, val := range values {
				if val == nil {
					strValues = append(strValues, "")
				} else if b, ok := val.([]byte); ok {
					strValues = append(strValues, string(b))
				} else {
					strValues = append(strValues, fmt.Sprintf("%v", val))
				}
			}
			writer.Write(strValues)
		}
		writer.Flush()
	}
}

// isValidUTF8 检查字节序列是否是有效的UTF-8编码
func isValidUTF8(data []byte) bool {
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			return false
		}
		i += size
	}
	return true
}