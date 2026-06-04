package controllers

import (
	"context"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/paularinzee/natour/internal/models"
	"github.com/paularinzee/natour/pkg/utils"
)

type ReviewController struct {
	reviewCollection *mongo.Collection
	tourCollection   *mongo.Collection
	userCollection   *mongo.Collection
}

func NewReviewController(db *mongo.Database) *ReviewController {
	return &ReviewController{
		reviewCollection: db.Collection("reviews"),
		tourCollection:   db.Collection("tours"),
		userCollection:   db.Collection("users"),
	}
}

// CreateReview - POST /api/v1/reviews
// @Summary Create a review
// @Description Create a new review for a tour
// @Tags Reviews
// @Accept json
// @Produce json
// @Param review body models.Review true "Review object"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /reviews [post]

// CreateReview - POST /api/v1/reviews
func (c *ReviewController) CreateReview(ctx *gin.Context) {
	// Get tour ID from URL parameter (from nested route)
	tourIDParam := ctx.Param("id")
	if tourIDParam == "" {
		ctx.Error(utils.NewBadRequestError("Tour ID is required"))
		return
	}

	tourObjID, err := primitive.ObjectIDFromHex(tourIDParam)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid tour ID"))
		return
	}

	// Parse review from JSON body
	var req struct {
		Review string `json:"review" binding:"required"`
		Rating int    `json:"rating" binding:"min=1,max=5"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

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

	userObjID, err := primitive.ObjectIDFromHex(userIDStr)
	if err != nil {
		ctx.Error(utils.NewUnauthorizedError("Invalid user ID format"))
		return
	}

	// Validate tour exists
	var tour models.Tour
	err = c.tourCollection.FindOne(context.Background(), bson.M{"_id": tourObjID}).Decode(&tour)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("Tour not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Check if user has already reviewed this tour
	var existingReview models.Review
	filter := bson.M{
		"tour": tourObjID,
		"user": userObjID,
	}

	err = c.reviewCollection.FindOne(context.Background(), filter).Decode(&existingReview)
	if err == nil {
		ctx.Error(utils.NewBadRequestError("You have already reviewed this tour"))
		return
	}

	// Create review
	review := models.Review{
		ID:        primitive.NewObjectID(),
		Review:    req.Review,
		Rating:    req.Rating,
		TourID:    tourObjID,
		UserID:    userObjID,
		CreatedAt: time.Now(),
	}

	if err := review.BeforeSave(); err != nil {
		ctx.Error(utils.NewBadRequestError(err.Error()))
		return
	}

	// Create review
	_, err = c.reviewCollection.InsertOne(context.Background(), review)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Update tour ratings
	if err := c.updateTourRatings(review.TourID); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Populate user info for response
	var user models.User
	c.userCollection.FindOne(context.Background(), bson.M{"_id": review.UserID}).Decode(&user)
	review.User = &user

	ctx.JSON(201, gin.H{
		"status": "success",
		"data": gin.H{
			"review": review.ToResponse(),
		},
	})
}

// GetAllReviews - GET /api/v1/reviews
// @Summary Get all reviews
// @Description Get list of all reviews with pagination
// @Tags Reviews
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(100)
// @Success 200 {object} map[string]interface{}
// @Router /reviews [get]
func (c *ReviewController) GetAllReviews(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(ctx.DefaultQuery("limit", "100"))
	skip := (page - 1) * limit

	findOptions := options.Find().
		SetSkip(int64(skip)).
		SetLimit(int64(limit)).
		SetSort(bson.D{{Key: "createdAt", Value: -1}})

	cursor, err := c.reviewCollection.Find(context.Background(), bson.M{}, findOptions)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(context.Background())

	var reviews []models.Review
	if err = cursor.All(context.Background(), &reviews); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Populate user data
	for i := range reviews {
		var user models.User
		err := c.userCollection.FindOne(context.Background(), bson.M{"_id": reviews[i].UserID}).Decode(&user)
		if err == nil {
			reviews[i].User = &user
		}
	}

	responses := make([]models.ReviewResponse, len(reviews))
	for i, review := range reviews {
		responses[i] = review.ToResponse()
	}

	ctx.JSON(200, gin.H{
		"status":  "success",
		"results": len(responses),
		"data": gin.H{
			"reviews": responses,
		},
	})
}

// GetTourReviews - GET /api/v1/reviews/tour/:tourId
// @Summary Get reviews for a specific tour
// @Description Get all reviews for a tour
// @Tags Reviews
// @Param tourId path string true "Tour ID"
// @Success 200 {object} map[string]interface{}
// @Router /reviews/tour/{tourId} [get]
func (c *ReviewController) GetTourReviews(ctx *gin.Context) {
	// Get tour ID from URL parameter
	tourIDParam := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(tourIDParam)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid tour ID"))
		return
	}

	cursor, err := c.reviewCollection.Find(context.Background(), bson.M{"tour": objID})
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(context.Background())

	var reviews []models.Review
	if err = cursor.All(context.Background(), &reviews); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Populate user data
	for i := range reviews {
		var user models.User
		c.userCollection.FindOne(context.Background(), bson.M{"_id": reviews[i].UserID}).Decode(&user)
		reviews[i].User = &user
	}

	responses := make([]models.ReviewResponse, len(reviews))
	for i, review := range reviews {
		responses[i] = review.ToResponse()
	}

	ctx.JSON(200, gin.H{
		"status":  "success",
		"results": len(responses),
		"data": gin.H{
			"reviews": responses,
		},
	})
}

// GetReview - GET /api/v1/reviews/:id
// @Summary Get single review
// @Description Get review by ID
// @Tags Reviews
// @Param id path string true "Review ID"
// @Success 200 {object} map[string]interface{}
// @Router /reviews/{id} [get]
func (c *ReviewController) GetReview(ctx *gin.Context) {
	id := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid review ID"))
		return
	}

	var review models.Review
	err = c.reviewCollection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&review)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("Review not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Populate user
	var user models.User
	err = c.userCollection.FindOne(context.Background(), bson.M{"_id": review.UserID}).Decode(&user)
	if err == nil {
		review.User = &user
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"data": gin.H{
			"review": review.ToResponse(),
		},
	})
}

// UpdateReview - PATCH /api/v1/reviews/:id
// @Summary Update review
// @Description Update review by ID (only review text and rating)
// @Tags Reviews
// @Param id path string true "Review ID"
// @Param review body object true "Update fields"
// @Success 200 {object} map[string]interface{}
// @Router /reviews/{id} [patch]
func (c *ReviewController) UpdateReview(ctx *gin.Context) {
	id := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid review ID"))
		return
	}

	// Get current user ID
	userID, _ := ctx.Get("userID")
	currentUserID, _ := primitive.ObjectIDFromHex(userID.(string))

	// Check if review exists and belongs to user
	var existingReview models.Review
	err = c.reviewCollection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&existingReview)
	if err != nil {
		ctx.Error(utils.NewNotFoundError("Review not found"))
		return
	}

	// Check ownership (only review owner or admin can update)
	userRole, _ := ctx.Get("userRole")
	if existingReview.UserID != currentUserID && userRole != "admin" {
		ctx.Error(utils.NewUnauthorizedError("You can only update your own reviews"))
		return
	}

	var updateData bson.M
	if err := ctx.ShouldBindJSON(&updateData); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	// Only allow updating review and rating
	allowedFields := map[string]bool{
		"review": true,
		"rating": true,
	}

	filteredUpdate := bson.M{}
	for key, value := range updateData {
		if allowedFields[key] {
			filteredUpdate[key] = value
		}
	}

	if len(filteredUpdate) == 0 {
		ctx.Error(utils.NewBadRequestError("No valid fields to update"))
		return
	}

	update := bson.M{"$set": filteredUpdate}

	var updatedReview models.Review
	err = c.reviewCollection.FindOneAndUpdate(
		context.Background(),
		bson.M{"_id": objID},
		update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updatedReview)

	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Update tour ratings
	if err := c.updateTourRatings(updatedReview.TourID); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"data": gin.H{
			"review": updatedReview.ToResponse(),
		},
	})
}

// DeleteReview - DELETE /api/v1/reviews/:id
// @Summary Delete review
// @Description Delete review by ID
// @Tags Reviews
// @Param id path string true "Review ID"
// @Success 204 {object} nil
// @Router /reviews/{id} [delete]
func (c *ReviewController) DeleteReview(ctx *gin.Context) {
	id := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid review ID"))
		return
	}

	// Get current user ID and role
	userID, _ := ctx.Get("userID")
	currentUserID, _ := primitive.ObjectIDFromHex(userID.(string))
	userRole, _ := ctx.Get("userRole")

	// Check if review exists and belongs to user
	var review models.Review
	err = c.reviewCollection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&review)
	if err != nil {
		ctx.Error(utils.NewNotFoundError("Review not found"))
		return
	}

	// Check ownership (only review owner or admin can delete)
	if review.UserID != currentUserID && userRole != "admin" {
		ctx.Error(utils.NewUnauthorizedError("You can only delete your own reviews"))
		return
	}

	result, err := c.reviewCollection.DeleteOne(context.Background(), bson.M{"_id": objID})
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	if result.DeletedCount == 0 {
		ctx.Error(utils.NewNotFoundError("Review not found"))
		return
	}

	// Update tour ratings
	if err := c.updateTourRatings(review.TourID); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(204, gin.H{"status": "success", "data": nil})
}

// updateTourRatings calculates and updates tour average rating
func (c *ReviewController) updateTourRatings(tourID primitive.ObjectID) error {
	// Aggregate reviews for this tour
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"tour": tourID}}},
		{{Key: "$group", Value: bson.M{
			"_id":       "$tour",
			"nRating":   bson.M{"$sum": 1},
			"avgRating": bson.M{"$avg": "$rating"},
		}}},
	}

	cursor, err := c.reviewCollection.Aggregate(context.Background(), pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(context.Background())

	var stats []struct {
		ID        primitive.ObjectID `bson:"_id"`
		NRating   int                `bson:"nRating"`
		AvgRating float64            `bson:"avgRating"`
	}

	if err = cursor.All(context.Background(), &stats); err != nil {
		return err
	}

	var update bson.M
	if len(stats) > 0 {
		update = bson.M{
			"$set": bson.M{
				"ratingsQuantity": stats[0].NRating,
				"ratingsAverage":  stats[0].AvgRating,
			},
		}
	} else {
		update = bson.M{
			"$set": bson.M{
				"ratingsQuantity": 0,
				"ratingsAverage":  4.5,
			},
		}
	}

	_, err = c.tourCollection.UpdateOne(context.Background(), bson.M{"_id": tourID}, update)
	return err
}
