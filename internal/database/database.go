package database

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

func InitDB(dbPath string) (*sql.DB, error) {
	// 确保数据目录存在
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	// 打开SQLite数据库
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// 配置连接池
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// 启用WAL模式和外键
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}

	// 创建表结构
	if err := createTables(db); err != nil {
		return nil, err
	}

	// 创建默认管理员账号
	if err := createDefaultAdmin(db); err != nil {
		db.Close()
		return nil, err
	}

	log.Println("Database initialized successfully")
	return db, nil
}

func createTables(db *sql.DB) error {
	schemas := []string{
		// 用户表
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username VARCHAR(50) UNIQUE NOT NULL,
			password VARCHAR(255) NOT NULL,
			real_name VARCHAR(50),
			role VARCHAR(20) NOT NULL DEFAULT 'reporter',
			is_active BOOLEAN NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// 操作日志表
		`CREATE TABLE IF NOT EXISTS operation_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			action VARCHAR(50) NOT NULL,
			table_name VARCHAR(50) NOT NULL,
			record_id INTEGER,
			detail TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,

		// 信息系统清单表
		`CREATE TABLE IF NOT EXISTS system_info_assets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			system_name VARCHAR(200) NOT NULL,
			subsystems TEXT,
			deploy_location VARCHAR(200) NOT NULL,
			network_name VARCHAR(100) NOT NULL,
			network_type INTEGER NOT NULL,
			run_status INTEGER NOT NULL,
			build_time VARCHAR(20) NOT NULL,
			has_media_platform BOOLEAN DEFAULT 0,
			mobile_app_type INTEGER,
			domain_or_ip VARCHAR(200) NOT NULL,
			function_modules TEXT,
			has_external_interface BOOLEAN DEFAULT 0,
			interface_scope TEXT,
			supervisory_dept VARCHAR(100) NOT NULL,
			app_responsible_dept VARCHAR(100) NOT NULL,
			network_responsible_dept VARCHAR(100) NOT NULL,
			maintenance_mode INTEGER NOT NULL,
			construction_dept VARCHAR(100) NOT NULL,
			maintenance_vendor VARCHAR(100),
			integration_vendor VARCHAR(100),
			development_vendor VARCHAR(100),
			system_contact VARCHAR(100) NOT NULL,
			security_contact VARCHAR(100) NOT NULL,
			admin_contact VARCHAR(100) NOT NULL,
			data_content TEXT NOT NULL,
			has_personal_info BOOLEAN DEFAULT 0,
			important_data_risk VARCHAR(200),
			data_storage_location VARCHAR(100) NOT NULL,
			has_cloud_deploy BOOLEAN DEFAULT 0,
			cloud_provider VARCHAR(100),
			cloud_security_review INTEGER,
			security_level INTEGER NOT NULL,
			security_record_no VARCHAR(100),
			security_assessment INTEGER,
			crypto_assessment INTEGER,
			backup_type INTEGER NOT NULL,
			log_retention VARCHAR(50),
			security_devices TEXT,
			network_devices TEXT,
			os_info TEXT,
			database_info TEXT,
			middleware_info TEXT,
			dev_framework TEXT,
			third_party_components TEXT,
			computing_rental TEXT,
			remarks TEXT,
			created_by INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_deleted BOOLEAN DEFAULT 0,
			FOREIGN KEY (created_by) REFERENCES users(id)
		)`,

		// 信息化软硬件清单表
		`CREATE TABLE IF NOT EXISTS hardware_software_assets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_name VARCHAR(200) NOT NULL,
			category VARCHAR(100) NOT NULL,
			brand VARCHAR(100) NOT NULL,
			model VARCHAR(100) NOT NULL,
			quantity INTEGER NOT NULL DEFAULT 1,
			department VARCHAR(100) NOT NULL,
			responsible_person VARCHAR(50) NOT NULL,
			user VARCHAR(50),
			location VARCHAR(200) NOT NULL,
			use_status INTEGER NOT NULL,
			device_status INTEGER,
			network VARCHAR(100),
			ip_address VARCHAR(50),
			mac_address VARCHAR(50),
			os_version VARCHAR(100),
			start_use_date VARCHAR(20) NOT NULL,
			warranty_end_date VARCHAR(20),
			asset_life VARCHAR(50) NOT NULL,
			remarks TEXT,
			supplier VARCHAR(200),
			created_by INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_deleted BOOLEAN DEFAULT 0,
			FOREIGN KEY (created_by) REFERENCES users(id)
		)`,

		// 数据资产清单表
		`CREATE TABLE IF NOT EXISTS data_assets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_system VARCHAR(200),
			security_level INTEGER,
			is_critical_infra BOOLEAN DEFAULT 0,
			data_name VARCHAR(200),
			data_items TEXT,
			data_classification INTEGER,
			data_carrier VARCHAR(100),
			data_source INTEGER,
			data_size REAL,
			data_count INTEGER,
			processor_name VARCHAR(200),
			main_leader VARCHAR(50),
			security_leader VARCHAR(50),
			contact_phone VARCHAR(50),
			processing_purpose TEXT,
			usage_scope TEXT,
			sharing_scope TEXT,
			cross_border BOOLEAN DEFAULT 0,
			has_personal_info_elements BOOLEAN DEFAULT 0,
			personal_info_scale INTEGER,
			has_sensitive_personal BOOLEAN DEFAULT 0,
			has_name BOOLEAN DEFAULT 0,
			has_contact BOOLEAN DEFAULT 0,
			is_cross_border BOOLEAN DEFAULT 0,
			has_cross_border_assessment BOOLEAN DEFAULT 0,
			assessment_result VARCHAR(100),
			security_measures TEXT,
			remarks TEXT,
			created_by INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_deleted BOOLEAN DEFAULT 0,
			FOREIGN KEY (created_by) REFERENCES users(id)
		)`,

		// 供应链清单表
		`CREATE TABLE IF NOT EXISTS supply_chain_assets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			system_name VARCHAR(200) NOT NULL,
			supplier_type INTEGER NOT NULL,
			company_name VARCHAR(200) NOT NULL,
			province_city VARCHAR(100) NOT NULL,
			address VARCHAR(300) NOT NULL,
			contact_person VARCHAR(50) NOT NULL,
			contact_phone VARCHAR(50) NOT NULL,
			service_content TEXT NOT NULL,
			remarks TEXT,
			created_by INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_deleted BOOLEAN DEFAULT 0,
			FOREIGN KEY (created_by) REFERENCES users(id)
		)`,

		// 风险漏洞清单表
		`CREATE TABLE IF NOT EXISTS vulnerability_assets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			system_name VARCHAR(200) NOT NULL,
			discovery_date VARCHAR(20) NOT NULL,
			discovery_method INTEGER,
			affected_device TEXT NOT NULL,
			vulnerability_name VARCHAR(200) NOT NULL,
			cve_number VARCHAR(50),
			cnvd_number VARCHAR(50),
			domain VARCHAR(200),
			ip_address VARCHAR(50),
			severity INTEGER,
			protocol VARCHAR(20),
			port INTEGER,
			vuln_type VARCHAR(100),
			risk_description TEXT NOT NULL,
			risk_impact TEXT NOT NULL,
			remediation_suggestion TEXT,
			remediation_measure TEXT,
			completion_date VARCHAR(20),
			created_by INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_deleted BOOLEAN DEFAULT 0,
			FOREIGN KEY (created_by) REFERENCES users(id)
		)`,

		// 软件信息统计表
		`CREATE TABLE IF NOT EXISTS software_statistics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			report_year INT NOT NULL,
			department_name VARCHAR(100) NOT NULL,
			department_head VARCHAR(50),
			head_phone VARCHAR(50),
			department_fax VARCHAR(50),
			registration_date VARCHAR(20),
			is_legalization_done BOOLEAN DEFAULT 0,
			total_staff_count INT,
			computer_user_count INT,
			server_count INT,
			desktop_count INT,
			laptop_count INT,
			pur_os_dom_lic INT,
			pur_os_dom_amt REAL,
			pur_os_for_lic INT,
			pur_os_for_amt REAL,
			pur_office_dom_lic INT,
			pur_office_dom_amt REAL,
			pur_office_for_lic INT,
			pur_office_for_amt REAL,
			pur_av_dom_lic INT,
			pur_av_dom_amt REAL,
			pur_av_for_lic INT,
			pur_av_for_amt REAL,
			cum_os_dom_lic INT,
			cum_os_for_lic INT,
			cum_office_dom_lic INT,
			cum_office_for_lic INT,
			cum_av_dom_lic INT,
			cum_av_for_lic INT,
			modification_log TEXT,
			created_by INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_deleted BOOLEAN DEFAULT 0,
			FOREIGN KEY (created_by) REFERENCES users(id)
		)`,

		// 责任部门表
		`CREATE TABLE IF NOT EXISTS responsible_departments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			department_name VARCHAR(100) NOT NULL,
			department_code VARCHAR(50),
			department_head VARCHAR(50),
			head_phone VARCHAR(50),
			department_fax VARCHAR(50),
			remarks TEXT,
			created_by INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_deleted BOOLEAN DEFAULT 0,
			FOREIGN KEY (created_by) REFERENCES users(id)
		)`,
	}

	for _, schema := range schemas {
		if _, err := db.Exec(schema); err != nil {
			return err
		}
	}

	return nil
}

func bootstrapAdminPassword() (string, bool, error) {
	if configured := strings.TrimSpace(os.Getenv("ADMIN_PASSWORD")); len(configured) >= 12 {
		return configured, false, nil
	}
	passwordBytes := make([]byte, 18)
	if _, err := rand.Read(passwordBytes); err != nil {
		return "", false, fmt.Errorf("generate bootstrap admin password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(passwordBytes), true, nil
}

func createDefaultAdmin(db *sql.DB) error {
	var storedHash string
	queryErr := db.QueryRow("SELECT password FROM users WHERE username = ?", "admin").Scan(&storedHash)
	if queryErr != nil && queryErr != sql.ErrNoRows {
		return fmt.Errorf("check admin user: %w", queryErr)
	}

	password, generated, err := bootstrapAdminPassword()
	if err != nil {
		return err
	}
	if os.Getenv("ADMIN_PASSWORD") != "" && len(strings.TrimSpace(os.Getenv("ADMIN_PASSWORD"))) < 12 {
		log.Println("WARNING: ADMIN_PASSWORD短于12字符，已忽略并生成随机临时密码")
	}

	if queryErr == sql.ErrNoRows {
		hashedPassword, hashErr := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if hashErr != nil {
			return fmt.Errorf("hash admin password: %w", hashErr)
		}
		if _, insertErr := db.Exec(
			"INSERT INTO users (username, password, real_name, role, is_active) VALUES (?, ?, ?, ?, ?)",
			"admin", string(hashedPassword), "系统管理员", "admin", true,
		); insertErr != nil {
			return fmt.Errorf("create admin user: %w", insertErr)
		}
		if generated {
			log.Printf("首次启动管理员已创建，用户名admin，临时密码：%s（请登录后立即修改）", password)
		} else {
			log.Println("首次启动管理员已使用ADMIN_PASSWORD创建，用户名admin")
		}
		return nil
	}

	// 自动淘汰历史固定默认口令。仅当数据库仍使用admin123时执行一次。
	if bcrypt.CompareHashAndPassword([]byte(storedHash), []byte("admin123")) == nil {
		hashedPassword, hashErr := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if hashErr != nil {
			return fmt.Errorf("hash replacement admin password: %w", hashErr)
		}
		if _, updateErr := db.Exec("UPDATE users SET password = ?, updated_at = CURRENT_TIMESTAMP WHERE username = ?", string(hashedPassword), "admin"); updateErr != nil {
			return fmt.Errorf("replace insecure admin password: %w", updateErr)
		}
		if generated {
			log.Printf("检测到历史默认管理员口令，已轮换为临时密码：%s（请登录后立即修改）", password)
		} else {
			log.Println("检测到历史默认管理员口令，已轮换为ADMIN_PASSWORD")
		}
	}
	return nil
}
