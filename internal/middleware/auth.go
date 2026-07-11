package middleware

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/paularinzee/natour/pkg/cache"
	"github.com/paularinzee/natour/pkg/utils"
)

func AuthMiddleware(jwtSecret string) gin.HandlerFunc {

	if jwtSecret == "" {
		panic("AuthMiddleware: jwtSecret cannot be empty")
	}

	return func(c *gin.Context) {
		// Get token from Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Error(utils.NewUnauthorizedError("Not authenticated"))
			c.Abort()
			return
		}

		// Extract token (Bearer <token>)
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			slog.Warn("Auth failed: invalid Authorization header format")
			c.Error(utils.NewUnauthorizedError("Invalid authorization format"))
			c.Abort()
			return
		}

		tokenString := parts[1]

		fmt.Printf("DEBUG Token string length: %d\n", len(tokenString))

		// Check if token is blacklisted
		if cache.IsBlacklisted(tokenString) {
			slog.Warn("Auth failed: token is blacklisted")
			c.Error(utils.NewUnauthorizedError("Token has been invalidated. Please login again."))
			c.Abort()
			return
		}

		// Parse and validate token signature/claims expiration
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return []byte(jwtSecret), nil
		})

		if err != nil {
			slog.Debug("Auth failed: JWT parsing or validation error", "error", err)
			c.Error(utils.NewUnauthorizedError("Invalid or expired token"))
			c.Abort()
			return
		}

		/// Safely unpack claims
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok || !token.Valid {
			c.Error(utils.NewUnauthorizedError("Invalid token claims"))
			c.Abort()
			return
		}

		// Safely resolve and type-assert expected claims
		userID, _ := claims["id"]
		userEmail, _ := claims["email"]
		userRole, _ := claims["role"]

		if userID == nil || userRole == nil {
			slog.Error("Auth failed: token missing required claims")
			c.Error(utils.NewUnauthorizedError("Malformed token structure"))
			c.Abort()
			return
		}

		// Use typed keys to prevent downstream package typos
		c.Set(string(CtxUserID), userID)
		c.Set(string(CtxUserRole), userRole)
		c.Set("userEmail", userEmail) // Can stringify if an email constant type is declared

		c.Next()
	}
}
