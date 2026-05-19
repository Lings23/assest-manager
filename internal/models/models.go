package models

import (
	"time"
)

// User 用户模型
type User struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Username  string    `json:"username" gorm:"uniqueIndex;size:50;not null"`
	Password  string    `json:"-" gorm:"size:255;not null"`
	RealName  string    `json:"real_name" gorm:"size:50"`
	Role      string    `json:"role" gorm:"size:20;not null"` // admin, reporter
	IsActive  bool      `json:"is_active" gorm:"default:true"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OperationLog 操作日志模型
type OperationLog struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	UserID    uint      `json:"user_id" gorm:"not null"`
	Action    string    `json:"action" gorm:"size:50;not null"` // create, update, delete
	TableName string    `json:"table_name" gorm:"size:50;not null"`
	RecordID  *uint     `json:"record_id"`
	Detail    string    `json:"detail" gorm:"type:text"`
	CreatedAt time.Time `json:"created_at"`
}

// SystemInfoAsset 信息系统清单
type SystemInfoAsset struct {
	ID                     uint      `json:"id" gorm:"primaryKey"`
	SystemName             string    `json:"system_name" gorm:"size:200;not null"`
	Subsystems             string    `json:"subsystems" gorm:"type:text"`
	DeployLocation         string    `json:"deploy_location" gorm:"size:200;not null"`
	NetworkName            string    `json:"network_name" gorm:"size:100;not null"`
	NetworkType            string    `json:"network_type" gorm:"size:50;not null"`
	RunStatus              string    `json:"run_status" gorm:"size:50;not null"`
	BuildTime              string    `json:"build_time" gorm:"size:20;not null"`
	HasMediaPlatform       bool      `json:"has_media_platform" gorm:"default:false"`
	MobileAppType          string    `json:"mobile_app_type" gorm:"size:50"`
	DomainOrIP             string    `json:"domain_or_ip" gorm:"size:200;not null"`
	FunctionModules        string    `json:"function_modules" gorm:"type:text"`
	HasExternalInterface   bool      `json:"has_external_interface" gorm:"default:false"`
	InterfaceScope         string    `json:"interface_scope" gorm:"type:text"`
	SupervisoryDept        string    `json:"supervisory_dept" gorm:"size:100;not null"`
	AppResponsibleDept     string    `json:"app_responsible_dept" gorm:"size:100;not null"`
	NetworkResponsibleDept string    `json:"network_responsible_dept" gorm:"size:100;not null"`
	MaintenanceMode        string    `json:"maintenance_mode" gorm:"size:50;not null"`
	ConstructionDept       string    `json:"construction_dept" gorm:"size:100;not null"`
	MaintenanceVendor      string    `json:"maintenance_vendor" gorm:"size:100"`
	IntegrationVendor      string    `json:"integration_vendor" gorm:"size:100"`
	DevelopmentVendor      string    `json:"development_vendor" gorm:"size:100"`
	SystemContact          string    `json:"system_contact" gorm:"size:100;not null"`
	SecurityContact        string    `json:"security_contact" gorm:"size:100;not null"`
	AdminContact           string    `json:"admin_contact" gorm:"size:100;not null"`
	DataContent            string    `json:"data_content" gorm:"type:text;not null"`
	HasPersonalInfo        bool      `json:"has_personal_info" gorm:"default:false"`
	ImportantDataRisk      string    `json:"important_data_risk" gorm:"size:200"`
	DataStorageLocation    string    `json:"data_storage_location" gorm:"size:100;not null"`
	HasCloudDeploy         bool      `json:"has_cloud_deploy" gorm:"default:false"`
	CloudProvider          string    `json:"cloud_provider" gorm:"size:100"`
	CloudSecurityReview    string    `json:"cloud_security_review" gorm:"size:50"`
	SecurityLevel          string    `json:"security_level" gorm:"size:50;not null"`
	SecurityRecordNo       string    `json:"security_record_no" gorm:"size:100"`
	SecurityAssessment     string    `json:"security_assessment" gorm:"size:50"`
	CryptoAssessment       string    `json:"crypto_assessment" gorm:"size:50"`
	BackupType             string    `json:"backup_type" gorm:"size:50;not null"`
	LogRetention           string    `json:"log_retention" gorm:"size:50;not null"`
	SecurityDevices        string    `json:"security_devices" gorm:"type:text"`
	NetworkDevices         string    `json:"network_devices" gorm:"type:text"`
	OsInfo                 string    `json:"os_info" gorm:"type:text"`
	DatabaseInfo           string    `json:"database_info" gorm:"type:text"`
	MiddlewareInfo         string    `json:"middleware_info" gorm:"type:text"`
	DevFramework           string    `json:"dev_framework" gorm:"type:text"`
	ThirdPartyComponents   string    `json:"third_party_components" gorm:"type:text"`
	ComputingRental        string    `json:"computing_rental" gorm:"type:text"`
	Remarks                string    `json:"remarks" gorm:"type:text"`
	CreatedBy              uint      `json:"created_by" gorm:"not null"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	IsDeleted              bool      `json:"is_deleted" gorm:"default:false;index"`
}

