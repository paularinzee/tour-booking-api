package middleware

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/paularinzee/natour/pkg/cache"
	"github.com/paularinzee/natour/pkg/utils"
)

func AuthMiddleware(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get token from Authorization header
		authHeader := c.GetHeader("Authorization")
		fmt.Printf("DEBUG Auth Header: %s\n", authHeader)

		if authHeader == "" {
			fmt.Println("DEBUG: No auth header")
			c.Error(utils.NewUnauthorizedError("Not authenticated"))
			c.Abort()
			return
		}

		// Extract token (Bearer <token>)
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			fmt.Printf("DEBUG: Invalid format - parts length: %d, first part: %s\n", len(parts), parts[0])
			c.Error(utils.NewUnauthorizedError("Invalid authorization format"))
			c.Abort()
			return
		}

		tokenString := parts[1]
		fmt.Printf("DEBUG Token string length: %d\n", len(tokenString))

		// Check if token is blacklisted
		if cache.IsBlacklisted(tokenString) {
			fmt.Println("DEBUG: Token is blacklisted")
			c.Error(utils.NewUnauthorizedError("Token has been invalidated. Please login again."))
			c.Abort()
			return
		}

		// Parse and validate token
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			// Validate signing method
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				// return nil, utils.NewUnauthorizedError("Invalid signing method")
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return []byte(jwtSecret), nil
		})

		if err != nil {
			fmt.Printf("DEBUG: JWT Parse error: %v\n", err)
			c.Error(utils.NewUnauthorizedError("Invalid or expired token"))
			c.Abort()
			return
		}

		// Extract claims
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok || !token.Valid {
			fmt.Println("DEBUG: Invalid claims or token not valid")
			c.Error(utils.NewUnauthorizedError("Invalid token claims"))
			c.Abort()
			return
		}

		fmt.Printf("DEBUG Claims: id=%v, email=%v, role=%v\n", claims["id"], claims["email"], claims["role"])

		// Set user info in context
		c.Set("userID", claims["id"])
		c.Set("userEmail", claims["email"])
		c.Set("userRole", claims["role"])

		c.Next()
	}
}

// RestrictTo roles middleware
func RestrictTo(allowedRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, exists := c.Get("userRole")
		if !exists {
			fmt.Println("DEBUG RestrictTo: userRole not found in context")
			c.Error(utils.NewUnauthorizedError("Not authenticated"))
			c.Abort()
			return
		}

		roleStr, ok := userRole.(string)
		if !ok {
			fmt.Printf("DEBUG RestrictTo: userRole is not string: %T\n", userRole)
			c.Error(utils.NewUnauthorizedError("Invalid role format"))
			c.Abort()
			return
		}
		fmt.Printf("DEBUG RestrictTo: userRole=%s, allowed=%v\n", roleStr, allowedRoles)

		for _, role := range allowedRoles {
			if roleStr == role {
				c.Next()
				return
			}
		}

		c.Error(utils.NewUnauthorizedError("You don't have permission to access this resource"))
		c.Abort()
	}
}
