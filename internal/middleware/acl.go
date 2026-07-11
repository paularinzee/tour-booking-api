package middleware

import (
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/paularinzee/natour/pkg/utils"
)

// Define custom types for context keys to prevent collisions and typos
type contextKey string

const (
	CtxUserID   contextKey = "userID"
	CtxUserRole contextKey = "userRole"
)

// Roles constants
const (
	RoleUser      = "user"
	RoleGuide     = "guide"
	RoleLeadGuide = "lead-guide"
	RoleAdmin     = "admin"
)

// AllowRoles checks if user has any of the allowed roles
// IMPORTANT: This assumes AuthMiddleware has already been called to set userRole
func AllowRoles(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, exists := c.Get(string(CtxUserRole))
		if !exists {
			slog.Warn("Authorization failed: userRole missing from context. Ensure AuthMiddleware is executed first.")
			c.Error(utils.NewUnauthorizedError("Not authenticated"))
			c.Abort()
			return
		}

		roleStr, ok := userRole.(string)
		if !ok {
			slog.Error("Authorization failed: userRole context value is not a string", "actual_type", fmt.Sprintf("%T", userRole))
			c.Error(utils.NewUnauthorizedError("Invalid role configuration"))
			c.Abort()
			return
		}

		for _, role := range roles {
			if roleStr == role {
				c.Next()
				return
			}
		}

		slog.Info("Access denied: unauthorized role", "user_role", roleStr, "required_roles", roles)
		c.Error(utils.NewForbiddenError("Access denied"))
		c.Abort()
	}
}

// AllowSelf checks if user is accessing their own resource
func AllowSelf(getResourceUserID func(c *gin.Context) (string, bool)) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentUserID, exists := c.Get("userID")
		if !exists {
			c.Error(utils.NewUnauthorizedError("Not authenticated"))
			c.Abort()
			return
		}

		// Admin bypasses ownership checks early
		userRole, _ := c.Get(string(CtxUserRole))
		if userRole == RoleAdmin {
			c.Next()
			return
		}

		resourceUserID, ok := getResourceUserID(c)
		if !ok || resourceUserID == "" {
			// Fail-closed security posture: if we can't safely identify the resource owner, deny access.
			slog.Error("Ownership check failed: unable to resolve resource user ID")
			c.Error(utils.NewForbiddenError("Access denied"))
			c.Abort()
			return
		}

		if currentUserID != resourceUserID {
			slog.Info("Access denied: user does not own resource", "auth_user", currentUserID, "owner_user", resourceUserID)
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
