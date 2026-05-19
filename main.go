package main

import (
	"asset-manager/internal/config" 
	"asset-manager/internal/database"
	"asset-manager/internal/routes"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
)

//go:embed web/*
var webFiles embed.FS

func main() {
	// 加载配置
	cfg := config.LoadConfig()

	// 初始化数据库
	db, err := database.InitDB(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// 获取嵌入的文件系统
	webSub, _ := fs.Sub(webFiles, "web")

	// 初始化路由
	router := routes.SetupRoutes(db, http.FS(webSub))

	// 启动服务器
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	fmt.Printf("Asset Manager starting on http://%s\n", addr)
	if err := router.Run(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
