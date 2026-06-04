package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/paularinzee/natour/internal/models"
	"github.com/paularinzee/natour/pkg/cache"
	"github.com/paularinzee/natour/pkg/email"
	"github.com/paularinzee/natour/pkg/utils"
)

type AuthController struct {
	userCollection *mongo.Collection
	jwtSecret      string
	jwtExpiresIn   time.Duration
	emailSender    email.EmailSender
}

func NewAuthController(db *mongo.Database, jwtSecret string, jwtExpiresIn time.Duration, emailSender email.EmailSender) *AuthController {
	return &AuthController{
		userCollection: db.Collection("users"),
		jwtSecret:      jwtSecret,
		jwtExpiresIn:   jwtExpiresIn,
		emailSender:    emailSender,
	}
}

// SignUpRequest represents the signup request body
type SignUpRequest struct {
	Name            string `json:"name" binding:"required"`
	Email           string `json:"email" binding:"required,email"`
	Password        string `json:"password" binding:"required,min=8"`
	PasswordConfirm string `json:"passwordConfirm" binding:"required"`
	Photo           string `json:"photo"`
	Role            string `json:"role"`
}

// SignUpResponse represents the signup response
type SignUpResponse struct {
	User  models.UserResponse `json:"user"`
	Token string              `json:"token"`
}

// SignUp - POST /api/v1/auth/signup

func (c *AuthController) SignUp(ctx *gin.Context) {
	var req SignUpRequest

	// Validate request body
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	// Check if passwords match
	if req.Password != req.PasswordConfirm {
		ctx.Error(utils.NewBadRequestError("Passwords are not the same"))
		return
	}

	// Normalize email
	email := models.NormalizeEmail(req.Email)

	// Check if user already exists
	var existingUser models.User
	err := c.userCollection.FindOne(context.Background(), bson.M{"email": email}).Decode(&existingUser)
	if err == nil {
		ctx.Error(utils.NewBadRequestError("User with this email already exists"))
		return
	}
	if err != mongo.ErrNoDocuments {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Create new user
	user := models.User{
		ID:              primitive.NewObjectID(),
		Name:            models.SanitizeName(req.Name),
		Email:           email,
		Password:        req.Password,
		PasswordConfirm: req.PasswordConfirm,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
		Active:          true,
	}

	// Set photo if provided
	if req.Photo != "" {
		user.Photo = req.Photo
	} else {
		user.Photo = "default.jpg"
	}

	// Set role if provided and valid
	if req.Role != "" {
		validRole := false
		for _, role := range models.ValidRoles() {
			if models.UserRole(req.Role) == role {
				validRole = true
				user.Role = role
				break
			}
		}
		if !validRole {
			ctx.Error(utils.NewBadRequestError("Invalid role"))
			return
		}
	} else {
		user.Role = models.RoleUser
	}

	// Hash password and validate
	if err := user.BeforeSave(true); err != nil {
		ctx.Error(utils.NewBadRequestError(err.Error()))
		return
	}

	// Insert user into database
	_, err = c.userCollection.InsertOne(context.Background(), user)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Generate JWT token
	token, err := user.GenerateJWT(c.jwtSecret, c.jwtExpiresIn)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Return response (excludes sensitive fields)
	ctx.JSON(201, gin.H{
		"status": "success",
		"data": gin.H{
			"user":  user.ToResponse(),
			"token": token,
		},
	})
}

// LoginRequest represents the login request body
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// LoginResponse represents the login response
type LoginResponse struct {
	User  models.UserResponse `json:"user"`
	Token string              `json:"token"`
}

// Login - POST /api/v1/auth/login

func (c *AuthController) Login(ctx *gin.Context) {
	var req LoginRequest

	// Validate request body
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	// Normalize email
	email := models.NormalizeEmail(req.Email)

	// Find user by email and include password field (normally excluded)
	var user models.User
	filter := bson.M{"email": email, "active": true}

	err := c.userCollection.FindOne(context.Background(), filter).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewUnauthorizedError("Invalid email or password"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Check password
	if !user.CheckPassword(req.Password) {
		ctx.Error(utils.NewUnauthorizedError("Invalid email or password"))
		return
	}

	// Generate JWT token
	token, err := user.GenerateJWT(c.jwtSecret, c.jwtExpiresIn)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Return response
	ctx.JSON(200, gin.H{
		"status": "success",
		"data": gin.H{
			"user":  user.ToResponse(),
			"token": token,
		},
	})
}

// GetMe - GET /api/v1/auth/me

// GetMe - GET /api/v1/auth/me
func (c *AuthController) GetMe(ctx *gin.Context) {
	// Get user ID from context (set by auth middleware)
	userID, exists := ctx.Get("userID")
	if !exists {
		ctx.Error(utils.NewUnauthorizedError("Not authenticated"))
		return
	}

	userIDStr, ok := userID.(string)
	if !ok {
		ctx.Error(utils.NewUnauthorizedError("Invalid user ID"))
		return
	}

	objID, err := primitive.ObjectIDFromHex(userIDStr)
	if err != nil {
		ctx.Error(utils.NewUnauthorizedError("Invalid user ID format"))
		return
	}

	// Find user
	var user models.User
	err = c.userCollection.FindOne(context.Background(), bson.M{"_id": objID, "active": true}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("User not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"data": gin.H{
			"user": user.ToResponse(),
		},
	})
}

// UpdateMe - PATCH /api/v1/auth/updateme

func (c *AuthController) UpdateMe(ctx *gin.Context) {
	// Get user ID from context
	userID, exists := ctx.Get("userID")
	if !exists {
		ctx.Error(utils.NewUnauthorizedError("Not authenticated"))
		return
	}

	userIDStr, ok := userID.(string)
	if !ok {
		ctx.Error(utils.NewUnauthorizedError("Invalid user ID"))
		return
	}

	objID, err := primitive.ObjectIDFromHex(userIDStr)
	if err != nil {
		ctx.Error(utils.NewUnauthorizedError("Invalid user ID format"))
		return
	}

	// Only allow updating certain fields
	var updateData map[string]interface{}
	if err := ctx.ShouldBindJSON(&updateData); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	// Filter allowed fields
	allowedFields := map[string]bool{
		"name":  true,
		"email": true,
		"photo": true,
	}

	filteredUpdate := bson.M{}
	for key, value := range updateData {
		if allowedFields[key] {
			if key == "email" {
				value = models.NormalizeEmail(value.(string))
			}
			if key == "name" {
				value = models.SanitizeName(value.(string))
			}
			filteredUpdate[key] = value
		}
	}

	if len(filteredUpdate) == 0 {
		ctx.Error(utils.NewBadRequestError("No valid fields to update"))
		return
	}

	filteredUpdate["updatedAt"] = time.Now()

	update := bson.M{"$set": filteredUpdate}

	var updatedUser models.User
	err = c.userCollection.FindOneAndUpdate(
		context.Background(),
		bson.M{"_id": objID},
		update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updatedUser)

	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("User not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"data": gin.H{
			"user": updatedUser.ToResponse(),
		},
	})
}

