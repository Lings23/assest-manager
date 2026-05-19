package handlers

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

// GetStatsSummary 获取统计概览
func GetStatsSummary(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _ := c.Get("user_id")
		role, _ := c.Get("role")

		whereClause := "is_deleted = 0"
		args := []interface{}{}

		if role != "admin" {
			whereClause += " AND created_by = ?"
			args = append(args, userID)
		}

		// 各表单总数
		var systemInfoCount, hardwareCount, dataAssetCount, supplyChainCount, vulnCount int

		db.QueryRow("SELECT COUNT(*) FROM system_info_assets WHERE "+whereClause, args...).Scan(&systemInfoCount)
		db.QueryRow("SELECT COUNT(*) FROM hardware_software_assets WHERE "+whereClause, args...).Scan(&hardwareCount)
		db.QueryRow("SELECT COUNT(*) FROM data_assets WHERE "+whereClause, args...).Scan(&dataAssetCount)
		db.QueryRow("SELECT COUNT(*) FROM supply_chain_assets WHERE "+whereClause, args...).Scan(&supplyChainCount)
		db.QueryRow("SELECT COUNT(*) FROM vulnerability_assets WHERE "+whereClause, args...).Scan(&vulnCount)

		// 高风险漏洞数
		var highRiskVulns int
		vulnArgs := append([]interface{}{}, args...)
		db.QueryRow("SELECT COUNT(*) FROM vulnerability_assets WHERE is_deleted = 0 AND severity = '高'", vulnArgs...).Scan(&highRiskVulns)

		c.JSON(http.StatusOK, gin.H{
			"code": 200,
			"data": gin.H{
				"system_info_count":   systemInfoCount,
				"hardware_count":      hardwareCount,
				"data_asset_count":    dataAssetCount,
				"supply_chain_count":  supplyChainCount,
				"vulnerability_count": vulnCount,
				"high_risk_vulns":     highRiskVulns,
			},
		})
	}
}

// GetSystemInfoStats 获取信息系统清单统计
func GetSystemInfoStats(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _ := c.Get("user_id")
		role, _ := c.Get("role")

		whereClause := "is_deleted = 0"
		args := []interface{}{}

		if role != "admin" {
			whereClause += " AND created_by = ?"
			args = append(args, userID)
		}

		// 按运行状态统计
		runStatusRows, _ := db.Query(
			"SELECT run_status, COUNT(*) as count FROM system_info_assets WHERE "+whereClause+" GROUP BY run_status",
			args...,
		)
		defer runStatusRows.Close()

		runStatusStats := []gin.H{}
		for runStatusRows.Next() {
			var status string
			var count int
			runStatusRows.Scan(&status, &count)
			runStatusStats = append(runStatusStats, gin.H{"status": status, "count": count})
		}

		// 按网络类型统计
		networkTypeRows, _ := db.Query(
			"SELECT network_type, COUNT(*) as count FROM system_info_assets WHERE "+whereClause+" GROUP BY network_type",
			args...,
		)
		defer networkTypeRows.Close()

		networkTypeStats := []gin.H{}
		for networkTypeRows.Next() {
			var ntype string
			var count int
			networkTypeRows.Scan(&ntype, &count)
			networkTypeStats = append(networkTypeStats, gin.H{"type": ntype, "count": count})
		}

		// 按等保级别统计
		securityLevelRows, _ := db.Query(
			"SELECT security_level, COUNT(*) as count FROM system_info_assets WHERE "+whereClause+" GROUP BY security_level",
			args...,
		)
		defer securityLevelRows.Close()

		securityLevelStats := []gin.H{}
		for securityLevelRows.Next() {
			var level string
			var count int
			securityLevelRows.Scan(&level, &count)
			securityLevelStats = append(securityLevelStats, gin.H{"level": level, "count": count})
		}

		c.JSON(http.StatusOK, gin.H{
			"code": 200,
			"data": gin.H{
				"run_status_stats":    runStatusStats,
				"network_type_stats":  networkTypeStats,
				"security_level_stats": securityLevelStats,
			},
		})
	}
}

// GetHardwareStats 获取信息化软硬件统计
func GetHardwareStats(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _ := c.Get("user_id")
		role, _ := c.Get("role")

		whereClause := "is_deleted = 0"
		args := []interface{}{}

		if role != "admin" {
			whereClause += " AND created_by = ?"
			args = append(args, userID)
		}

		// 按类别统计
		categoryRows, _ := db.Query(
			"SELECT category, COUNT(*) as count FROM hardware_software_assets WHERE "+whereClause+" GROUP BY category",
			args...,
		)
		defer categoryRows.Close()

		categoryStats := []gin.H{}
		for categoryRows.Next() {
			var category string
			var count int
			categoryRows.Scan(&category, &count)
			categoryStats = append(categoryStats, gin.H{"category": category, "count": count})
		}

		// 按使用状态统计
		useStatusRows, _ := db.Query(
			"SELECT use_status, COUNT(*) as count FROM hardware_software_assets WHERE "+whereClause+" GROUP BY use_status",
			args...,
		)
		defer useStatusRows.Close()

		useStatusStats := []gin.H{}
		for useStatusRows.Next() {
			var status string
			var count int
			useStatusRows.Scan(&status, &count)
			useStatusStats = append(useStatusStats, gin.H{"status": status, "count": count})
		}

		c.JSON(http.StatusOK, gin.H{
			"code": 200,
			"data": gin.H{
				"category_stats":   categoryStats,
				"use_status_stats": useStatusStats,
			},
		})
	}
}

// GetVulnerabilityStats 获取风险漏洞统计
func GetVulnerabilityStats(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _ := c.Get("user_id")
		role, _ := c.Get("role")

		whereClause := "is_deleted = 0"
		args := []interface{}{}

		if role != "admin" {
			whereClause += " AND created_by = ?"
			args = append(args, userID)
		}

		// 按漏洞等级统计
		severityRows, _ := db.Query(
			"SELECT severity, COUNT(*) as count FROM vulnerability_assets WHERE "+whereClause+" GROUP BY severity",
			args...,
		)
		defer severityRows.Close()

		severityStats := []gin.H{}
		for severityRows.Next() {
			var severity string
			var count int
			severityRows.Scan(&severity, &count)
			severityStats = append(severityStats, gin.H{"severity": severity, "count": count})
		}

		// 按发现方式统计
		methodRows, _ := db.Query(
			"SELECT discovery_method, COUNT(*) as count FROM vulnerability_assets WHERE "+whereClause+" GROUP BY discovery_method",
			args...,
		)
		defer methodRows.Close()

		methodStats := []gin.H{}
		for methodRows.Next() {
			var method string
			var count int
			methodRows.Scan(&method, &count)
			methodStats = append(methodStats, gin.H{"method": method, "count": count})
		}

		c.JSON(http.StatusOK, gin.H{
			"code": 200,
			"data": gin.H{
				"severity_stats": severityStats,
				"method_stats":   methodStats,
			},
		})
	}
}
