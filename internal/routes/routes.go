package routes

import (
	"asset-manager/internal/handlers"
	"asset-manager/internal/middleware"
	"database/sql"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// SetupRoutes 设置路由
func SetupRoutes(db *sql.DB, webFS http.FileSystem, allowedOrigins []string) *gin.Engine {
	router := gin.Default()
	// 旧系统未配置可信反向代理，禁止直接信任客户端提供的转发头，
	// 避免攻击者伪造 IP 绕过登录限流。
	if err := router.SetTrustedProxies(nil); err != nil {
		panic("配置可信代理失败: " + err.Error())
	}

	allowed := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = true
	}

	// 配置CORS与基础安全响应头。旧前端仍含内联样式，因此style-src暂时保留unsafe-inline。
	router.Use(func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin != "" && allowed[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		if c.Request.Method == "OPTIONS" {
			if origin != "" && !allowed[origin] {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
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
		protected.POST("/auth/change-password", handlers.ChangePassword(db))

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
			assets.GET("/export", handlers.ExportAssetToCSV(db))
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
