package handlers

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

type countQuery struct {
	key   string
	table string
	extra string
}

func statsScope(c *gin.Context) (string, []interface{}) {
	whereClause := "is_deleted = 0"
	args := []interface{}{}
	role, _ := c.Get("role")
	if role != "admin" {
		userID, _ := c.Get("user_id")
		whereClause += " AND created_by = ?"
		args = append(args, userID)
	}
	return whereClause, args
}

func queryCount(db *sql.DB, table, whereClause string, args ...interface{}) (int, error) {
	var count int
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", table, whereClause)
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func queryGroupedStats(db *sql.DB, table, field, whereClause, responseKey string, args ...interface{}) ([]gin.H, error) {
	query := fmt.Sprintf("SELECT %s, COUNT(*) FROM %s WHERE %s GROUP BY %s", field, table, whereClause, field)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]gin.H, 0)
	for rows.Next() {
		var value interface{}
		var count int
		if err := rows.Scan(&value, &count); err != nil {
			return nil, err
		}
		if raw, ok := value.([]byte); ok {
			value = string(raw)
		}
		result = append(result, gin.H{responseKey: value, "count": count})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func statsError(c *gin.Context) {
	c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "统计查询失败"})
}

// GetStatsSummary 获取统计概览。
func GetStatsSummary(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		whereClause, args := statsScope(c)
		queries := []countQuery{
			{key: "system_info_count", table: "system_info_assets"},
			{key: "hardware_count", table: "hardware_software_assets"},
			{key: "data_asset_count", table: "data_assets"},
			{key: "supply_chain_count", table: "supply_chain_assets"},
			{key: "vulnerability_count", table: "vulnerability_assets"},
			{key: "high_risk_vulns", table: "vulnerability_assets", extra: " AND severity = 0"},
		}

		data := gin.H{}
		for _, item := range queries {
			count, err := queryCount(db, item.table, whereClause+item.extra, args...)
			if err != nil {
				statsError(c)
				return
			}
			data[item.key] = count
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "data": data})
	}
}

// GetSystemInfoStats 获取信息系统清单统计。
func GetSystemInfoStats(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		whereClause, args := statsScope(c)
		runStatus, err := queryGroupedStats(db, "system_info_assets", "run_status", whereClause, "status", args...)
		if err != nil {
			statsError(c)
			return
		}
		networkType, err := queryGroupedStats(db, "system_info_assets", "network_type", whereClause, "type", args...)
		if err != nil {
			statsError(c)
			return
		}
		securityLevel, err := queryGroupedStats(db, "system_info_assets", "security_level", whereClause, "level", args...)
		if err != nil {
			statsError(c)
			return
		}

		c.JSON(http.StatusOK, gin.H{"code": 200, "data": gin.H{
			"run_status_stats":     runStatus,
			"network_type_stats":   networkType,
			"security_level_stats": securityLevel,
		}})
	}
}

// GetHardwareStats 获取信息化软硬件统计。
func GetHardwareStats(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		whereClause, args := statsScope(c)
		categories, err := queryGroupedStats(db, "hardware_software_assets", "category", whereClause, "category", args...)
		if err != nil {
			statsError(c)
			return
		}
		useStatus, err := queryGroupedStats(db, "hardware_software_assets", "use_status", whereClause, "status", args...)
		if err != nil {
			statsError(c)
			return
		}

		c.JSON(http.StatusOK, gin.H{"code": 200, "data": gin.H{
			"category_stats":   categories,
			"use_status_stats": useStatus,
		}})
	}
}

// GetVulnerabilityStats 获取风险漏洞统计。
func GetVulnerabilityStats(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		whereClause, args := statsScope(c)
		severity, err := queryGroupedStats(db, "vulnerability_assets", "severity", whereClause, "severity", args...)
		if err != nil {
			statsError(c)
			return
		}
		method, err := queryGroupedStats(db, "vulnerability_assets", "discovery_method", whereClause, "method", args...)
		if err != nil {
			statsError(c)
			return
		}

		c.JSON(http.StatusOK, gin.H{"code": 200, "data": gin.H{
			"severity_stats": severity,
			"method_stats":   method,
		}})
	}
}
