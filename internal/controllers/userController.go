package controllers

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/paularinzee/natour/internal/models"
	"github.com/paularinzee/natour/pkg/utils"
)

type UserController struct {
	userCollection *mongo.Collection
}

func NewUserController(db *mongo.Database) *UserController {
	return &UserController{
		userCollection: db.Collection("users"),
	}
}

// DeleteMe - DELETE /api/v1/auth/deleteme
// Soft delete the current user (set active to false)
func (c *UserController) DeleteMe(ctx *gin.Context) {
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

	// Soft delete - set active to false
	update := bson.M{
		"$set": bson.M{
			"active":    false,
			"updatedAt": time.Now(),
		},
	}

	result, err := c.userCollection.UpdateOne(context.Background(), bson.M{"_id": objID}, update)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	if result.ModifiedCount == 0 {
		ctx.Error(utils.NewNotFoundError("User not found"))
		return
	}

	ctx.JSON(204, gin.H{"status": "success", "data": nil})
}

// GetAllUsers - GET /api/v1/users
// Get all users (admin only)
func (c *UserController) GetAllUsers(ctx *gin.Context) {
	cursor, err := c.userCollection.Find(context.Background(), bson.M{"active": true})
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(context.Background())

	var users []models.User
	if err = cursor.All(context.Background(), &users); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	responses := make([]models.UserResponse, len(users))
	for i, user := range users {
		responses[i] = user.ToResponse()
	}

	ctx.JSON(200, gin.H{
		"status":  "success",
		"results": len(responses),
		"data": gin.H{
			"users": responses,
		},
	})
}

// CreateUser - POST /api/v1/users
// Create a new user (admin only)
func (c *UserController) CreateUser(ctx *gin.Context) {
	var req struct {
		Name            string `json:"name" binding:"required"`
		Email           string `json:"email" binding:"required,email"`
		Password        string `json:"password" binding:"required,min=8"`
		PasswordConfirm string `json:"passwordConfirm" binding:"required"`
		Role            string `json:"role"`
		Photo           string `json:"photo"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	if req.Password != req.PasswordConfirm {
		ctx.Error(utils.NewBadRequestError("Passwords are not the same"))
		return
	}

	// Check if user exists
	email := models.NormalizeEmail(req.Email)
	var existingUser models.User
	err := c.userCollection.FindOne(context.Background(), bson.M{"email": email}).Decode(&existingUser)
	if err == nil {
		ctx.Error(utils.NewBadRequestError("User with this email already exists"))
		return
	}

	// Create user
	user := models.User{
		ID:              primitive.NewObjectID(),
		Name:            req.Name,
		Email:           email,
		Password:        req.Password,
		PasswordConfirm: req.PasswordConfirm,
		Photo:           "default.jpg",
		Role:            models.RoleUser,
		Active:          true,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// Set role if provided
	if req.Role != "" {
		for _, role := range models.ValidRoles() {
			if models.UserRole(req.Role) == role {
				user.Role = role
				break
			}
		}
	}

	// Set photo if provided
	if req.Photo != "" {
		user.Photo = req.Photo
	}

	if err := user.BeforeSave(true); err != nil {
		ctx.Error(utils.NewBadRequestError(err.Error()))
		return
	}

	_, err = c.userCollection.InsertOne(context.Background(), user)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(201, gin.H{
		"status": "success",
		"data": gin.H{
			"user": user.ToResponse(),
		},
	})
}

// GetUser - GET /api/v1/users/:id
// Get a user by ID (admin only)
func (c *UserController) GetUser(ctx *gin.Context) {
	id := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid user ID"))
		return
	}

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

// UpdateUser - PATCH /api/v1/users/:id
// Update a user (admin only)
func (c *UserController) UpdateUser(ctx *gin.Context) {
	id := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid user ID"))
		return
	}

	var updateData bson.M
	if err := ctx.ShouldBindJSON(&updateData); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	// Allowed fields for admin update
	allowedFields := map[string]bool{
		"name":   true,
		"email":  true,
		"photo":  true,
		"role":   true,
		"active": true,
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

// DeleteUser - DELETE /api/v1/users/:id
// Hard delete a user (admin only)
func (c *UserController) DeleteUser(ctx *gin.Context) {
	id := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid user ID"))
		return
	}

	result, err := c.userCollection.DeleteOne(context.Background(), bson.M{"_id": objID})
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	if result.DeletedCount == 0 {
		ctx.Error(utils.NewNotFoundError("User not found"))
		return
	}

	ctx.JSON(204, gin.H{"status": "success", "data": nil})
}
