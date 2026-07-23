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

// GetAllTours Godoc
// @Summary      Get all tours
// @Description  Get a paginated, sorted, and filtered list of tours
// @Tags         Tours
// @Produce      json
// @Param        page    query     int     false  "Page number" default(1)
// @Param        limit   query     int     false  "Number of items per page" default(100)
// @Param        sort    query     string  false  "Sort fields (e.g. -price,ratingsAverage)"
// @Param        fields  query     string  false  "Comma separated fields to return"
// @Success      200     {object}  map[string]interface{}
// @Failure      500     {object}  utils.AppError
// @Router       /tours [get]
func (c *TourController) GetAllTours(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()
	filter := bson.M{"secretTour": false}
	findOptions := options.Find()

	if fields := ctx.Query("fields"); fields != "" {
		projection := bson.M{}
		for _, field := range strings.Split(fields, ",") {
			projection[field] = 1
		}
		findOptions.SetProjection(projection)
	}

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

	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(ctx.DefaultQuery("limit", "100"))
	skip := (page - 1) * limit

	findOptions.SetSkip(int64(skip))
	findOptions.SetLimit(int64(limit))

	if alias := ctx.GetString("alias"); alias == "top" {
		findOptions.SetSort(bson.D{{Key: "ratingsAverage", Value: -1}, {Key: "price", Value: 1}})
		findOptions.SetLimit(5)
	}

	cursor, err := c.collection.Find(reqCtx, filter, findOptions)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(reqCtx)

	var tours []models.Tour
	if err = cursor.All(reqCtx, &tours); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status":  "success",
		"results": len(tours),
		"data":    gin.H{"tours": tours},
	})
}

// GetTour Godoc
// @Summary      Get tour by ID
// @Description  Get a single tour by ID including associated user reviews
// @Tags         Tours
// @Produce      json
// @Param        id   path      string  true  "Tour ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  utils.AppError
// @Failure      404  {object}  utils.AppError
// @Failure      500  {object}  utils.AppError
// @Router       /tours/{id} [get]
func (c *TourController) GetTour(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()
	id := ctx.Param("id")

	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid tour ID"))
		return
	}

	filter := bson.M{"_id": objID, "secretTour": false}

	var tour models.Tour
	err = c.collection.FindOne(reqCtx, filter).Decode(&tour)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("Tour not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	reviewCollection := c.collection.Database().Collection("reviews")
	cursor, err := reviewCollection.Find(reqCtx, bson.M{"tour": objID})
	if err == nil {
		defer cursor.Close(reqCtx)
		var reviews []models.Review
		if err = cursor.All(reqCtx, &reviews); err == nil && len(reviews) > 0 {
			userIDs := make([]primitive.ObjectID, 0, len(reviews))
			userIDMap := make(map[primitive.ObjectID]bool)

			for _, review := range reviews {
				if !userIDMap[review.UserID] {
					userIDMap[review.UserID] = true
					userIDs = append(userIDs, review.UserID)
				}
			}

			userCollection := c.collection.Database().Collection("users")
			userCursor, err := userCollection.Find(reqCtx, bson.M{"_id": bson.M{"$in": userIDs}})
			if err == nil {
				defer userCursor.Close(reqCtx)
				var users []models.User
				if err = userCursor.All(reqCtx, &users); err == nil {
					userMap := make(map[primitive.ObjectID]models.User, len(users))
					for _, user := range users {
						userMap[user.ID] = user
					}

					for i := range reviews {
						if u, ok := userMap[reviews[i].UserID]; ok {
							reviews[i].User = &u
						}
					}
				}
			}
			tour.Reviews = reviews
		}
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"data":   gin.H{"tour": tour.ToResponse()},
	})
}

