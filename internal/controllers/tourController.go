package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/paularinzee/natour/internal/middleware"
	"github.com/paularinzee/natour/internal/models"
	"github.com/paularinzee/natour/pkg/utils"
)

type TourController struct {
	collection *mongo.Collection
}

func NewTourController(db *mongo.Database) *TourController {
	return &TourController{
		collection: db.Collection("tours"),
	}
}

// AliasTopTours modifies query params for top tours
func (c *TourController) AliasTopTours(ctx *gin.Context) {
	ctx.Set("alias", "top")
	ctx.Next()
}

// GetUploadedImages retrieves uploaded images from context (set by middleware)
func GetUploadedImages(ctx *gin.Context) *middleware.ImageUploadResult {
	if val, exists := ctx.Get("uploadedImages"); exists {
		return val.(*middleware.ImageUploadResult)
	}
	return &middleware.ImageUploadResult{
		Images: []string{},
	}
}

// GetAllTours - GET /api/v1/tours
// GetAllTours godoc
// @Summary Get all tours
// @Description Get list of all tours with filtering, sorting, and pagination
// @Tags Tours
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(100)
// @Param sort query string false "Sort by field (prefix - for descending)"
// @Param fields query string false "Comma separated fields to return"
// @Success 200 {object} map[string]interface{}
// @Router /tours [get]
func (c *TourController) GetAllTours(ctx *gin.Context) {
	// Build filter
	filter := bson.M{"secretTour": false}

	// Build options
	findOptions := options.Find()

	// Fields
	if fields := ctx.Query("fields"); fields != "" {
		projection := bson.M{}
		for _, field := range strings.Split(fields, ",") {
			projection[field] = 1
		}
		findOptions.SetProjection(projection)
	}

	// Sort
	if sort := ctx.Query("sort"); sort != "" {
		sortFields := bson.D{}
		for _, field := range strings.Split(sort, ",") {
			if strings.HasPrefix(field, "-") {
				sortFields = append(sortFields, bson.E{Key: field[1:], Value: -1})
			} else {
				sortFields = append(sortFields, bson.E{Key: field, Value: 1})
			}
		}
		findOptions.SetSort(sortFields)
	} else {
		findOptions.SetSort(bson.D{{Key: "createdAt", Value: -1}})
	}

	// Pagination
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(ctx.DefaultQuery("limit", "100"))
	skip := (page - 1) * limit

	findOptions.SetSkip(int64(skip))
	findOptions.SetLimit(int64(limit))

	// Check if alias is set
	if alias := ctx.GetString("alias"); alias == "top" {
		findOptions.SetSort(bson.D{{Key: "ratingsAverage", Value: -1}, {Key: "price", Value: 1}})
		findOptions.SetLimit(5)
	}

	cursor, err := c.collection.Find(context.Background(), filter, findOptions)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(context.Background())

	var tours []models.Tour
	if err = cursor.All(context.Background(), &tours); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status":  "success",
		"results": len(tours),
		"data":    gin.H{"tours": tours},
	})
}

// GetTour - GET /api/v1/tours/:id

func (c *TourController) GetTour(ctx *gin.Context) {
	id := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid tour ID"))
		return
	}

	filter := bson.M{"_id": objID, "secretTour": false}

	var tour models.Tour
	err = c.collection.FindOne(context.Background(), filter).Decode(&tour)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("Tour not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Populate reviews for this tour
	reviewCollection := c.collection.Database().Collection("reviews")
	cursor, err := reviewCollection.Find(context.Background(), bson.M{"tour": objID})
	if err == nil {
		defer cursor.Close(context.Background())
		var reviews []models.Review
		if err = cursor.All(context.Background(), &reviews); err == nil {
			// Populate user data for each review
			userCollection := c.collection.Database().Collection("users")
			for i := range reviews {
				var user models.User
				userCollection.FindOne(context.Background(), bson.M{"_id": reviews[i].UserID}).Decode(&user)
				reviews[i].User = &user
			}
			tour.Reviews = reviews
		}
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"data":   gin.H{"tour": tour.ToResponse()},
	})
}

