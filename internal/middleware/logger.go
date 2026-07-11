package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// LoggerConfig configures the structured logger middleware
type LoggerConfig struct {
	SkipPaths         []string
	LogRequestBody    bool
	LogRequestHeaders bool
	MaxBodySize       int64
}

// DefaultLoggerConfig returns production standard properties
func DefaultLoggerConfig() LoggerConfig {
	return LoggerConfig{
		SkipPaths:         []string{"/health", "/metrics", "/favicon.ico"},
		LogRequestBody:    false,
		LogRequestHeaders: false,
		MaxBodySize:       2048, // 2KB safe upper bound
	}
}

// Logger returns a high-throughput, structured log writer middleware
func Logger(config LoggerConfig) gin.HandlerFunc {
	// Build a fast lookup lookup map for skipped endpoints
	skipMap := make(map[string]bool, len(config.SkipPaths))
	for _, path := range config.SkipPaths {
		skipMap[path] = true
	}

	return func(c *gin.Context) {
		if skipMap[c.Request.URL.Path] {
			c.Next()
			return
		}

		start := time.Now()
		var requestBodyAttr slog.Attr

		// Intercept body safely without inviting OOM attacks
		if config.LogRequestBody && c.Request.Body != nil && c.Request.ContentLength > 0 {
			// Limit bytes read from network stream to the maximum allowed body size + 1 (to check for truncation)
			limitReader := io.LimitReader(c.Request.Body, config.MaxBodySize+1)
			bodyBytes, err := io.ReadAll(limitReader)

			if err == nil {
				// Re-patch the request body stream cleanly for underlying controller pipelines
				// We must chain the read bytes back with the remaining untouched body stream
				c.Request.Body = io.NopCloser(io.MultiReader(
					bytes.NewReader(bodyBytes),
					c.Request.Body,
				))

				var parsedBody interface{}
				isTruncated := int64(len(bodyBytes)) > config.MaxBodySize

				targetBytes := bodyBytes
				if isTruncated {
					targetBytes = bodyBytes[:config.MaxBodySize]
				}

				if json.Unmarshal(targetBytes, &parsedBody) == nil {
					if isTruncated {
						requestBodyAttr = slog.Any("request_body", map[string]interface{}{
							"data":      parsedBody,
							"truncated": true,
						})
					} else {
						requestBodyAttr = slog.Any("request_body", parsedBody)
					}
				} else if len(targetBytes) > 0 {
					val := string(targetBytes)
					if isTruncated {
						val += "... (truncated)"
					}
					requestBodyAttr = slog.String("request_body", val)
				}
			}
		}

		// Collect request headers cleanly
		var headersAttr slog.Attr
		if config.LogRequestHeaders {
			headersMap := make(map[string]string)
			for key, values := range c.Request.Header {
				if len(values) > 0 {
					lowKey := strings.ToLower(key)
					// Mask credentials aggressively
					if lowKey == "authorization" || lowKey == "cookie" || lowKey == "x-api-key" {
						headersMap[key] = "[REDACTED]"
					} else {
						headersMap[key] = values[0]
					}
				}
			}
			headersAttr = slog.Any("request_headers", headersMap)
		}

		// Execute downstream handlers/controllers
		c.Next()

		// Context compilation metric states
		latency := time.Since(start).Milliseconds()
		userID, _ := c.Get(string(CtxUserID))
		userRole, _ := c.Get(string(CtxUserRole))

		// Construct high-performance structured parameters
		args := []any{
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.String("query", c.Request.URL.RawQuery),
			slog.String("ip", c.ClientIP()),
			slog.String("user_agent", c.Request.UserAgent()),
			slog.Int("status_code", c.Writer.Status()),
			slog.Int64("response_time_ms", latency),
			slog.Int64("request_size_bytes", c.Request.ContentLength),
			slog.Int("response_size_bytes", c.Writer.Size()),
		}

		if userID != nil {
			args = append(args, slog.Any("user_id", userID))
		}
		if userRole != nil {
			args = append(args, slog.Any("user_role", userRole))
		}
		if requestBodyAttr.Key != "" {
			args = append(args, requestBodyAttr)
		}
		if headersAttr.Key != "" {
			args = append(args, headersAttr)
		}
		if len(c.Errors) > 0 {
			args = append(args, slog.String("error", c.Errors.Last().Error()))
		}

		// Log using contextual log routing levels dynamically
		status := c.Writer.Status()
		switch {
		case status >= 500:
			slog.Error("API HTTP Request Failure", args...)
		case status >= 400:
			slog.Warn("API HTTP Request Client Exception", args...)
		default:
			slog.Info("API HTTP Request Success", args...)
		}
	}
}