// CreateTour Godoc
// @Summary      Create a new tour
// @Description  Create a tour via JSON or multipart/form-data
// @Tags         Tours
// @Accept       json,multipart/form-data
// @Produce      json
// @Security     Bearer
// @Param        tour  body      models.Tour  false  "Tour payload (if JSON)"
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  utils.AppError
// @Failure      401   {object}  utils.AppError
// @Failure      500   {object}  utils.AppError
// @Router       /tours/tours [post]
func (c *TourController) CreateTour(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()
	var tour models.Tour

	contentType := ctx.GetHeader("Content-Type")

	if strings.HasPrefix(contentType, "multipart/form-data") {
		uploadedImages := GetUploadedImages(ctx)

		tour.Name = ctx.PostForm("name")
		tour.Duration, _ = strconv.Atoi(ctx.PostForm("duration"))
		tour.Price, _ = strconv.ParseFloat(ctx.PostForm("price"), 64)
		tour.Difficulty = ctx.PostForm("difficulty")
		tour.Summary = ctx.PostForm("summary")
		tour.Description = ctx.PostForm("description")
		tour.MaxGroupSize, _ = strconv.Atoi(ctx.PostForm("maxGroupSize"))

		if priceDiscount := ctx.PostForm("priceDiscount"); priceDiscount != "" {
			tour.PriceDiscount, _ = strconv.ParseFloat(priceDiscount, 64)
		}

		if uploadedImages.ImageCover != "" {
			tour.ImageCover = uploadedImages.ImageCover
		}
		if len(uploadedImages.Images) > 0 {
			tour.Images = uploadedImages.Images
		}

		if startLocation := ctx.PostForm("startLocation"); startLocation != "" {
			var location models.StartLocation
			if err := json.Unmarshal([]byte(startLocation), &location); err == nil {
				tour.StartLocation = location
			}
		}

		if locations := ctx.PostForm("locations"); locations != "" {
			var locs []models.Location
			if err := json.Unmarshal([]byte(locations), &locs); err == nil {
				tour.Locations = locs
			}
		}

		if startDates := ctx.PostForm("startDates"); startDates != "" {
			var dates []time.Time
			if err := json.Unmarshal([]byte(startDates), &dates); err == nil {
				tour.StartDates = dates
			}
		}
	} else {
		if err := ctx.ShouldBindJSON(&tour); err != nil {
			ctx.Error(utils.NewBadRequestError("Invalid request body: " + err.Error()))
			return
		}
	}

	tour.Slug = models.GenerateSlug(tour.Name)
	tour.CreatedAt = time.Now()
	tour.RatingsAverage = models.DefaultRating
	tour.RatingsQuantity = 0
	tour.ID = primitive.NewObjectID()

	if err := validateTour(tour); err != nil {
		ctx.Error(utils.NewBadRequestError(err.Error()))
		return
	}

	result, err := c.collection.InsertOne(reqCtx, tour)
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

// UpdateTour Godoc
// @Summary      Update an existing tour
// @Description  Update tour details by ID
// @Tags         Tours
// @Accept       json,multipart/form-data
// @Produce      json
// @Security     Bearer
// @Param        id    path      string       true   "Tour ID"
// @Param        tour  body      models.Tour  false  "Partial Tour updates"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  utils.AppError
// @Failure      401   {object}  utils.AppError
// @Failure      404   {object}  utils.AppError
// @Failure      500   {object}  utils.AppError
// @Router       /tours/{id} [patch]
func (c *TourController) UpdateTour(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()
	id := ctx.Param("id")

	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid tour ID"))
		return
	}

	updateData := bson.M{}
	contentType := ctx.GetHeader("Content-Type")

	if strings.HasPrefix(contentType, "multipart/form-data") {
		if uploadedImages, exists := ctx.Get("uploadedImages"); exists {
			images := uploadedImages.(*middleware.ImageUploadResult)

			if images.ImageCover != "" || len(images.Images) > 0 {
				if err := c.replaceImages(reqCtx, objID, images.ImageCover, images.Images); err != nil {
					ctx.Error(err)
					return
				}
			}

			if images.ImageCover != "" {
				updateData["imageCover"] = images.ImageCover
			}
			if len(images.Images) > 0 {
				updateData["images"] = images.Images
			}
		}

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
		var jsonData map[string]interface{}
		if err := ctx.ShouldBindJSON(&jsonData); err != nil {
			ctx.Error(utils.NewBadRequestError("Invalid request body: " + err.Error()))
			return
		}

		if name, ok := jsonData["name"].(string); ok {
			jsonData["slug"] = models.GenerateSlug(name)
		}

		updateData = jsonData
	}

	if len(updateData) == 0 {
		ctx.Error(utils.NewBadRequestError("No update data provided"))
		return
	}

	update := bson.M{"$set": updateData}

	result := c.collection.FindOneAndUpdate(
		reqCtx,
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

func (c *TourController) replaceImages(ctx context.Context, tourID primitive.ObjectID, newImageCover string, newImages []string) error {
	var existingTour models.Tour
	err := c.collection.FindOne(ctx, bson.M{"_id": tourID}).Decode(&existingTour)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return utils.NewNotFoundError("Tour not found")
		}
		return utils.NewInternalServerError(err)
	}

	if newImageCover != "" && existingTour.ImageCover != "" {
		utils.DeleteImages(existingTour.ImageCover)
	}

	if len(newImages) > 0 && len(existingTour.Images) > 0 {
		utils.DeleteImages(existingTour.Images...)
	}

	return nil
}

