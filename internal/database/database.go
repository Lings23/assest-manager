package database

import (
	"database/sql"
	"log"
	"os"

	_ "modernc.org/sqlite"
	"golang.org/x/crypto/bcrypt"
)

func InitDB(dbPath string) (*sql.DB, error) {
	// 确保数据目录存在
	dir := dbPath[:len(dbPath)-len("/assets.db")]
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	// 打开SQLite数据库
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// 配置连接池
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	// 启用WAL模式和外键
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA foreign_keys=ON")

	// 创建表结构
	if err := createTables(db); err != nil {
		return nil, err
	}

	// 创建默认管理员账号
	createDefaultAdmin(db)

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
			network_type VARCHAR(50) NOT NULL,
			run_status VARCHAR(50) NOT NULL,
			build_time VARCHAR(20) NOT NULL,
			has_media_platform BOOLEAN DEFAULT 0,
			mobile_app_type VARCHAR(50),
			domain_or_ip VARCHAR(200) NOT NULL,
			function_modules TEXT,
			has_external_interface BOOLEAN DEFAULT 0,
			interface_scope TEXT,
			supervisory_dept VARCHAR(100) NOT NULL,
			app_responsible_dept VARCHAR(100) NOT NULL,
			network_responsible_dept VARCHAR(100) NOT NULL,
			maintenance_mode VARCHAR(50) NOT NULL,
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
			cloud_security_review VARCHAR(50),
			security_level VARCHAR(50) NOT NULL,
			security_record_no VARCHAR(100),
			security_assessment VARCHAR(50),
			crypto_assessment VARCHAR(50),
			backup_type VARCHAR(50) NOT NULL,
			log_retention VARCHAR(50) NOT NULL,
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
			use_status VARCHAR(50) NOT NULL,
			device_status VARCHAR(50) NOT NULL,
			network VARCHAR(100) NOT NULL,
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
			source_system VARCHAR(200) NOT NULL,
			security_level VARCHAR(50) NOT NULL,
			is_critical_infra BOOLEAN DEFAULT 0,
			data_name VARCHAR(200) NOT NULL,
			data_items TEXT NOT NULL,
			data_classification VARCHAR(50) NOT NULL,
			data_carrier VARCHAR(100) NOT NULL,
			data_source VARCHAR(50) NOT NULL,
			data_size REAL,
			data_count INTEGER,
			processor_name VARCHAR(200) NOT NULL,
			main_leader VARCHAR(50) NOT NULL,
			security_leader VARCHAR(50) NOT NULL,
			contact_phone VARCHAR(50) NOT NULL,
			processing_purpose TEXT NOT NULL,
			usage_scope TEXT NOT NULL,
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
			security_measures TEXT NOT NULL,
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
			supplier_type VARCHAR(50) NOT NULL,
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
			discovery_method VARCHAR(50) NOT NULL,
			affected_device TEXT NOT NULL,
			vulnerability_name VARCHAR(200) NOT NULL,
			cve_number VARCHAR(50),
			cnvd_number VARCHAR(50),
			domain VARCHAR(200),
			ip_address VARCHAR(50),
			severity VARCHAR(20) NOT NULL,
			protocol VARCHAR(20),
			port INTEGER,
			vuln_type VARCHAR(100),
			risk_description TEXT NOT NULL,
			risk_impact TEXT NOT NULL,
			remediation_suggestion TEXT NOT NULL,
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

func createDefaultAdmin(db *sql.DB) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", "admin").Scan(&count)
	if err != nil {
		log.Printf("Failed to check admin user: %v", err)
		return
	}

	if count == 0 {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
		if err != nil {
			log.Printf("Failed to hash admin password: %v", err)
			return
		}

		_, err = db.Exec(
			"INSERT INTO users (username, password, real_name, role, is_active) VALUES (?, ?, ?, ?, ?)",
			"admin", string(hashedPassword), "系统管理员", "admin", true,
		)
		if err != nil {
			log.Printf("Failed to create default admin: %v", err)
		} else {
			log.Println("Default admin user created (username: admin, password: admin123)")
		}
	}
}