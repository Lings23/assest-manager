package routes

import (
	"asset-manager/internal/handlers"
	"asset-manager/internal/middleware"
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

// SetupRoutes 设置路由
func SetupRoutes(db *sql.DB, webFS http.FileSystem) *gin.Engine {
	router := gin.Default()

	// 配置CORS
	router.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// 静态文件服务 - 手动处理以确保正确路径
	router.GET("/static/*filepath", func(c *gin.Context) {
		filepath := c.Param("filepath")
		c.FileFromFS(filepath, webFS)
	})

	// API路由组
	api := router.Group("/api")

	// 认证路由(无需登录)
	auth := api.Group("/auth")
	{
		auth.POST("/login", handlers.Login(db))
	}

	// 需要认证的路由
	protected := api.Group("")
	protected.Use(middleware.AuthMiddleware())
	{
		protected.POST("/auth/logout", handlers.Logout())
		protected.GET("/auth/me", handlers.GetCurrentUser())

		// 软件信息统计累计计算（特殊路由）
		protected.GET("/assets/software-stat/calculate-cumulative", handlers.CalculateCumulative(db))
		protected.POST("/assets/software-stat/calculate-cumulative", handlers.CalculateCumulative(db))

		// 资产管理路由 - 使用通用Handler
		assets := protected.Group("/assets/:type")
		{
			assets.GET("", handlers.ListAssets(db))
			assets.POST("", handlers.CreateAsset(db))
			assets.GET("/:id", handlers.GetAsset(db))
			assets.PUT("/:id", handlers.UpdateAsset(db))
			assets.DELETE("/:id", middleware.AdminOnly(), handlers.DeleteAsset(db))
			assets.GET("/export", handlers.ExportAsset(db))
			assets.POST("/import", handlers.ImportAsset(db))
			assets.GET("/template", handlers.DownloadTemplate(db))
		}

		// 统计路由
		stats := protected.Group("/stats")
		{
			stats.GET("/summary", handlers.GetStatsSummary(db))
			stats.GET("/system-info", handlers.GetSystemInfoStats(db))
			stats.GET("/hardware", handlers.GetHardwareStats(db))
			stats.GET("/vulnerability", handlers.GetVulnerabilityStats(db))
		}
	}

	// SPA fallback - 所有未匹配的路由返回index.html
	router.NoRoute(func(c *gin.Context) {
		c.FileFromFS("/", webFS)
	})

	return router
}
