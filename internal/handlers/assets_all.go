package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// booleanFieldMappings 定义需要布尔转换的字段（按资产类型）
var booleanFieldMappings = map[string][]string{
	"system-info": {
		"has_external_interface",
		"has_personal_info",
		"has_cloud_deploy",
		"has_media_platform",
	},
	"data": {
		"is_critical_infra",
		"has_personal_info_elements",
		"has_sensitive_personal",
		"is_cross_border",
		"has_cross_border_assessment",
		"cross_border",
	},
	"hardware":        {},
	"supply-chain":    {},
	"vulnerability":   {},
	"software-stat":   {"is_legalization_done"},
	"responsible-dept": {},
}

// convertBooleanFields 将"是"/"否"字符串转换为布尔值
func convertBooleanFields(assetType string, data map[string]interface{}) {
	fields, ok := booleanFieldMappings[assetType]
	if !ok {
		return
	}

	for _, field := range fields {
		if val, exists := data[field]; exists {
			switch v := val.(type) {
			case string:
				if v == "是" {
					data[field] = true
				} else if v == "否" {
					data[field] = false
				}
			}
		}
	}
}

// convertBooleanToText 将数据库中的布尔值(0/1)转换为"是"/"否"文本显示
func convertBooleanToText(assetType string, data map[string]interface{}) {
	fields, ok := booleanFieldMappings[assetType]
	if !ok {
		return
	}

	for _, field := range fields {
		if val, exists := data[field]; exists {
			// 先转换为 bool
			var boolVal bool
			switch v := val.(type) {
			case bool:
				boolVal = v
			case int64:
				boolVal = v == 1
			case int:
				boolVal = v == 1
			case float64:
				boolVal = v == 1
			default:
				continue
			}
			// 将布尔值转为 "是"/"否"
			if boolVal {
				data[field] = "是"
			} else {
				data[field] = "否"
			}
		}
	}
}

