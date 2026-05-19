package middleware

import (
	"asset-manager/internal/utils"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware JWT认证中间件
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "未提供认证令牌",
			})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "认证令牌格式错误",
			})
			c.Abort()
			return
		}

		claims, err := utils.ParseToken(parts[1])
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "无效的认证令牌",
			})
			c.Abort()
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Next()
	}
}

// AdminOnly 管理员权限中间件
func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists || role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "需要管理员权限",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// OwnerOrAdmin 所有者或管理员权限中间件
func OwnerOrAdmin(getCreatedByFunc func(c *gin.Context) uint) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _ := c.Get("user_id")
		role, _ := c.Get("role")

		if role == "admin" {
			c.Next()
			return
		}

		createdBy := getCreatedByFunc(c)
		if createdBy != userID.(uint) {
			c.JSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "无权限操作此资源",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