// CreateTour - POST /api/v1/tours

func (c *TourController) CreateTour(ctx *gin.Context) {
	var tour models.Tour

	// Check content type to determine how to parse
	contentType := ctx.GetHeader("Content-Type")

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Handle multipart form with images
		uploadedImages := GetUploadedImages(ctx)

		// Bind form values
		tour.Name = ctx.PostForm("name")
		tour.Duration, _ = strconv.Atoi(ctx.PostForm("duration"))
		tour.Price, _ = strconv.ParseFloat(ctx.PostForm("price"), 64)
		tour.Difficulty = ctx.PostForm("difficulty")
		tour.Summary = ctx.PostForm("summary")
		tour.Description = ctx.PostForm("description")
		tour.MaxGroupSize, _ = strconv.Atoi(ctx.PostForm("maxGroupSize"))

		// Handle price discount if provided
		if priceDiscount := ctx.PostForm("priceDiscount"); priceDiscount != "" {
			tour.PriceDiscount, _ = strconv.ParseFloat(priceDiscount, 64)
		}

		// Set image paths from uploaded files
		if uploadedImages.ImageCover != "" {
			tour.ImageCover = uploadedImages.ImageCover
		}
		if len(uploadedImages.Images) > 0 {
			tour.Images = uploadedImages.Images
		}

		// Handle start location if provided as JSON string
		if startLocation := ctx.PostForm("startLocation"); startLocation != "" {
			var location models.StartLocation
			if err := json.Unmarshal([]byte(startLocation), &location); err == nil {
				tour.StartLocation = location
			}
		}

		// Handle locations array if provided as JSON string
		if locations := ctx.PostForm("locations"); locations != "" {
			var locs []models.Location
			if err := json.Unmarshal([]byte(locations), &locs); err == nil {
				tour.Locations = locs
			}
		}

		// Handle start dates if provided as JSON string
		if startDates := ctx.PostForm("startDates"); startDates != "" {
			var dates []time.Time
			if err := json.Unmarshal([]byte(startDates), &dates); err == nil {
				tour.StartDates = dates
			}
		}

	} else {
		// Handle JSON request
		if err := ctx.ShouldBindJSON(&tour); err != nil {
			ctx.Error(utils.NewBadRequestError("Invalid request body: " + err.Error()))
			return
		}
	}

	// Generate slug and set defaults
	tour.Slug = models.GenerateSlug(tour.Name)
	tour.CreatedAt = time.Now()
	tour.RatingsAverage = models.DefaultRating
	tour.RatingsQuantity = 0
	tour.ID = primitive.NewObjectID()

	// Validate tour
	if err := validateTour(tour); err != nil {
		ctx.Error(utils.NewBadRequestError(err.Error()))
		return
	}

	result, err := c.collection.InsertOne(context.Background(), tour)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	tour.ID = result.InsertedID.(primitive.ObjectID)

	ctx.JSON(201, gin.H{
		"status": "success",
		"data":   gin.H{"tour": tour},
	})
}

// UpdateTour - PATCH /api/v1/tours/:id

