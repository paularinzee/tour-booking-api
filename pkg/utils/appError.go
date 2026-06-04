package utils

import (
	"fmt"
)

type AppError struct {
	StatusCode int
	Message    string
	ErrorCode  string
	Err        error
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func NewAppError(statusCode int, message string, err error) *AppError {
	return &AppError{
		StatusCode: statusCode,
		Message:    message,
		Err:        err,
	}
}

func NewBadRequestError(message string) *AppError {
	return &AppError{
		StatusCode: 400,
		Message:    message,
		ErrorCode:  "BAD_REQUEST",
	}
}

func NewNotFoundError(message string) *AppError {
	return &AppError{
		StatusCode: 404,
		Message:    message,
		ErrorCode:  "NOT_FOUND",
	}
}

func NewInternalServerError(err error) *AppError {
	return &AppError{
		StatusCode: 500,
		Message:    "Internal server error",
		ErrorCode:  "INTERNAL_SERVER_ERROR",
		Err:        err,
	}
}

func NewUnauthorizedError(message string) *AppError {
	return &AppError{
		StatusCode: 401,
		Message:    message,
		ErrorCode:  "UNAUTHORIZED",
	}
}

// Add this function
func NewForbiddenError(message string) *AppError {
	return &AppError{
		StatusCode: 403,
		Message:    message,
		ErrorCode:  "FORBIDDEN",
	}
}
