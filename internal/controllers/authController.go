package controllers

import (
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
}

// SignUpResponse represents the signup response
type SignUpResponse struct {
	User  models.UserResponse `json:"user"`
	Token string              `json:"token"`
}

// UpdateMeRequest represents fields allowed for profile update
type UpdateMeRequest struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	Photo string `json:"photo,omitempty"`
}

// UpdatePasswordRequest represents the password change payload
type UpdatePasswordRequest struct {
	PasswordCurrent string `json:"passwordCurrent" binding:"required"`
	Password        string `json:"password" binding:"required,min=8"`
	PasswordConfirm string `json:"passwordConfirm" binding:"required"`
}

// ForgotPasswordRequest represents the email payload for reset
type ForgotPasswordRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// ResetPasswordRequest represents the payload to reset password
type ResetPasswordRequest struct {
	Password        string `json:"password" binding:"required,min=8"`
	PasswordConfirm string `json:"passwordConfirm" binding:"required"`
}

// SignUp godoc
// @Summary      Register a new user
// @Description  Creates a new user account with default 'user' role and returns an authentication JWT token
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request  body      SignUpRequest  true  "User Registration Details"
// @Success      201      {object}  map[string]interface{}
// @Failure      400      {object}  map[string]string  "Validation error or duplicate email"
// @Failure      500      {object}  map[string]string  "Internal server error"
// @Router       /auth/signup [post]
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
	emailStr := models.NormalizeEmail(req.Email)

	// Create new user - Strict production rule: Always default to standard user role during signup
	user := models.User{
		ID:              primitive.NewObjectID(),
		Name:            models.SanitizeName(req.Name),
		Email:           emailStr,
		Password:        req.Password,
		PasswordConfirm: req.PasswordConfirm,
		Role:            models.RoleUser,
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

	// Hash password and validate
	if err := user.BeforeSave(true); err != nil {
		ctx.Error(utils.NewBadRequestError(err.Error()))
		return
	}

	// Insert user into database (Using ctx.Request.Context() for graceful cancellation)
	_, err := c.userCollection.InsertOne(ctx.Request.Context(), user)
	if err != nil {
		// Handle race conditions via MongoDB unique index violation (Error code 11000)
		if mongo.IsDuplicateKeyError(err) {
			ctx.Error(utils.NewBadRequestError("User with this email already exists"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Generate JWT token
	token, err := user.GenerateJWT(c.jwtSecret, c.jwtExpiresIn)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

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

// Login godoc
// @Summary      Log in user
// @Description  Authenticates a user with email and password, returning a JWT token
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        credentials  body      LoginRequest  true  "Email and Password"
// @Success      200          {object}  map[string]interface{}
// @Failure      400          {object}  map[string]string  "Invalid request body"
// @Failure      401          {object}  map[string]string  "Invalid email or password"
// @Failure      500          {object}  map[string]string  "Internal server error"
// @Router       /auth/login [post]
func (c *AuthController) Login(ctx *gin.Context) {
	var req LoginRequest

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	emailStr := models.NormalizeEmail(req.Email)

	var user models.User
	filter := bson.M{"email": emailStr, "active": true}

	// Context used to drop execution if client cancels
	err := c.userCollection.FindOne(ctx.Request.Context(), filter).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewUnauthorizedError("Invalid email or password"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	if !user.CheckPassword(req.Password) {
		ctx.Error(utils.NewUnauthorizedError("Invalid email or password"))
		return
	}

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

// GetMe godoc
// @Summary      Get current user profile
// @Description  Retrieves details of the currently authenticated user
// @Tags         auth
// @Security     Bearer
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string  "Unauthorized / Not authenticated"
// @Failure      404  {object}  map[string]string  "User not found"
// @Router       /auth/me [get]
func (c *AuthController) GetMe(ctx *gin.Context) {
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

	var user models.User
	err = c.userCollection.FindOne(ctx.Request.Context(), bson.M{"_id": objID, "active": true}).Decode(&user)
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

// UpdateMe godoc
// @Summary      Update user profile
// @Description  Updates authenticated user details (name, email, photo)
// @Tags         auth
// @Security     Bearer
// @Accept       json
// @Produce      json
// @Param        request  body      UpdateMeRequest  true  "Fields to update"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  map[string]string  "Invalid input or email already in use"
// @Failure      401      {object}  map[string]string  "Not authenticated"
// @Failure      404      {object}  map[string]string  "User not found"
// @Router       /auth/updateme [patch]
func (c *AuthController) UpdateMe(ctx *gin.Context) {
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

	var updateData map[string]interface{}
	if err := ctx.ShouldBindJSON(&updateData); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	allowedFields := map[string]bool{
		"name":  true,
		"email": true,
		"photo": true,
	}

	filteredUpdate := bson.M{}
	for key, value := range updateData {
		if allowedFields[key] {
			// Defend against panic: Safe type assertion check
			strVal, ok := value.(string)
			if !ok {
				ctx.Error(utils.NewBadRequestError("Invalid value type for field: " + key))
				return
			}

			if key == "email" {
				filteredUpdate[key] = models.NormalizeEmail(strVal)
			} else if key == "name" {
				filteredUpdate[key] = models.SanitizeName(strVal)
			} else {
				filteredUpdate[key] = strVal
			}
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
		ctx.Request.Context(),
		bson.M{"_id": objID, "active": true},
		update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updatedUser)

	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("User not found"))
			return
		}
		if mongo.IsDuplicateKeyError(err) {
			ctx.Error(utils.NewBadRequestError("Email address is already in use"))
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

// UpdatePassword godoc
// @Summary      Update user password
// @Description  Allows logged-in users to update their current password and issues a new JWT token
// @Tags         auth
// @Security     Bearer
// @Accept       json
// @Produce      json
// @Param        request  body      UpdatePasswordRequest  true  "Password Update Payload"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  map[string]string  "Validation error or mismatching passwords"
// @Failure      401      {object}  map[string]string  "Current password is wrong or user unauthenticated"
// @Router       /auth/updatepassword [patch]
func (c *AuthController) UpdatePassword(ctx *gin.Context) {
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

	var req struct {
		PasswordCurrent string `json:"passwordCurrent" binding:"required"`
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

	var user models.User
	err = c.userCollection.FindOne(ctx.Request.Context(), bson.M{"_id": objID, "active": true}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("User not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	if !user.CheckPassword(req.PasswordCurrent) {
		ctx.Error(utils.NewUnauthorizedError("Your current password is wrong"))
		return
	}

	user.Password = req.Password
	user.PasswordConfirm = req.PasswordConfirm

	if err := user.BeforeSave(false); err != nil {
		ctx.Error(utils.NewBadRequestError(err.Error()))
		return
	}

	update := bson.M{"$set": bson.M{
		"password":          user.Password,
		"passwordChangedAt": user.PasswordChangedAt,
		"updatedAt":         time.Now(),
	}}

	err = c.userCollection.FindOneAndUpdate(
		ctx.Request.Context(),
		bson.M{"_id": objID},
		update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&user)

	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

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

// ForgotPassword godoc
// @Summary      Request password reset link
// @Description  Sends a password reset token via email if the account exists
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request  body      ForgotPasswordRequest  true  "Registered user email"
// @Success      200      {object}  map[string]string      "Reset link sent message"
// @Failure      400      {object}  map[string]string      "Invalid email payload"
// @Failure      500      {object}  map[string]string      "Email sending error"
// @Router       /auth/forgotpassword [post]
func (c *AuthController) ForgotPassword(ctx *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	emailStr := models.NormalizeEmail(req.Email)

	var user models.User
	err := c.userCollection.FindOne(ctx.Request.Context(), bson.M{"email": emailStr, "active": true}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			// Masking user presence to protect privacy
			ctx.JSON(200, gin.H{
				"status":  "success",
				"message": "If your email is registered, you will receive a reset link",
			})
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	resetToken, err := user.CreatePasswordResetToken()
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	update := bson.M{
		"$set": bson.M{
			"passwordResetToken":   user.PasswordResetToken,
			"passwordResetExpires": user.PasswordResetExpires,
		},
	}
	_, err = c.userCollection.UpdateOne(ctx.Request.Context(), bson.M{"_id": user.ID}, update)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	if err := c.emailSender.SendPasswordResetEmail(user.Email, resetToken); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status":  "success",
		"message": "Password reset link sent to your email",
	})
}

// ResetPassword godoc
// @Summary      Reset password with token
// @Description  Resets password using the token sent via email and returns a new JWT token
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        token    path      string                true  "Password reset token"
// @Param        request  body      ResetPasswordRequest  true  "New password details"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  map[string]string  "Invalid/expired token or password mismatch"
// @Failure      500      {object}  map[string]string  "Internal server error"
// @Router       /auth/resetpassword/{token} [patch]
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

	// FIX: Hash the token *before* hitting the DB to safely search by specific token index
	hash := sha256.Sum256([]byte(resetToken))
	hashedToken := hex.EncodeToString(hash[:])

	var user models.User
	filter := bson.M{
		"passwordResetToken":   hashedToken,
		"passwordResetExpires": bson.M{"$gt": time.Now()},
		"active":               true,
	}

	err := c.userCollection.FindOne(ctx.Request.Context(), filter).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewBadRequestError("Invalid or expired reset token"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	user.Password = req.Password
	user.PasswordConfirm = req.PasswordConfirm

	if err := user.BeforeSave(false); err != nil {
		ctx.Error(utils.NewBadRequestError(err.Error()))
		return
	}

	update := bson.M{
		"$set": bson.M{
			"password":             user.Password,
			"passwordChangedAt":    user.PasswordChangedAt,
			"passwordResetToken":   "",
			"passwordResetExpires": nil,
			"updatedAt":            time.Now(),
		},
	}

	_, err = c.userCollection.UpdateOne(ctx.Request.Context(), bson.M{"_id": user.ID}, update)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

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

// Logout godoc
// @Summary      Log out current user
// @Description  Invalidates the current JWT access token by adding it to the Redis blacklist
// @Tags         auth
// @Security     Bearer
// @Produce      json
// @Success      200  {object}  map[string]string  "Logged out successfully"
// @Failure      401  {object}  map[string]string  "Not authenticated or invalid token format"
// @Router       /auth/logout [post]
func (c *AuthController) Logout(ctx *gin.Context) {
	authHeader := ctx.GetHeader("Authorization")
	if authHeader == "" {
		ctx.Error(utils.NewUnauthorizedError("Not authenticated"))
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		ctx.Error(utils.NewUnauthorizedError("Invalid authorization format"))
		return
	}

	tokenString := parts[1]

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return []byte(c.jwtSecret), nil
	})

	if err != nil {
		ctx.JSON(200, gin.H{
			"status":  "success",
			"message": "Logged out successfully",
		})
		return
	}

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