// DeleteTour Godoc
// @Summary      Delete a tour
// @Description  Delete a tour by ID and remove associated files
// @Tags         Tours
// @Produce      json
// @Security     Bearer
// @Param        id   path      string  true  "Tour ID"
// @Success      204  "No Content"
// @Failure      400  {object}  utils.AppError
// @Failure      401  {object}  utils.AppError
// @Failure      404  {object}  utils.AppError
// @Failure      500  {object}  utils.AppError
// @Router       /tours/{id} [delete]
func (c *TourController) DeleteTour(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()
	id := ctx.Param("id")

	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid tour ID"))
		return
	}

	var tour models.Tour
	err = c.collection.FindOne(reqCtx, bson.M{"_id": objID}).Decode(&tour)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("Tour not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	toDelete := make([]string, 0, len(tour.Images)+1)
	if tour.ImageCover != "" {
		toDelete = append(toDelete, tour.ImageCover)
	}
	toDelete = append(toDelete, tour.Images...)

	if len(toDelete) > 0 {
		utils.DeleteImages(toDelete...)
	}

	result, err := c.collection.DeleteOne(reqCtx, bson.M{"_id": objID})
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

// CleanupOrphanedImages Godoc
// @Summary      Clean up orphaned tour images
// @Description  Deletes unreferenced files from disk (Admin only)
// @Tags         Tours
// @Produce      json
// @Security     Bearer
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  utils.AppError
// @Failure      500  {object}  utils.AppError
// @Router       /admin/tours/cleanup/images [delete]
func (c *TourController) CleanupOrphanedImages(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()

	var tours []models.Tour
	cursor, err := c.collection.Find(reqCtx, bson.M{})
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(reqCtx)

	if err = cursor.All(reqCtx, &tours); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	referencedImages := make(map[string]bool)
	for _, tour := range tours {
		if tour.ImageCover != "" {
			referencedImages[tour.ImageCover] = true
		}
		for _, img := range tour.Images {
			referencedImages[img] = true
		}
	}

	uploadPath := "public/img/tours"
	files, err := os.ReadDir(uploadPath)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

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

// GetTourStats Godoc
// @Summary      Get tour aggregate statistics
// @Description  Calculates aggregated stats grouped by difficulty
// @Tags         Tours
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      500  {object}  utils.AppError
// @Router       /tours/stats [get]
func (c *TourController) GetTourStats(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()

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

	cursor, err := c.collection.Aggregate(reqCtx, pipeline)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(reqCtx)

	var stats []bson.M
	if err = cursor.All(reqCtx, &stats); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"data":   gin.H{"stats": stats},
	})
}

// GetMonthlyPlan Godoc
// @Summary      Get monthly tour plan for a given year
// @Description  Calculates monthly schedule count for tours
// @Tags         Tours
// @Produce      json
// @Param        year  path      int  true  "Year (e.g. 2026)"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  utils.AppError
// @Failure      500   {object}  utils.AppError
// @Router       /monthly-plan/{year} [get]
func (c *TourController) GetMonthlyPlan(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()

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

	cursor, err := c.collection.Aggregate(reqCtx, pipeline)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(reqCtx)

	var plan []bson.M
	if err = cursor.All(reqCtx, &plan); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"result": len(plan),
		"data":   gin.H{"plan": plan},
	})
}

// GetToursWithin Godoc
// @Summary      Get tours within a specified radius
// @Description  Geospatial query to fetch tours near a center point
// @Tags         Tours
// @Produce      json
// @Param        distance  path      number  true  "Radius distance"
// @Param        latlng    path      string  true  "Latitude and Longitude (lat,lng)"
// @Param        unit      path      string  true  "Unit: 'mi' or 'km'"
// @Success      200       {object}  map[string]interface{}
// @Failure      400       {object}  utils.AppError
// @Failure      500       {object}  utils.AppError
// @Router       /tours/tours-within/{distance}/center/{latlng}/unit/{unit} [get]
func (c *TourController) GetToursWithin(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()

	distance, err := strconv.ParseFloat(ctx.Param("distance"), 64)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid distance"))
		return
	}

	latlng := ctx.Param("latlng")
	unit := ctx.Param("unit")

	parts := strings.Split(latlng, ",")
	if len(parts) != 2 {
		ctx.Error(utils.NewBadRequestError("Please provide latitude and longitude in format lat,lng"))
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

	cursor, err := c.collection.Find(reqCtx, filter)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(reqCtx)

	var tours []models.Tour
	if err = cursor.All(reqCtx, &tours); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status":  "success",
		"results": len(tours),
		"data":    gin.H{"tours": tours},
	})
}

// GetDistances Godoc
// @Summary      Get distances from point to all tour starting locations
// @Description  Calculates distance from latlng to each tour start point
// @Tags         Tours
// @Produce      json
// @Param        latlng  path      string  true  "Latitude and Longitude (lat,lng)"
// @Param        unit    path      string  true  "Unit: 'mi' or 'km'"
// @Success      200     {object}  map[string]interface{}
// @Failure      400     {object}  utils.AppError
// @Failure      500     {object}  utils.AppError
// @Router       /distances/{latlng}/unit/{unit} [get]
func (c *TourController) GetDistances(ctx *gin.Context) {
	reqCtx := ctx.Request.Context()

	latlng := ctx.Param("latlng")
	unit := ctx.Param("unit")

	parts := strings.Split(latlng, ",")
	if len(parts) != 2 {
		ctx.Error(utils.NewBadRequestError("Please provide latitude and longitude in format lat,lng"))
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

	cursor, err := c.collection.Aggregate(reqCtx, pipeline)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(reqCtx)

	var distances []bson.M
	if err = cursor.All(reqCtx, &distances); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(200, gin.H{
		"status": "success",
		"data":   gin.H{"distances": distances},
	})
}

func validateTour(tour models.Tour) error {
	if len(tour.Name) < models.MinNameLength {
		return fmt.Errorf("tour name must have at least %d characters", models.MinNameLength)
	}
	if len(tour.Name) > models.MaxNameLength {
		return fmt.Errorf("tour name must have at most %d characters", models.MaxNameLength)
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