// HardwareSoftwareAsset 信息化软硬件清单
type HardwareSoftwareAsset struct {
	ID               uint      `json:"id" gorm:"primaryKey"`
	AssetName        string    `json:"asset_name" gorm:"size:200;not null"`
	Category         string    `json:"category" gorm:"size:100;not null"`
	Brand            string    `json:"brand" gorm:"size:100;not null"`
	Model            string    `json:"model" gorm:"size:100;not null"`
	Quantity         int       `json:"quantity" gorm:"not null"`
	Department       string    `json:"department" gorm:"size:100;not null"`
	ResponsiblePerson string   `json:"responsible_person" gorm:"size:50;not null"`
	User             string    `json:"user" gorm:"size:50"`
	Location         string    `json:"location" gorm:"size:200;not null"`
	UseStatus        string    `json:"use_status" gorm:"size:50;not null"`
	DeviceStatus     string    `json:"device_status" gorm:"size:50;not null"`
	Network          string    `json:"network" gorm:"size:100;not null"`
	IPAddress        string    `json:"ip_address" gorm:"size:50"`
	MACAddress       string    `json:"mac_address" gorm:"size:50"`
	OSVersion        string    `json:"os_version" gorm:"size:100"`
	StartUseDate     string    `json:"start_use_date" gorm:"size:20;not null"`
	WarrantyEndDate  string    `json:"warranty_end_date" gorm:"size:20"`
	AssetLife        string    `json:"asset_life" gorm:"size:50;not null"`
	Remarks          string    `json:"remarks" gorm:"type:text"`
	Supplier         string    `json:"supplier" gorm:"size:200"`
	CreatedBy        uint      `json:"created_by" gorm:"not null"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	IsDeleted        bool      `json:"is_deleted" gorm:"default:false;index"`
}

// DataAsset 数据资产清单
type DataAsset struct {
	ID                        uint      `json:"id" gorm:"primaryKey"`
	SourceSystem              string    `json:"source_system" gorm:"size:200;not null"`
	SecurityLevel             string    `json:"security_level" gorm:"size:50;not null"`
	IsCriticalInfra           bool      `json:"is_critical_infra" gorm:"default:false"`
	DataName                  string    `json:"data_name" gorm:"size:200;not null"`
	DataItems                 string    `json:"data_items" gorm:"type:text;not null"`
	DataClassification        string    `json:"data_classification" gorm:"size:50;not null"`
	DataCarrier               string    `json:"data_carrier" gorm:"size:100;not null"`
	DataSource                string    `json:"data_source" gorm:"size:50;not null"`
	DataSize                  float64   `json:"data_size"`
	DataCount                 int       `json:"data_count"`
	ProcessorName             string    `json:"processor_name" gorm:"size:200;not null"`
	MainLeader                string    `json:"main_leader" gorm:"size:50;not null"`
	SecurityLeader            string    `json:"security_leader" gorm:"size:50;not null"`
	ContactPhone              string    `json:"contact_phone" gorm:"size:50;not null"`
	ProcessingPurpose         string    `json:"processing_purpose" gorm:"type:text;not null"`
	UsageScope                string    `json:"usage_scope" gorm:"type:text;not null"`
	SharingScope              string    `json:"sharing_scope" gorm:"type:text"`
	CrossBorder               bool      `json:"cross_border" gorm:"default:false"`
	HasPersonalInfoElements   bool      `json:"has_personal_info_elements" gorm:"default:false"`
	PersonalInfoScale         int       `json:"personal_info_scale"`
	HasSensitivePersonal      bool      `json:"has_sensitive_personal" gorm:"default:false"`
	HasName                   bool      `json:"has_name" gorm:"default:false"`
	HasContact                bool      `json:"has_contact" gorm:"default:false"`
	IsCrossBorder             bool      `json:"is_cross_border" gorm:"default:false"`
	HasCrossBorderAssessment  bool      `json:"has_cross_border_assessment" gorm:"default:false"`
	AssessmentResult          string    `json:"assessment_result" gorm:"size:100"`
	SecurityMeasures          string    `json:"security_measures" gorm:"type:text;not null"`
	Remarks                   string    `json:"remarks" gorm:"type:text"`
	CreatedBy                 uint      `json:"created_by" gorm:"not null"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
	IsDeleted                 bool      `json:"is_deleted" gorm:"default:false;index"`
}