func (c *TourController) UpdateTour(ctx *gin.Context) {
	id := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid tour ID"))
		return
	}

	updateData := bson.M{}

	// Check content type to determine how to parse
	contentType := ctx.GetHeader("Content-Type")

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Handle multipart form with images

		// Get uploaded images from middleware
		if uploadedImages, exists := ctx.Get("uploadedImages"); exists {
			images := uploadedImages.(*middleware.ImageUploadResult)

			// Delete old images before updating with new ones
			if images.ImageCover != "" || len(images.Images) > 0 {
				if err := c.replaceImages(ctx, objID, images.ImageCover, images.Images); err != nil {
					// Log error but continue
					fmt.Printf("Warning: Could not delete old images: %v\n", err)
				}
			}

			if images.ImageCover != "" {
				updateData["imageCover"] = images.ImageCover
			}
			if len(images.Images) > 0 {
				updateData["images"] = images.Images
			}
		}

		// Handle regular form fields
		if name := ctx.PostForm("name"); name != "" {
			updateData["name"] = name
			updateData["slug"] = models.GenerateSlug(name)
		}
		if duration := ctx.PostForm("duration"); duration != "" {
			if d, err := strconv.Atoi(duration); err == nil {
				updateData["duration"] = d
			}
		}
		if price := ctx.PostForm("price"); price != "" {
			if p, err := strconv.ParseFloat(price, 64); err == nil {
				updateData["price"] = p
			}
		}
		if difficulty := ctx.PostForm("difficulty"); difficulty != "" {
			updateData["difficulty"] = difficulty
		}
		if summary := ctx.PostForm("summary"); summary != "" {
			updateData["summary"] = summary
		}
		if description := ctx.PostForm("description"); description != "" {
			updateData["description"] = description
		}
		if maxGroupSize := ctx.PostForm("maxGroupSize"); maxGroupSize != "" {
			if m, err := strconv.Atoi(maxGroupSize); err == nil {
				updateData["maxGroupSize"] = m
			}
		}
		if priceDiscount := ctx.PostForm("priceDiscount"); priceDiscount != "" {
			if pd, err := strconv.ParseFloat(priceDiscount, 64); err == nil {
				updateData["priceDiscount"] = pd
			}
		}
		if ratingsAverage := ctx.PostForm("ratingsAverage"); ratingsAverage != "" {
			if ra, err := strconv.ParseFloat(ratingsAverage, 64); err == nil {
				updateData["ratingsAverage"] = ra
			}
		}

		// Handle JSON string fields
		if startLocation := ctx.PostForm("startLocation"); startLocation != "" {
			var location models.StartLocation
			if err := json.Unmarshal([]byte(startLocation), &location); err == nil {
				updateData["startLocation"] = location
			}
		}
		if locations := ctx.PostForm("locations"); locations != "" {
			var locs []models.Location
			if err := json.Unmarshal([]byte(locations), &locs); err == nil {
				updateData["locations"] = locs
			}
		}
		if startDates := ctx.PostForm("startDates"); startDates != "" {
			var dates []time.Time
			if err := json.Unmarshal([]byte(startDates), &dates); err == nil {
				updateData["startDates"] = dates
			}
		}

	} else {
		// Handle JSON request
		var jsonData map[string]interface{}
		if err := ctx.ShouldBindJSON(&jsonData); err != nil {
			ctx.Error(utils.NewBadRequestError("Invalid request body: " + err.Error()))
			return
		}

		// If name is updated, update slug too
		if name, ok := jsonData["name"]; ok {
			jsonData["slug"] = models.GenerateSlug(name.(string))
		}

		updateData = jsonData
	}

	if len(updateData) == 0 {
		ctx.Error(utils.NewBadRequestError("No update data provided"))
		return
	}

	update := bson.M{"$set": updateData}

	result := c.collection.FindOneAndUpdate(
		context.Background(),
		bson.M{"_id": objID},
		update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)

	var tour models.Tour
	if err := result.Decode(&tour); err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("Tour not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"data":   gin.H{"tour": tour},
	})
}

// replaceImages deletes old images when updating with new ones
func (c *TourController) replaceImages(ctx *gin.Context, tourID primitive.ObjectID, newImageCover string, newImages []string) error {
	// Get existing tour
	var existingTour models.Tour
	err := c.collection.FindOne(context.Background(), bson.M{"_id": tourID}).Decode(&existingTour)
	if err != nil {
		return err
	}

	// Delete old images if they are being replaced
	if newImageCover != "" && existingTour.ImageCover != "" {
		utils.DeleteImages(existingTour.ImageCover)
	}

	if len(newImages) > 0 && len(existingTour.Images) > 0 {
		utils.DeleteImages(existingTour.Images...)
	}

	return nil
}

