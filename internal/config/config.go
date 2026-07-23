package config

import (
	"crypto/rand"
	"encoding/base64"
	"log"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Host               string
	Port               int
	DBPath             string
	JWTSecret          string
	JWTTTLHours        int
	CORSAllowedOrigins []string
	DataDir            string
}

func loadJWTSecret() string {
	if secret := strings.TrimSpace(os.Getenv("JWT_SECRET")); len(secret) >= 32 {
		return secret
	}
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		panic("无法生成临时JWT密钥: " + err.Error())
	}
	log.Println("WARNING: JWT_SECRET未配置或短于32字符，已生成仅本次进程有效的随机密钥；重启后现有令牌将失效")
	return base64.RawURLEncoding.EncodeToString(secretBytes)
}

func splitNonEmpty(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
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
	jwtTTLHours := 2
	if value := os.Getenv("JWT_TTL_HOURS"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 1 && parsed <= 24 {
			jwtTTLHours = parsed
		}
	}

	return &Config{
		Host:               "0.0.0.0",
		Port:               port,
		DBPath:             dataDir + "/assets.db",
		JWTSecret:          loadJWTSecret(),
		JWTTTLHours:        jwtTTLHours,
		CORSAllowedOrigins: splitNonEmpty(os.Getenv("CORS_ALLOWED_ORIGINS")),
		DataDir:            dataDir,
	}
}