// SupplyChainAsset 供应链清单
type SupplyChainAsset struct {
	ID            uint      `json:"id" gorm:"primaryKey"`
	SystemName    string    `json:"system_name" gorm:"size:200;not null"`
	SupplierType  string    `json:"supplier_type" gorm:"size:50;not null"`
	CompanyName   string    `json:"company_name" gorm:"size:200;not null"`
	ProvinceCity  string    `json:"province_city" gorm:"size:100;not null"`
	Address       string    `json:"address" gorm:"size:300;not null"`
	ContactPerson string    `json:"contact_person" gorm:"size:50;not null"`
	ContactPhone  string    `json:"contact_phone" gorm:"size:50;not null"`
	ServiceContent string   `json:"service_content" gorm:"type:text;not null"`
	Remarks       string    `json:"remarks" gorm:"type:text"`
	CreatedBy     uint      `json:"created_by" gorm:"not null"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	IsDeleted     bool      `json:"is_deleted" gorm:"default:false;index"`
}

// VulnerabilityAsset 风险漏洞清单
type VulnerabilityAsset struct {
	ID                  uint      `json:"id" gorm:"primaryKey"`
	SystemName          string    `json:"system_name" gorm:"size:200;not null"`
	DiscoveryDate       string    `json:"discovery_date" gorm:"size:20;not null"`
	DiscoveryMethod     string    `json:"discovery_method" gorm:"size:50;not null"`
	AffectedDevice      string    `json:"affected_device" gorm:"type:text;not null"`
	VulnerabilityName   string    `json:"vulnerability_name" gorm:"size:200;not null"`
	CVENumber           string    `json:"cve_number" gorm:"size:50"`
	CNVDNumber          string    `json:"cnvd_number" gorm:"size:50"`
	Domain              string    `json:"domain" gorm:"size:200"`
	IPAddress           string    `json:"ip_address" gorm:"size:50"`
	Severity            string    `json:"severity" gorm:"size:20;not null"`
	Protocol            string    `json:"protocol" gorm:"size:20"`
	Port                int       `json:"port"`
	VulnType            string    `json:"vuln_type" gorm:"size:100"`
	RiskDescription     string    `json:"risk_description" gorm:"type:text;not null"`
	RiskImpact          string    `json:"risk_impact" gorm:"type:text;not null"`
	RemediationSuggestion string  `json:"remediation_suggestion" gorm:"type:text;not null"`
	RemediationMeasure  string    `json:"remediation_measure" gorm:"type:text"`
	CompletionDate      string    `json:"completion_date" gorm:"size:20"`
	CreatedBy           uint      `json:"created_by" gorm:"not null"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	IsDeleted           bool      `json:"is_deleted" gorm:"default:false;index"`
}