// DeleteTour - DELETE /api/v1/tours/:id

func (c *TourController) DeleteTour(ctx *gin.Context) {
	id := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid tour ID"))
		return
	}

	// First, find the tour to get image filenames
	var tour models.Tour
	err = c.collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&tour)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("Tour not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Delete associated images from file system
	utils.DeleteImages(append([]string{tour.ImageCover}, tour.Images...)...)

	// Delete the tour from database
	result, err := c.collection.DeleteOne(context.Background(), bson.M{"_id": objID})
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	if result.DeletedCount == 0 {
		ctx.Error(utils.NewNotFoundError("Tour not found"))
		return
	}

	ctx.JSON(204, gin.H{"status": "success", "data": nil})
}

// CleanupOrphanedImages - DELETE /api/v1/admin/tours/cleanup/images

func (c *TourController) CleanupOrphanedImages(ctx *gin.Context) {
	// Get all tours
	var tours []models.Tour
	cursor, err := c.collection.Find(context.Background(), bson.M{})
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(context.Background())

	if err = cursor.All(context.Background(), &tours); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Build a set of referenced images
	referencedImages := make(map[string]bool)
	for _, tour := range tours {
		if tour.ImageCover != "" {
			referencedImages[tour.ImageCover] = true
		}
		for _, img := range tour.Images {
			referencedImages[img] = true
		}
	}

	// Read all files in upload directory
	uploadPath := "public/img/tours"
	files, err := os.ReadDir(uploadPath)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Delete orphaned images
	deletedCount := 0
	for _, file := range files {
		if !file.IsDir() {
			filename := file.Name()
			if !referencedImages[filename] {
				filePath := filepath.Join(uploadPath, filename)
				if err := os.Remove(filePath); err == nil {
					deletedCount++
				}
			}
		}
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"data": gin.H{
			"deletedCount": deletedCount,
			"message":      fmt.Sprintf("Deleted %d orphaned images", deletedCount),
		},
	})
}

// GetTourStats - GET /api/v1/tours/stats

func (c *TourController) GetTourStats(ctx *gin.Context) {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"ratingsAverage": bson.M{"$gte": 4.5}}}},
		{{Key: "$group", Value: bson.M{
			"_id":        bson.M{"$toUpper": "$difficulty"},
			"numTours":   bson.M{"$sum": 1},
			"numRatings": bson.M{"$sum": "$ratingsQuantity"},
			"avgRating":  bson.M{"$avg": "$ratingsAverage"},
			"avgPrice":   bson.M{"$avg": "$price"},
			"minPrice":   bson.M{"$min": "$price"},
			"maxPrice":   bson.M{"$max": "$price"},
		}}},
		{{Key: "$sort", Value: bson.M{"avgPrice": 1}}},
	}

	cursor, err := c.collection.Aggregate(context.Background(), pipeline)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(context.Background())

	var stats []bson.M
	if err = cursor.All(context.Background(), &stats); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"data":   gin.H{"stats": stats},
	})
}

// GetMonthlyPlan - GET /api/v1/tours/monthly-plan/:year

func (c *TourController) GetMonthlyPlan(ctx *gin.Context) {
	year, err := strconv.Atoi(ctx.Param("year"))
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid year"))
		return
	}

	startDate := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(year, 12, 31, 23, 59, 59, 0, time.UTC)

	pipeline := mongo.Pipeline{
		{{Key: "$unwind", Value: "$startDates"}},
		{{Key: "$match", Value: bson.M{
			"startDates": bson.M{
				"$gte": startDate,
				"$lte": endDate,
			},
		}}},
		{{Key: "$group", Value: bson.M{
			"_id":           bson.M{"$month": "$startDates"},
			"numTourStarts": bson.M{"$sum": 1},
			"tours":         bson.M{"$push": "$name"},
		}}},
		{{Key: "$addFields", Value: bson.M{"month": "$_id"}}},
		{{Key: "$project", Value: bson.M{"_id": 0}}},
		{{Key: "$sort", Value: bson.M{"numTourStarts": -1}}},
		{{Key: "$limit", Value: 12}},
	}

	cursor, err := c.collection.Aggregate(context.Background(), pipeline)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(context.Background())

	var plan []bson.M
	if err = cursor.All(context.Background(), &plan); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"result": len(plan),
		"data":   gin.H{"plan": plan},
	})
}