// UpdatePassword - PATCH /api/v1/auth/updatepassword

func (c *AuthController) UpdatePassword(ctx *gin.Context) {
	// Get user ID from context
	userID, exists := ctx.Get("userID")
	if !exists {
		ctx.Error(utils.NewUnauthorizedError("Not authenticated"))
		return
	}

	userIDStr, ok := userID.(string)
	if !ok {
		ctx.Error(utils.NewUnauthorizedError("Invalid user ID"))
		return
	}

	objID, err := primitive.ObjectIDFromHex(userIDStr)
	if err != nil {
		ctx.Error(utils.NewUnauthorizedError("Invalid user ID format"))
		return
	}

	// Parse request
	var req struct {
		PasswordCurrent string `json:"passwordCurrent" binding:"required"`
		Password        string `json:"password" binding:"required,min=8"`
		PasswordConfirm string `json:"passwordConfirm" binding:"required"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	// Check if passwords match
	if req.Password != req.PasswordConfirm {
		ctx.Error(utils.NewBadRequestError("Passwords are not the same"))
		return
	}

	// Find user with password field
	var user models.User
	err = c.userCollection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&user)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Check current password
	if !user.CheckPassword(req.PasswordCurrent) {
		ctx.Error(utils.NewUnauthorizedError("Your current password is wrong"))
		return
	}

	// Update password
	user.Password = req.Password
	user.PasswordConfirm = req.PasswordConfirm

	if err := user.BeforeSave(false); err != nil {
		ctx.Error(utils.NewBadRequestError(err.Error()))
		return
	}

	// Save updated password
	update := bson.M{"$set": bson.M{
		"password":          user.Password,
		"passwordChangedAt": user.PasswordChangedAt,
		"updatedAt":         time.Now(),
	}}

	err = c.userCollection.FindOneAndUpdate(
		context.Background(),
		bson.M{"_id": objID},
		update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&user)

	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Generate new JWT token
	token, err := user.GenerateJWT(c.jwtSecret, c.jwtExpiresIn)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"data": gin.H{
			"user":  user.ToResponse(),
			"token": token,
		},
	})
}

func (c *AuthController) ForgotPassword(ctx *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	// Normalize email
	email := models.NormalizeEmail(req.Email)

	// Find user
	var user models.User
	err := c.userCollection.FindOne(context.Background(), bson.M{"email": email, "active": true}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			// Don't reveal that user doesn't exist (security best practice)
			ctx.JSON(200, gin.H{
				"status":  "success",
				"message": "If your email is registered, you will receive a reset link",
			})
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Create reset token
	resetToken, err := user.CreatePasswordResetToken()
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Save token to database
	update := bson.M{
		"$set": bson.M{
			"passwordResetToken":   user.PasswordResetToken,
			"passwordResetExpires": user.PasswordResetExpires,
		},
	}
	_, err = c.userCollection.UpdateOne(context.Background(), bson.M{"_id": user.ID}, update)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Send email with token
	if err := c.emailSender.SendPasswordResetEmail(user.Email, resetToken); err != nil {
		// Log error but don't expose to user
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status":  "success",
		"message": "Password reset link sent to your email",
	})
}

// ResetPassword - PATCH /api/v1/auth/resetpassword/:token

func (c *AuthController) ResetPassword(ctx *gin.Context) {
	resetToken := ctx.Param("token")
	if resetToken == "" {
		ctx.Error(utils.NewBadRequestError("Reset token is required"))
		return
	}

	var req struct {
		Password        string `json:"password" binding:"required,min=8"`
		PasswordConfirm string `json:"passwordConfirm" binding:"required"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	if req.Password != req.PasswordConfirm {
		ctx.Error(utils.NewBadRequestError("Passwords are not the same"))
		return
	}

	// Find user with valid reset token
	var user models.User
	now := time.Now()
	filter := bson.M{
		"passwordResetToken":   bson.M{"$ne": ""},
		"passwordResetExpires": bson.M{"$gt": now},
		"active":               true,
	}

	err := c.userCollection.FindOne(context.Background(), filter).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewBadRequestError("Invalid or expired reset token"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Verify token
	hash := sha256.Sum256([]byte(resetToken))
	hashedToken := hex.EncodeToString(hash[:])

	if user.PasswordResetToken != hashedToken {
		ctx.Error(utils.NewBadRequestError("Invalid reset token"))
		return
	}

	// Update password
	user.Password = req.Password
	user.PasswordConfirm = req.PasswordConfirm

	if err := user.BeforeSave(false); err != nil {
		ctx.Error(utils.NewBadRequestError(err.Error()))
		return
	}

	// Clear reset token and update password
	update := bson.M{
		"$set": bson.M{
			"password":             user.Password,
			"passwordChangedAt":    user.PasswordChangedAt,
			"passwordResetToken":   "",
			"passwordResetExpires": nil,
			"updatedAt":            time.Now(),
		},
	}

	_, err = c.userCollection.UpdateOne(context.Background(), bson.M{"_id": user.ID}, update)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Generate new JWT token
	token, err := user.GenerateJWT(c.jwtSecret, c.jwtExpiresIn)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"data": gin.H{
			"user":  user.ToResponse(),
			"token": token,
		},
	})
}

