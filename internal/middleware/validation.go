package middleware

import (
	"log/slog"
	"reflect"

	"github.com/gin-gonic/gin"
	"github.com/paularinzee/natour/pkg/utils"
)

// Typed context key for safety
const CtxValidatedBody = "validatedBody"

// ValidateRequest initializes a concurrent-safe request payload validation barrier.
// It uses reflection to dynamically instantiate new struct values per request.
func ValidateRequest(model interface{}) gin.HandlerFunc {
	t := reflect.TypeOf(model)
	// Guard rails: make sure the developer actually passed a struct type on initialization
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		panic("ValidateRequest middleware must be initialized with a struct type")
	}

	return func(c *gin.Context) {
		// Create a completely new pointer to a fresh instance of the struct type
		// This guarantees that concurrent requests do not corrupt each other's data footprints
		newBody := reflect.New(t).Interface()

		// ShouldBindJSON binds AND automatically triggers go-playground validation via `binding` tags
		if err := c.ShouldBindJSON(newBody); err != nil {
			slog.Warn("Request schema validation rejected", "error", err.Error(), "path", c.Request.URL.Path)

			c.Error(utils.NewBadRequestError("Invalid request layout or validation constraints failed: " + err.Error()))
			c.Abort()
			return
		}

		// Store the isolated, validated data structural reference into the context safely
		c.Set(CtxValidatedBody, newBody)
		c.Next()
	}
}

// Helper utility to pull out the typed model inside your handlers safely
func GetValidatedBody[T any](c *gin.Context) (*T, bool) {
	val, exists := c.Get(CtxValidatedBody)
	if !exists {
		return nil, false
	}

	typedVal, ok := val.(*T)
	return typedVal, ok
}