// PaginatedResponse 分页响应结构
type PaginatedResponse struct {
	Data     interface{} `json:"data"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

// AssetConfig 资产配置结构
type AssetConfig struct {
	TableName string
	TypeName  string
}

// getAssetConfig 获取资产配置
func getAssetConfig(assetType string) *AssetConfig {
	configs := map[string]*AssetConfig{
		"system-info":      {TableName: "system_info_assets", TypeName: "信息系统清单"},
		"hardware":         {TableName: "hardware_software_assets", TypeName: "信息化软硬件清单"},
		"data":             {TableName: "data_assets", TypeName: "数据资产清单"},
		"supply-chain":     {TableName: "supply_chain_assets", TypeName: "供应链清单"},
		"vulnerability":    {TableName: "vulnerability_assets", TypeName: "风险漏洞清单"},
		"software-stat":    {TableName: "software_statistics", TypeName: "软件信息统计"},
		"responsible-dept": {TableName: "responsible_departments", TypeName: "责任部门"},
	}
	return configs[assetType]
}

// ListAssets 通用资产列表查询
func ListAssets(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetType := c.Param("type")
		config := getAssetConfig(assetType)
		if config == nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "无效的资产类型"})
			return
		}

		userID, _ := c.Get("user_id")
		role, _ := c.Get("role")

		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
		search := c.Query("search")

		if page < 1 {
			page = 1
		}
		if pageSize < 1 || pageSize > 100 {
			pageSize = 20
		}

		offset := (page - 1) * pageSize

		whereClause := "is_deleted = 0"
		args := []interface{}{}

		if role != "admin" {
			whereClause += " AND created_by = ?"
			args = append(args, userID)
		}

		if search != "" {
			searchPattern := "%" + search + "%"
			// 根据不同表使用不同的搜索字段
			switch config.TableName {
			case "system_info_assets":
				whereClause += " AND (system_name LIKE ? OR deploy_location LIKE ?)"
				args = append(args, searchPattern, searchPattern)
			case "hardware_software_assets":
				whereClause += " AND (asset_name LIKE ? OR brand LIKE ? OR category LIKE ?)"
				args = append(args, searchPattern, searchPattern, searchPattern)
			case "data_assets":
				whereClause += " AND (data_name LIKE ? OR source_system LIKE ?)"
				args = append(args, searchPattern, searchPattern)
			case "supply_chain_assets":
				whereClause += " AND (company_name LIKE ? OR system_name LIKE ?)"
				args = append(args, searchPattern, searchPattern)
			case "vulnerability_assets":
				whereClause += " AND (vulnerability_name LIKE ? OR system_name LIKE ?)"
				args = append(args, searchPattern, searchPattern)
			case "software_statistics":
				whereClause += " AND (department_name LIKE ? OR department_head LIKE ?)"
				args = append(args, searchPattern, searchPattern)
			case "responsible_departments":
				whereClause += " AND (department_name LIKE ? OR department_head LIKE ?)"
				args = append(args, searchPattern, searchPattern)
			}
		}

		var total int
		countSQL := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", config.TableName, whereClause)
		db.QueryRow(countSQL, args...).Scan(&total)

		querySQL := fmt.Sprintf("SELECT * FROM %s WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?", config.TableName, whereClause)
		args = append(args, pageSize, offset)

		rows, err := db.Query(querySQL, args...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "查询失败"})
			return
		}
		defer rows.Close()

		columns, _ := rows.Columns()
		var assets []map[string]interface{}

		for rows.Next() {
			values := make([]interface{}, len(columns))
			valuePtrs := make([]interface{}, len(columns))
			for i := range values {
				valuePtrs[i] = &values[i]
			}

			if err := rows.Scan(valuePtrs...); err != nil {
				continue
			}

			asset := make(map[string]interface{})
			for i, col := range columns {
				val := values[i]
				if b, ok := val.([]byte); ok {
					asset[col] = string(b)
				} else {
					asset[col] = val
				}
			}
			// 将布尔字段的 0/1 转换为 "是"/"否"
			convertBooleanToText(assetType, asset)
			assets = append(assets, asset)
		}

		c.JSON(http.StatusOK, gin.H{
			"code": 200,
			"data": PaginatedResponse{
				Data:     assets,
				Total:    total,
				Page:     page,
				PageSize: pageSize,
			},
		})
	}
}

// GetAsset 通用资产详情查询
func GetAsset(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetType := c.Param("type")
		id := c.Param("id")
		config := getAssetConfig(assetType)
		if config == nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "无效的资产类型"})
			return
		}

		userID, _ := c.Get("user_id")
		role, _ := c.Get("role")

		query := fmt.Sprintf("SELECT * FROM %s WHERE id = ? AND is_deleted = 0", config.TableName)
		args := []interface{}{id}

		if role != "admin" {
			query += " AND created_by = ?"
			args = append(args, userID)
		}

		// 先获取列名
		colQuery := fmt.Sprintf("SELECT * FROM %s LIMIT 0", config.TableName)
		colRows, err := db.Query(colQuery)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "查询失败"})
			return
		}
		columns, _ := colRows.Columns()
		colRows.Close()

		// 查询数据
		row := db.QueryRow(query, args...)

		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := row.Scan(valuePtrs...); err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "记录不存在"})
			return
		} else if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "查询失败"})
			return
		}

		asset := make(map[string]interface{})
		for i, col := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok {
				asset[col] = string(b)
			} else {
				asset[col] = val
			}
		}


		// 将布尔字段的 0/1 转换为 "是"/"否"
		convertBooleanToText(assetType, asset)
		c.JSON(http.StatusOK, gin.H{"code": 200, "data": asset})
	}
}

// CreateAsset 通用资产创建
func CreateAsset(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetType := c.Param("type")
		config := getAssetConfig(assetType)
		if config == nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "无效的资产类型"})
			return
		}

		userID, _ := c.Get("user_id")

		var data map[string]interface{}
		if err := c.ShouldBindJSON(&data); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "请求参数错误"})
			return
		}

		// 转换布尔字段（将"是"/"否"转为 true/false）
		convertBooleanFields(assetType, data)
		// 数据校验
		if err := ValidateAssetData(assetType, data); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
			return
		}

		// 构建插入语句
		columns := []string{}
		values := []interface{}{}
		placeholders := []string{}

		for key, value := range data {
			if key == "created_by" || key == "created_at" || key == "updated_at" {
				continue
			}
			columns = append(columns, key)
			values = append(values, value)
			placeholders = append(placeholders, "?")
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		columns = append(columns, "created_by", "created_at", "updated_at")
		values = append(values, userID, now, now)
		placeholders = append(placeholders, "?", "?", "?")

		query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
			config.TableName,
			joinStrings(columns, ", "),
			joinStrings(placeholders, ", "))

		result, err := db.Exec(query, values...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 500, "message": "创建失败", "error": err.Error(),
			})
			return
		}

		newID, _ := result.LastInsertId()
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "创建成功", "data": gin.H{"id": newID}})
	}
}

// UpdateAsset 通用资产更新
func UpdateAsset(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetType := c.Param("type")
		id := c.Param("id")
		config := getAssetConfig(assetType)
		if config == nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "无效的资产类型"})
			return
		}

		userID, _ := c.Get("user_id")
		role, _ := c.Get("role")

		// 检查权限
		if role != "admin" {
			var createdBy uint
			db.QueryRow(fmt.Sprintf("SELECT created_by FROM %s WHERE id = ? AND is_deleted = 0", config.TableName), id).Scan(&createdBy)
			if createdBy != userID.(uint) {
				c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "无权限修改此记录"})
				return
			}
		}

		var data map[string]interface{}
		if err := c.ShouldBindJSON(&data); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "请求参数错误"})
			return
		}

		// 转换布尔字段（将"是"/"否"转为 true/false）
		convertBooleanFields(assetType, data)

		// 数据校验（更新时校验提交的字段）
		if err := ValidateAssetData(assetType, data); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
			return
		}

		// 构建更新语句
		setParts := []string{}
		values := []interface{}{}

		for key, value := range data {
			if key == "id" || key == "created_by" || key == "created_at" {
				continue
			}
			setParts = append(setParts, fmt.Sprintf("%s = ?", key))
			values = append(values, value)
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		setParts = append(setParts, "updated_at = ?")
		values = append(values, now)
		values = append(values, id)

		query := fmt.Sprintf("UPDATE %s SET %s WHERE id = ?", config.TableName, joinStrings(setParts, ", "))

		_, err := db.Exec(query, values...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "更新失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "更新成功"})
	}
}

// DeleteAsset 通用资产删除(软删除)
func DeleteAsset(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetType := c.Param("type")
		id := c.Param("id")
		config := getAssetConfig(assetType)
		if config == nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "无效的资产类型"})
			return
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		query := fmt.Sprintf("UPDATE %s SET is_deleted = 1, updated_at = ? WHERE id = ?", config.TableName)

		_, err := db.Exec(query, now, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "删除失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "删除成功"})
	}
}

// ExportAsset 通用资产导出
func ExportAsset(db *sql.DB) gin.HandlerFunc {
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

		c.Header("Content-Type", "text/csv; charset=utf-8")
		filename := fmt.Sprintf("%s_%s.csv", config.TypeName, time.Now().Format("20060102_150405"))
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))

		// 写入表头
		header := joinStrings(columns, ",") + "\n"
		c.Writer.WriteString(header)

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
			c.Writer.WriteString(joinStrings(strValues, ",") + "\n")
		}
	}
}

// CalculateCumulative 计算软件信息统计累计值
// 累计值 = 该部门登记日期之前所有采购值之和 + 当前采购值
func CalculateCumulative(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		departmentName := c.Query("department_name")
		registrationDate := c.Query("registration_date")

		if departmentName == "" || registrationDate == "" {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "缺少部门名称或登记日期"})
			return
		}

		// 获取当前采购值（可选）
		var currentPurchase map[string]interface{}
		if c.Request.Method == "POST" {
			if err := c.ShouldBindJSON(&currentPurchase); err != nil {
				currentPurchase = make(map[string]interface{})
			}
		} else {
			currentPurchase = make(map[string]interface{})
		}

		// 查询该部门登记日期之前的采购值总和
		query := `
			SELECT
				COALESCE(SUM(pur_os_dom_lic), 0) as prev_os_dom_lic,
				COALESCE(SUM(pur_os_for_lic), 0) as prev_os_for_lic,
				COALESCE(SUM(pur_office_dom_lic), 0) as prev_office_dom_lic,
				COALESCE(SUM(pur_office_for_lic), 0) as prev_office_for_lic,
				COALESCE(SUM(pur_av_dom_lic), 0) as prev_av_dom_lic,
				COALESCE(SUM(pur_av_for_lic), 0) as prev_av_for_lic
			FROM software_statistics
			WHERE department_name = ?
			  AND registration_date < ?
			  AND is_deleted = 0`

		var prevOsDomLic, prevOsForLic, prevOfficeDomLic, prevOfficeForLic, prevAvDomLic, prevAvForLic int

		err := db.QueryRow(query, departmentName, registrationDate).Scan(
			&prevOsDomLic, &prevOsForLic,
			&prevOfficeDomLic, &prevOfficeForLic,
			&prevAvDomLic, &prevAvForLic,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "查询失败"})
			return
		}

		// 加上当前采购值
		result := map[string]int{
			"cum_os_dom_lic":     prevOsDomLic + getInt(currentPurchase, "pur_os_dom_lic"),
			"cum_os_for_lic":     prevOsForLic + getInt(currentPurchase, "pur_os_for_lic"),
			"cum_office_dom_lic": prevOfficeDomLic + getInt(currentPurchase, "pur_office_dom_lic"),
			"cum_office_for_lic": prevOfficeForLic + getInt(currentPurchase, "pur_office_for_lic"),
			"cum_av_dom_lic":     prevAvDomLic + getInt(currentPurchase, "pur_av_dom_lic"),
			"cum_av_for_lic":     prevAvForLic + getInt(currentPurchase, "pur_av_for_lic"),
		}

		c.JSON(http.StatusOK, gin.H{"code": 200, "data": result})
	}
}

// getInt 从map中获取整数值
func getInt(m map[string]interface{}, key string) int {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case int:
			return v
		case float64:
			return int(v)
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
	}
	return 0
}

// joinStrings 连接字符串切片
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}