// GetToursWithin - GET /api/v1/tours-within/:distance/center/:latlng/unit/:unit

func (c *TourController) GetToursWithin(ctx *gin.Context) {
	distance, err := strconv.ParseFloat(ctx.Param("distance"), 64)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid distance"))
		return
	}

	latlng := ctx.Param("latlng")
	unit := ctx.Param("unit")

	parts := strings.Split(latlng, ",")
	if len(parts) != 2 {
		ctx.Error(utils.NewBadRequestError("Please provide latitude and longitude in the format lat,lng"))
		return
	}

	lat, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid latitude"))
		return
	}

	lng, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid longitude"))
		return
	}

	var radius float64
	if unit == "mi" {
		radius = distance / 3963.2
	} else {
		radius = distance / 6378.1
	}

	filter := bson.M{
		"startLocation": bson.M{
			"$geoWithin": bson.M{
				"$centerSphere": []interface{}{[]float64{lng, lat}, radius},
			},
		},
	}

	cursor, err := c.collection.Find(context.Background(), filter)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(context.Background())

	var tours []models.Tour
	if err = cursor.All(context.Background(), &tours); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status":  "success",
		"results": len(tours),
		"data":    gin.H{"tours": tours},
	})
}

// GetDistances - GET /api/v1/distances/:latlng/unit/:unit

func (c *TourController) GetDistances(ctx *gin.Context) {
	latlng := ctx.Param("latlng")
	unit := ctx.Param("unit")

	parts := strings.Split(latlng, ",")
	if len(parts) != 2 {
		ctx.Error(utils.NewBadRequestError("Please provide latitude and longitude in the format lat,lng"))
		return
	}

	lat, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid latitude"))
		return
	}

	lng, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid longitude"))
		return
	}

	var multiplier float64
	if unit == "mi" {
		multiplier = 0.000621371
	} else {
		multiplier = 0.001
	}

	pipeline := mongo.Pipeline{
		{{Key: "$geoNear", Value: bson.M{
			"near": bson.M{
				"type":        "Point",
				"coordinates": []float64{lng, lat},
			},
			"distanceField":      "distance",
			"distanceMultiplier": multiplier,
		}}},
		{{Key: "$project", Value: bson.M{
			"distance": 1,
			"name":     1,
		}}},
	}

	cursor, err := c.collection.Aggregate(context.Background(), pipeline)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(context.Background())

	var distances []bson.M
	if err = cursor.All(context.Background(), &distances); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"data":   gin.H{"distances": distances},
	})
}

// Helper validation function
func validateTour(tour models.Tour) error {
	if len(tour.Name) < models.MinNameLength {
		return fmt.Errorf("tour name must have more or equal than %d characters", models.MinNameLength)
	}
	if len(tour.Name) > models.MaxNameLength {
		return fmt.Errorf("tour name must have less or equal than %d characters", models.MaxNameLength)
	}

	validDifficulty := false
	for _, d := range models.ValidDifficulties {
		if tour.Difficulty == d {
			validDifficulty = true
			break
		}
	}
	if !validDifficulty {
		return fmt.Errorf("difficulty must be easy, medium, or difficult")
	}

	if tour.RatingsAverage < models.MinRating || tour.RatingsAverage > models.MaxRating {
		return fmt.Errorf("rating must be between %.1f and %.1f", models.MinRating, models.MaxRating)
	}

	if tour.PriceDiscount > 0 && !models.ValidatePriceDiscount(tour.Price, tour.PriceDiscount) {
		return fmt.Errorf("discount price must be below regular price")
	}

	return nil
}