// Logout - POST /api/v1/auth/logout

func (c *AuthController) Logout(ctx *gin.Context) {
	// Get token from Authorization header
	authHeader := ctx.GetHeader("Authorization")
	if authHeader == "" {
		ctx.Error(utils.NewUnauthorizedError("Not authenticated"))
		return
	}

	// Extract token
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		ctx.Error(utils.NewUnauthorizedError("Invalid authorization format"))
		return
	}

	tokenString := parts[1]

	// Parse token to get expiration
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return []byte(c.jwtSecret), nil
	})

	if err != nil {
		// Even if token is invalid, still return success
		ctx.JSON(200, gin.H{
			"status":  "success",
			"message": "Logged out successfully",
		})
		return
	}

	// Get expiration time
	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		if exp, ok := claims["exp"]; ok {
			if expFloat, ok := exp.(float64); ok {
				expiration := time.Unix(int64(expFloat), 0)
				cache.AddToBlacklist(tokenString, expiration)
			}
		}
	}

	ctx.JSON(200, gin.H{
		"status":  "success",
		"message": "Logged out successfully",
	})
}

// CreateAdmin - POST /api/v1/auth/create-admin (TEMPORARY - remove in production)
func (c *AuthController) CreateAdmin(ctx *gin.Context) {
	var req struct {
		Name            string `json:"name" binding:"required"`
		Email           string `json:"email" binding:"required,email"`
		Password        string `json:"password" binding:"required,min=8"`
		PasswordConfirm string `json:"passwordConfirm" binding:"required"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(400, gin.H{"status": "error", "message": err.Error()})
		return
	}

	if req.Password != req.PasswordConfirm {
		ctx.JSON(400, gin.H{"status": "error", "message": "Passwords are not the same"})
		return
	}

	email := models.NormalizeEmail(req.Email)

	user := models.User{
		ID:              primitive.NewObjectID(),
		Name:            req.Name,
		Email:           email,
		Password:        req.Password,
		PasswordConfirm: req.PasswordConfirm,
		Photo:           "default.jpg",
		Role:            models.RoleAdmin,
		Active:          true,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := user.BeforeSave(true); err != nil {
		ctx.JSON(400, gin.H{"status": "error", "message": err.Error()})
		return
	}

	_, err := c.userCollection.InsertOne(context.Background(), user)
	if err != nil {
		ctx.JSON(500, gin.H{"status": "error", "message": err.Error()})
		return
	}

	ctx.JSON(201, gin.H{
		"status":  "success",
		"message": "Admin user created",
		"data": gin.H{
			"email": user.Email,
			"role":  user.Role,
		},
	})
}
