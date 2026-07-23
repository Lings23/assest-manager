package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"asset-manager/internal/utils"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

const (
	loginFailureLimit = 5
	loginWindow       = 5 * time.Minute
	loginLockDuration = 15 * time.Minute
)

type loginAttempt struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
}

var loginAttempts = struct {
	sync.Mutex
	items map[string]loginAttempt
}{items: make(map[string]loginAttempt)}

var dummyPasswordHash, _ = bcrypt.GenerateFromPassword([]byte("invalid-password-placeholder"), bcrypt.DefaultCost)

func loginAttemptKey(c *gin.Context, username string) string {
	return c.ClientIP() + "|" + strings.ToLower(strings.TrimSpace(username))
}

func loginLocked(key string, now time.Time) (bool, time.Duration) {
	loginAttempts.Lock()
	defer loginAttempts.Unlock()
	attempt, exists := loginAttempts.items[key]
	if !exists || !attempt.lockedUntil.After(now) {
		if exists && now.Sub(attempt.windowStart) > loginWindow {
			delete(loginAttempts.items, key)
		}
		return false, 0
	}
	return true, time.Until(attempt.lockedUntil)
}

func recordLoginFailure(key string, now time.Time) {
	loginAttempts.Lock()
	defer loginAttempts.Unlock()
	attempt := loginAttempts.items[key]
	if attempt.windowStart.IsZero() || now.Sub(attempt.windowStart) > loginWindow {
		attempt = loginAttempt{windowStart: now}
	}
	attempt.failures++
	if attempt.failures >= loginFailureLimit {
		attempt.lockedUntil = now.Add(loginLockDuration)
	}
	loginAttempts.items[key] = attempt
}

func clearLoginFailures(key string) {
	loginAttempts.Lock()
	delete(loginAttempts.items, key)
	loginAttempts.Unlock()
}

// LoginRequest 登录请求结构
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginResponse 登录响应结构
type LoginResponse struct {
	Token string   `json:"token"`
	User  UserInfo `json:"user"`
}

// ChangePasswordRequest 修改当前用户密码请求。
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
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
		req.Username = strings.TrimSpace(req.Username)
		if len(req.Username) > 50 || len(req.Password) > 256 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "用户名或密码长度不合法"})
			return
		}

		now := time.Now()
		attemptKey := loginAttemptKey(c, req.Username)
		if locked, retryAfter := loginLocked(attemptKey, now); locked {
			c.Header("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
			c.JSON(http.StatusTooManyRequests, gin.H{"code": 429, "message": "登录失败次数过多，请稍后重试"})
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
			_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(req.Password))
			recordLoginFailure(attemptKey, now)
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
			recordLoginFailure(attemptKey, now)
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "用户名或密码错误",
			})
			return
		}
		clearLoginFailures(attemptKey)

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
		tokenID, _ := c.Get("token_id")
		expiresAt, _ := c.Get("token_expires_at")
		if id, ok := tokenID.(string); ok {
			expiry, _ := expiresAt.(time.Time)
			if expiry.IsZero() {
				expiry = time.Now().Add(2 * time.Hour)
			}
			utils.RevokeToken(id, expiry)
		}
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

// ChangePassword 修改当前用户密码，并使当前访问令牌立即失效。
func ChangePassword(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ChangePasswordRequest
		if err := c.ShouldBindJSON(&req); err != nil || len(req.NewPassword) < 12 || len(req.NewPassword) > 128 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "新密码长度必须为 12 到 128 位"})
			return
		}

		userID, ok := c.Get("user_id")
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "未认证"})
			return
		}

		var currentHash string
		if err := db.QueryRow("SELECT password FROM users WHERE id = ? AND is_active = 1", userID).Scan(&currentHash); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "用户状态无效"})
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(req.CurrentPassword)) != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "当前密码错误"})
			return
		}

		newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "密码更新失败"})
			return
		}
		if _, err = db.Exec("UPDATE users SET password = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", string(newHash), userID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "密码更新失败"})
			return
		}

		tokenID, _ := c.Get("token_id")
		expiresAt, _ := c.Get("token_expires_at")
		if id, ok := tokenID.(string); ok {
			expiry, _ := expiresAt.(time.Time)
			utils.RevokeToken(id, expiry)
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "密码修改成功，请重新登录"})
	}
}
