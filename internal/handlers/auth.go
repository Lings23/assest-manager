package handlers

import (
	"database/sql"
	"net/http"

	"asset-manager/internal/utils"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// LoginRequest 登录请求结构
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginResponse 登录响应结构
type LoginResponse struct {
	Token string     `json:"token"`
	User  UserInfo   `json:"user"`
}

// UserInfo 用户信息结构
type UserInfo struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// Login 用户登录
func Login(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    400,
				"message": "请求参数错误",
			})
			return
		}

		var id uint
		var password, role string
		var isActive bool

		err := db.QueryRow(
			"SELECT id, password, role, is_active FROM users WHERE username = ?",
			req.Username,
		).Scan(&id, &password, &role, &isActive)

		if err == sql.ErrNoRows {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "用户名或密码错误",
			})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":    500,
				"message": "服务器错误",
			})
			return
		}

		if !isActive {
			c.JSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "账号已被禁用",
			})
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(password), []byte(req.Password)); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "用户名或密码错误",
			})
			return
		}

		token, err := utils.GenerateToken(id, req.Username, role)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":    500,
				"message": "生成令牌失败",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"code":    200,
			"message": "登录成功",
			"data": LoginResponse{
				Token: token,
				User: UserInfo{
					ID:       id,
					Username: req.Username,
					Role:     role,
				},
			},
		})
	}
}

// Logout 用户登出
func Logout() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"code":    200,
			"message": "登出成功",
		})
	}
}

// GetCurrentUser 获取当前用户信息
func GetCurrentUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _ := c.Get("user_id")
		username, _ := c.Get("username")
		role, _ := c.Get("role")

		c.JSON(http.StatusOK, gin.H{
			"code": 200,
			"data": UserInfo{
				ID:       userID.(uint),
				Username: username.(string),
				Role:     role.(string),
			},
		})
	}
}