// SoftwareStatistics 软件信息统计
type SoftwareStatistics struct {
	ID                  uint      `json:"id" gorm:"primaryKey"`

	// 基本信息
	ReportYear          int       `json:"report_year" gorm:"not null"`
	DepartmentName      string    `json:"department_name" gorm:"size:100;not null"`
	DepartmentHead      string    `json:"department_head" gorm:"size:50"`
	HeadPhone           string    `json:"head_phone" gorm:"size:50"`
	DepartmentFax       string    `json:"department_fax" gorm:"size:50"`
	RegistrationDate    string    `json:"registration_date" gorm:"size:20"`
	IsLegalizationDone  bool      `json:"is_legalization_done" gorm:"default:false"`

	// 人员设备统计
	TotalStaffCount     int       `json:"total_staff_count"`
	ComputerUserCount   int       `json:"computer_user_count"`
	ServerCount         int       `json:"server_count"`
	DesktopCount        int       `json:"desktop_count"`
	LaptopCount         int       `json:"laptop_count"`

	// 采购_操作系统
	PurOsDomLic         int       `json:"pur_os_dom_lic"`
	PurOsDomAmt         float64   `json:"pur_os_dom_amt"`
	PurOsForLic         int       `json:"pur_os_for_lic"`
	PurOsForAmt         float64   `json:"pur_os_for_amt"`

	// 采购_办公软件
	PurOfficeDomLic     int       `json:"pur_office_dom_lic"`
	PurOfficeDomAmt     float64   `json:"pur_office_dom_amt"`
	PurOfficeForLic     int       `json:"pur_office_for_lic"`
	PurOfficeForAmt     float64   `json:"pur_office_for_amt"`

	// 采购_杀毒软件
	PurAvDomLic         int       `json:"pur_av_dom_lic"`
	PurAvDomAmt         float64   `json:"pur_av_dom_amt"`
	PurAvForLic         int       `json:"pur_av_for_lic"`
	PurAvForAmt         float64   `json:"pur_av_for_amt"`

	// 累计_操作系统
	CumOsDomLic         int       `json:"cum_os_dom_lic"`
	CumOsForLic         int       `json:"cum_os_for_lic"`

	// 累计_办公软件
	CumOfficeDomLic     int       `json:"cum_office_dom_lic"`
	CumOfficeForLic     int       `json:"cum_office_for_lic"`

	// 累计_杀毒软件
	CumAvDomLic         int       `json:"cum_av_dom_lic"`
	CumAvForLic         int       `json:"cum_av_for_lic"`

	// 日志与审计
	ModificationLog     string    `json:"modification_log" gorm:"type:text"`
	CreatedBy           uint      `json:"created_by" gorm:"not null"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	IsDeleted           bool      `json:"is_deleted" gorm:"default:false;index"`
}

// ResponsibleDepartment 责任部门
type ResponsibleDepartment struct {
	ID             uint      `json:"id" gorm:"primaryKey"`
	DepartmentName string    `json:"department_name" gorm:"size:100;not null"`
	DepartmentCode string    `json:"department_code" gorm:"size:50"`
	DepartmentHead string    `json:"department_head" gorm:"size:50"`
	HeadPhone      string    `json:"head_phone" gorm:"size:50"`
	DepartmentFax  string    `json:"department_fax" gorm:"size:50"`
	Remarks        string    `json:"remarks" gorm:"type:text"`
	CreatedBy      uint      `json:"created_by" gorm:"not null"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	IsDeleted      bool      `json:"is_deleted" gorm:"default:false;index"`
}
