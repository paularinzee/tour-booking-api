package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/paularinzee/natour/pkg/utils"
)

// ErrorHandler intercepts failures attached to the Gin context and normalizes HTTP API responses.
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Execute downstream handlers/controllers first
		c.Next()

		// If no errors occurred during the request lifecycle, return immediately
		if len(c.Errors) == 0 {
			return
		}

		err := c.Errors.Last().Err
		c.Abort() // Signal explicitly that the request lifecycle is terminated

		// 1. Check for custom application domain errors
		var appErr *utils.AppError
		if errors.As(err, &appErr) {
			c.JSON(appErr.StatusCode, gin.H{
				"status":  "error",
				"message": appErr.Message,
				"code":    appErr.ErrorCode,
			})
			return
		}

		// 2. Check for MongoDB Write Exceptions (Handling pointers explicitly)
		var writeEx mongo.WriteException
		var writeExPtr *mongo.WriteException

		if errors.As(err, &writeExPtr) {
			writeEx = *writeExPtr
		}

		if errors.As(err, &writeEx) || writeExPtr != nil {
			// Alternatively check explicit driver codes: e.g., writeEx.WriteErrors[0].Code == 11000
			if strings.Contains(err.Error(), "duplicate key") {
				c.JSON(http.StatusConflict, gin.H{
					"status":  "error",
					"message": "Duplicate value: This record already exists",
					"code":    "DUPLICATE_KEY",
				})
				return
			}
		}

		// 3. Fallback for unhandled/internal system errors
		// CRITICAL: Log the actual error internally so engineering can diagnose it
		slog.Error("Unhandled system error occurred during request",
			"error", err.Error(),
			"path", c.Request.URL.Path,
			"method", c.Request.Method,
		)

		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": "Something went wrong",
			"code":    "INTERNAL_ERROR",
		})
	}
}
