package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"github.com/paularinzee/natour/pkg/utils"
)

var validate = validator.New()

func ValidateRequest(body interface{}) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := c.ShouldBindJSON(body); err != nil {
			c.Error(utils.NewBadRequestError("Invalid request body: " + err.Error()))
			c.Abort()
			return
		}

		if err := validate.Struct(body); err != nil {
			c.Error(utils.NewBadRequestError("Validation failed: " + err.Error()))
			c.Abort()
			return
		}

		c.Set("validatedBody", body)
		c.Next()
	}
}
