package config

import (
	"os"
	"strconv"
)

type Config struct {
	Host      string
	Port      int
	DBPath    string
	JWTSecret string
	DataDir   string
}

func LoadConfig() *Config {
	// 获取可执行文件所在目录
	execPath, _ := os.Getwd()

	// 数据目录
	dataDir := execPath + "/data"
	os.MkdirAll(dataDir, 0755)
	os.MkdirAll(dataDir+"/backup", 0755)
	os.MkdirAll(dataDir+"/export", 0755)

	// 端口配置
	port := 8082
	if p := os.Getenv("PORT"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil {
			port = parsed
		}
	}

	return &Config{
		Host:      "0.0.0.0",
		Port:      port,
		DBPath:    dataDir + "/assets.db",
		JWTSecret: "asset-manager-secret-key-2026",
		DataDir:   dataDir,
	}
}
