package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/paularinzee/natour/pkg/utils"
)

func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		// Check if there are any errors
		if len(c.Errors) > 0 {
			err := c.Errors.Last().Err

			switch e := err.(type) {
			case *utils.AppError:
				c.JSON(e.StatusCode, gin.H{
					"status":  "error",
					"message": e.Message,
					"code":    e.ErrorCode,
				})
				return

			case mongo.WriteException:
				// Handle duplicate key error
				if strings.Contains(e.Error(), "duplicate key") {
					c.JSON(http.StatusConflict, gin.H{
						"status":  "error",
						"message": "Duplicate value: This record already exists",
						"code":    "DUPLICATE_KEY",
					})
					return
				}

			default:
				// Default error response
				c.JSON(http.StatusInternalServerError, gin.H{
					"status":  "error",
					"message": "Something went wrong",
					"code":    "INTERNAL_ERROR",
				})
			}
		}
	}
}
