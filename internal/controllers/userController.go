package controllers

import (
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

type CreateUserRequest struct {
	Name            string `json:"name" binding:"required" example:"John Doe"`
	Email           string `json:"email" binding:"required,email" example:"john@example.com"`
	Password        string `json:"password" binding:"required,min=8" example:"password123"`
	PasswordConfirm string `json:"passwordConfirm" binding:"required" example:"password123"`
	Role            string `json:"role" example:"user"`
	Photo           string `json:"photo" example:"default.jpg"`
}

type UpdateUserRequest struct {
	Name   string `json:"name,omitempty" example:"John Doe"`
	Email  string `json:"email,omitempty" example:"john@example.com"`
	Photo  string `json:"photo,omitempty" example:"avatar.jpg"`
	Role   string `json:"role,omitempty" example:"guide"`
	Active bool   `json:"active,omitempty" example:"true"`
}

// DeleteMe Godoc
// @Summary      Deactivate current account
// @Description  Soft delete the currently authenticated user account
// @Tags         Users
// @Produce      json
// @Security     Bearer
// @Success      204  "No Content"
// @Failure      401  {object}  utils.AppError
// @Failure      404  {object}  utils.AppError
// @Failure      500  {object}  utils.AppError
// @Router       /auth/deleteme [delete]
func (c *UserController) DeleteMe(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()

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

	update := bson.M{
		"$set": bson.M{
			"active":    false,
			"updatedAt": time.Now(),
		},
	}

	result, err := c.userCollection.UpdateOne(reqCtx, bson.M{"_id": objID}, update)
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

// GetAllUsers Godoc
// @Summary      Get all active users
// @Description  Retrieve all active users (Admin only)
// @Tags         Users
// @Produce      json
// @Security     Bearer
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  utils.AppError
// @Failure      500  {object}  utils.AppError
// @Router       /users [get]
func (c *UserController) GetAllUsers(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()

	cursor, err := c.userCollection.Find(reqCtx, bson.M{"active": true})
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(reqCtx)

	var users []models.User
	if err = cursor.All(reqCtx, &users); err != nil {
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

// CreateUser Godoc
// @Summary      Create a user
// @Description  Manually create a new user account (Admin only)
// @Tags         Users
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        user  body      CreateUserRequest  true  "User details"
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  utils.AppError
// @Failure      401   {object}  utils.AppError
// @Failure      500   {object}  utils.AppError
// @Router       /users [post]
func (c *UserController) CreateUser(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()

	var req CreateUserRequest

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	if req.Password != req.PasswordConfirm {
		ctx.Error(utils.NewBadRequestError("Passwords are not the same"))
		return
	}

	email := models.NormalizeEmail(req.Email)
	var existingUser models.User
	err := c.userCollection.FindOne(reqCtx, bson.M{"email": email}).Decode(&existingUser)
	if err == nil {
		ctx.Error(utils.NewBadRequestError("User with this email already exists"))
		return
	}

	user := models.User{
		ID:              primitive.NewObjectID(),
		Name:            models.SanitizeName(req.Name),
		Email:           email,
		Password:        req.Password,
		PasswordConfirm: req.PasswordConfirm,
		Photo:           "default.jpg",
		Role:            models.RoleUser,
		Active:          true,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if req.Role != "" {
		isValidRole := false
		for _, role := range models.ValidRoles() {
			if models.UserRole(req.Role) == role {
				user.Role = role
				isValidRole = true
				break
			}
		}
		if !isValidRole {
			ctx.Error(utils.NewBadRequestError("Invalid user role provided"))
			return
		}
	}

	if req.Photo != "" {
		user.Photo = req.Photo
	}

	if err := user.BeforeSave(true); err != nil {
		ctx.Error(utils.NewBadRequestError(err.Error()))
		return
	}

	_, err = c.userCollection.InsertOne(reqCtx, user)
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

// GetUser Godoc
// @Summary      Get user by ID
// @Description  Get single user details by ID (Admin only)
// @Tags         Users
// @Produce      json
// @Security     Bearer
// @Param        id   path      string  true  "User ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  utils.AppError
// @Failure      401  {object}  utils.AppError
// @Failure      404  {object}  utils.AppError
// @Failure      500  {object}  utils.AppError
// @Router       /users/{id} [get]
func (c *UserController) GetUser(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()

	id := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid user ID"))
		return
	}

	var user models.User
	err = c.userCollection.FindOne(reqCtx, bson.M{"_id": objID, "active": true}).Decode(&user)
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

// UpdateUser Godoc
// @Summary      Update user
// @Description  Update single user record by ID (Admin only)
// @Tags         Users
// @Accept       json
// @Produce      json
// @Security     Bearer
// @Param        id    path      string             true  "User ID"
// @Param        user  body      UpdateUserRequest  true  "Partial user attributes"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  utils.AppError
// @Failure      401   {object}  utils.AppError
// @Failure      404   {object}  utils.AppError
// @Failure      500   {object}  utils.AppError
// @Router       /users/{id} [patch]
func (c *UserController) UpdateUser(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()

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
				if emailStr, ok := value.(string); ok {
					value = models.NormalizeEmail(emailStr)
				}
			}
			if key == "name" {
				if nameStr, ok := value.(string); ok {
					value = models.SanitizeName(nameStr)
				}
			}
			if key == "role" {
				roleStr, ok := value.(string)
				if !ok {
					ctx.Error(utils.NewBadRequestError("Invalid role value format"))
					return
				}
				isValidRole := false
				for _, role := range models.ValidRoles() {
					if models.UserRole(roleStr) == role {
						isValidRole = true
						break
					}
				}
				if !isValidRole {
					ctx.Error(utils.NewBadRequestError("Invalid user role provided"))
					return
				}
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
		reqCtx,
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

// DeleteUser Godoc
// @Summary      Delete user
// @Description  Permanently hard delete a user account from database (Admin only)
// @Tags         Users
// @Produce      json
// @Security     Bearer
// @Param        id   path      string  true  "User ID"
// @Success      204  "No Content"
// @Failure      400  {object}  utils.AppError
// @Failure      401  {object}  utils.AppError
// @Failure      404  {object}  utils.AppError
// @Failure      500  {object}  utils.AppError
// @Router       /users/{id} [delete]
func (c *UserController) DeleteUser(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()

	id := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid user ID"))
		return
	}

	result, err := c.userCollection.DeleteOne(reqCtx, bson.M{"_id": objID})
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
