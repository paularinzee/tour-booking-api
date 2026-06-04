package middleware

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/paularinzee/natour/pkg/utils"
)

// Roles constants
const (
	RoleUser      = "user"
	RoleGuide     = "guide"
	RoleLeadGuide = "lead-guide"
	RoleAdmin     = "admin"
)

// RequireAuth is DEPRECATED - use AuthMiddleware from auth.go instead
// This function does NOT actually authenticate users
// It only checks if userID exists in context (which it won't without AuthMiddleware)
func RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// This is a placeholder - DO NOT USE
		// Use AuthMiddleware from auth.go instead
		fmt.Println("WARNING: RequireAuth called but does not authenticate! Use AuthMiddleware instead.")

		_, exists := c.Get("userID")
		if !exists {
			c.Error(utils.NewUnauthorizedError("Not authenticated"))
			c.Abort()
			return
		}
		c.Next()
	}
}

// AllowRoles checks if user has any of the allowed roles
// IMPORTANT: This assumes AuthMiddleware has already been called to set userRole
func AllowRoles(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, exists := c.Get("userRole")
		if !exists {
			fmt.Println("DEBUG AllowRoles: userRole not found in context - make sure AuthMiddleware is called first")
			c.Error(utils.NewUnauthorizedError("Not authenticated"))
			c.Abort()
			return
		}

		roleStr, ok := userRole.(string)
		if !ok {
			c.Error(utils.NewUnauthorizedError("Invalid role"))
			c.Abort()
			return
		}

		for _, role := range roles {
			if roleStr == role {
				c.Next()
				return
			}
		}

		c.Error(utils.NewForbiddenError("Access denied"))
		c.Abort()
	}
}

// AllowSelf checks if user is accessing their own resource
func AllowSelf(getResourceUserID func(c *gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentUserID, exists := c.Get("userID")
		if !exists {
			c.Error(utils.NewUnauthorizedError("Not authenticated"))
			c.Abort()
			return
		}

		resourceUserID := getResourceUserID(c)
		if resourceUserID == "" {
			c.Error(utils.NewNotFoundError("Resource not found"))
			c.Abort()
			return
		}

		userRole, _ := c.Get("userRole")

		// Admin can access anything
		if userRole == RoleAdmin {
			c.Next()
			return
		}

		if currentUserID != resourceUserID {
			c.Error(utils.NewForbiddenError("Access denied"))
			c.Abort()
			return
		}

		c.Next()
	}
}

// Public allows anyone (no auth)
func Public() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
	}
}
