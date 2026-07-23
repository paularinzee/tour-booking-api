package controllers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/paularinzee/natour/internal/models"
	"github.com/paularinzee/natour/pkg/paystack"
	"github.com/paularinzee/natour/pkg/utils"
)

type BookingController struct {
	bookingCollection *mongo.Collection
	tourCollection    *mongo.Collection
	userCollection    *mongo.Collection
	paymentService    *paystack.PaymentService
}

// CreateBookingDTO represents payload for creating a booking manually
type CreateBookingDTO struct {
	TourID string  `json:"tourId" binding:"required" example:"60c72b2f9b1d8b2a3c8e4567"`
	UserID string  `json:"userId" binding:"required" example:"60c72b2f9b1d8b2a3c8e4568"`
	Price  float64 `json:"price" binding:"required" example:"250.00"`
}

// UpdateBookingDTO represents payload for updating a booking status
type UpdateBookingDTO struct {
	Status string `json:"status" binding:"required" example:"paid"`
}

func NewBookingController(db *mongo.Database) (*BookingController, error) {
	paymentService, err := paystack.NewPaymentService()
	if err != nil {
		return nil, err
	}

	return &BookingController{
		bookingCollection: db.Collection("bookings"),
		tourCollection:    db.Collection("tours"),
		userCollection:    db.Collection("users"),
		paymentService:    paymentService,
	}, nil
}

// GetCheckoutSession godoc
// @Summary      Create checkout session
// @Description  Initializes a Paystack checkout session for a tour or runs a mock checkout session in test mode
// @Tags         Bookings
// @Accept       json
// @Produce      json
// @Param        tourId path string true "Tour ID"
// @Security     BearerAuth
// @Success      200 {object} map[string]interface{} "Returns authorizationUrl, reference, and accessCode"
// @Failure      400 {object} map[string]interface{} "Invalid tour ID"
// @Failure      401 {object} map[string]interface{} "Not authenticated"
// @Failure      404 {object} map[string]interface{} "Tour not found"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /api/v1/bookings/checkout-session/{tourId} [get]
func (c *BookingController) GetCheckoutSession(ctx *gin.Context) {
	mockMode := os.Getenv("PAYSTACK_MOCK_MODE") == "true"
	if mockMode && gin.Mode() != gin.ReleaseMode {
		c.getMockCheckoutSession(ctx)
		return
	}
	c.getRealCheckoutSession(ctx)
}

func (c *BookingController) getMockCheckoutSession(ctx *gin.Context) {
	tourID := ctx.Param("tourId")
	tourObjID, err := primitive.ObjectIDFromHex(tourID)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid tour ID"))
		return
	}

	userID, exists := ctx.Get("userID")
	if !exists {
		ctx.Error(utils.NewUnauthorizedError("Not authenticated"))
		return
	}

	userIDStr, ok := userID.(string)
	if !ok {
		ctx.Error(utils.NewUnauthorizedError("Invalid authenticated user session"))
		return
	}
	userObjID, _ := primitive.ObjectIDFromHex(userIDStr)

	var tour models.Tour
	err = c.tourCollection.FindOne(ctx.Request.Context(), bson.M{"_id": tourObjID}).Decode(&tour)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("Tour not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	domain := os.Getenv("DOMAIN")
	if domain == "" {
		domain = "http://localhost:8080"
	}

	reference := fmt.Sprintf("MOCK-TOUR-%s-%d", tourID, time.Now().UnixNano())
	amountKobo := int64(tour.Price*100 + 0.5)

	booking := models.Booking{
		ID:               primitive.NewObjectID(),
		TourID:           tourObjID,
		UserID:           userObjID,
		Price:            tour.Price,
		PriceKobo:        amountKobo,
		Status:           models.BookingPending,
		Reference:        reference,
		AccessCode:       "mock_access_code",
		AuthorizationURL: fmt.Sprintf("%s/api/v1/bookings/mock-payment?reference=%s", domain, reference),
	}

	if err := booking.BeforeSave(); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	_, err = c.bookingCollection.InsertOne(ctx.Request.Context(), booking)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"authorizationUrl": booking.AuthorizationURL,
			"reference":        booking.Reference,
			"accessCode":       booking.AccessCode,
			"mock":             true,
		},
	})
}

func (c *BookingController) getRealCheckoutSession(ctx *gin.Context) {
	tourID := ctx.Param("tourId")
	tourObjID, err := primitive.ObjectIDFromHex(tourID)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid tour ID"))
		return
	}

	userID, exists := ctx.Get("userID")
	if !exists {
		ctx.Error(utils.NewUnauthorizedError("Not authenticated"))
		return
	}

	userIDStr, ok := userID.(string)
	if !ok {
		ctx.Error(utils.NewUnauthorizedError("Invalid user session"))
		return
	}
	userObjID, err := primitive.ObjectIDFromHex(userIDStr)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid user ID format"))
		return
	}

	// FIX: Corrected query target to tourCollection with tourObjID
	var tour models.Tour
	err = c.tourCollection.FindOne(ctx.Request.Context(), bson.M{"_id": tourObjID}).Decode(&tour)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("Tour not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	var user models.User
	err = c.userCollection.FindOne(ctx.Request.Context(), bson.M{"_id": userObjID}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("User not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	reference := fmt.Sprintf("TOUR-%s-%d", tourID, time.Now().UnixNano())
	amountKobo := int64(tour.Price*100 + 0.5)

	domain := os.Getenv("DOMAIN")
	if domain == "" {
		domain = "http://localhost:8080"
	}
	callbackURL := domain + "/api/v1/bookings/verify-payment"

	paymentReq := &paystack.InitializePaymentRequest{
		Email:       user.Email,
		Amount:      amountKobo,
		Reference:   reference,
		CallbackURL: callbackURL,
		Metadata: map[string]interface{}{
			"tourId":   tourID,
			"userId":   userIDStr,
			"tourName": tour.Name,
		},
	}

	paymentResp, err := c.paymentService.InitializePayment(ctx.Request.Context(), paymentReq)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	booking := models.Booking{
		ID:               primitive.NewObjectID(),
		TourID:           tourObjID,
		UserID:           userObjID,
		Price:            tour.Price,
		PriceKobo:        amountKobo,
		Status:           models.BookingPending,
		Reference:        paymentResp.Reference,
		AccessCode:       paymentResp.AccessCode,
		AuthorizationURL: paymentResp.AuthorizationURL,
	}

	if err := booking.BeforeSave(); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	_, err = c.bookingCollection.InsertOne(ctx.Request.Context(), booking)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"authorizationUrl": paymentResp.AuthorizationURL,
			"reference":        paymentResp.Reference,
			"accessCode":       paymentResp.AccessCode,
		},
	})
}

// VerifyPayment godoc
// @Summary      Verify booking payment
// @Description  Verifies the status of a transaction using the Paystack payment reference
// @Tags         Bookings
// @Produce      json
// @Param        reference query string true "Payment reference"
// @Success      200 {object} map[string]interface{} "Payment verified successfully"
// @Failure      400 {object} map[string]interface{} "Missing reference or verification failed"
// @Failure      500 {object} map[string]interface{} "Failed to update booking"
// @Router       /api/v1/bookings/verify-payment [get]
func (c *BookingController) VerifyPayment(ctx *gin.Context) {
	reference := ctx.Query("reference")
	if reference == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Missing payment reference"})
		return
	}

	mockMode := os.Getenv("PAYSTACK_MOCK_MODE") == "true"
	if mockMode && gin.Mode() != gin.ReleaseMode {
		_, err := c.bookingCollection.UpdateOne(
			ctx.Request.Context(),
			bson.M{"reference": reference},
			bson.M{"$set": bson.M{"status": models.BookingPaid, "updatedAt": time.Now()}},
		)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Failed to update booking"})
			return
		}

		var booking models.Booking
		c.bookingCollection.FindOne(ctx.Request.Context(), bson.M{"reference": reference}).Decode(&booking)

		ctx.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Payment verified successfully",
			"data":    gin.H{"booking": booking.ToResponse(), "reference": reference},
		})
		return
	}

	success, err := c.paymentService.VerifyPayment(ctx.Request.Context(), reference)
	if err != nil || !success {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Payment verification failed"})
		return
	}

	_, err = c.bookingCollection.UpdateOne(
		ctx.Request.Context(),
		bson.M{"reference": reference},
		bson.M{"$set": bson.M{"status": models.BookingPaid, "updatedAt": time.Now()}},
	)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Failed to update booking"})
		return
	}

	var booking models.Booking
	c.bookingCollection.FindOne(ctx.Request.Context(), bson.M{"reference": reference}).Decode(&booking)

	ctx.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Payment verified successfully",
		"data":    gin.H{"booking": booking.ToResponse(), "reference": reference},
	})
}

// Webhook godoc
// @Summary      Paystack webhook endpoint
// @Description  Receives asynchronous event updates from Paystack (e.g., charge.success)
// @Tags         Bookings
// @Accept       json
// @Produce      json
// @Param        x-paystack-signature header string true "HMAC SHA512 signature"
// @Success      200 {object} map[string]interface{} "Status success or ignored"
// @Failure      400 {object} map[string]interface{} "Bad request or missing payload"
// @Failure      401 {object} map[string]interface{} "Invalid signature"
// @Router       /api/v1/bookings/webhook [post]
func (c *BookingController) Webhook(ctx *gin.Context) {
	signature := ctx.GetHeader("x-paystack-signature")
	if signature == "" {
		ctx.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		ctx.AbortWithStatus(http.StatusBadRequest)
		return
	}

	// FIX: Restore the body reader so downstream middleware can read it if needed
	ctx.Request.Body = io.NopCloser(bytes.NewBuffer(body))

	secret := os.Getenv("PAYSTACK_SECRET_KEY")
	h := hmac.New(sha512.New, []byte(secret))
	h.Write(body)
	expectedSignature := hex.EncodeToString(h.Sum(nil))

	if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
		ctx.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	var webhookData struct {
		Event string `json:"event"`
		Data  struct {
			Reference string `json:"reference"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &webhookData); err != nil {
		ctx.AbortWithStatus(http.StatusBadRequest)
		return
	}

	var targetStatus models.BookingStatus
	switch webhookData.Event {
	case "charge.success":
		targetStatus = models.BookingPaid
	case "charge.failed", "charge.dispute.created":
		targetStatus = models.BookingCancelled
	default:
		ctx.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}

	_, err = c.bookingCollection.UpdateOne(
		ctx.Request.Context(),
		bson.M{"reference": webhookData.Data.Reference},
		bson.M{"$set": bson.M{"status": targetStatus, "updatedAt": time.Now()}},
	)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "success"})
}

// GetAllBookings godoc
// @Summary      Get all bookings
// @Description  Retrieves all bookings populated with tour and user details (Admin only)
// @Tags         Bookings
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} map[string]interface{} "List of populated bookings"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /api/v1/bookings [get]
func (c *BookingController) GetAllBookings(ctx *gin.Context) {
	pipeline := mongo.Pipeline{
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: "tours"},
			{Key: "localField", Value: "tourId"},
			{Key: "foreignField", Value: "_id"},
			{Key: "as", Value: "tour"},
		}}},
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: "users"},
			{Key: "localField", Value: "userId"},
			{Key: "foreignField", Value: "_id"},
			{Key: "as", Value: "user"},
		}}},
		{{Key: "$unwind", Value: bson.D{
			{Key: "path", Value: "$tour"},
			{Key: "preserveNullAndEmptyArrays", Value: true},
		}}},
		{{Key: "$unwind", Value: bson.D{
			{Key: "path", Value: "$user"},
			{Key: "preserveNullAndEmptyArrays", Value: true},
		}}},
	}

	cursor, err := c.bookingCollection.Aggregate(ctx.Request.Context(), pipeline)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(ctx.Request.Context())

	var bookings []models.Booking
	if err = cursor.All(ctx.Request.Context(), &bookings); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	responses := make([]models.BookingResponse, len(bookings))
	for i, booking := range bookings {
		responses[i] = booking.ToResponse()
	}

	ctx.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"results": len(responses),
		"data":    gin.H{"bookings": responses},
	})
}

// GetBooking godoc
// @Summary      Get booking by ID
// @Description  Fetches details of a specific booking by ID (Admin only)
// @Tags         Bookings
// @Produce      json
// @Param        id path string true "Booking ID"
// @Security     BearerAuth
// @Success      200 {object} map[string]interface{} "Booking details"
// @Failure      400 {object} map[string]interface{} "Invalid booking ID"
// @Failure      404 {object} map[string]interface{} "Booking not found"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /api/v1/bookings/{id} [get]
func (c *BookingController) GetBooking(ctx *gin.Context) {
	id := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid booking ID"))
		return
	}

	var booking models.Booking
	err = c.bookingCollection.FindOne(ctx.Request.Context(), bson.M{"_id": objID}).Decode(&booking)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("Booking not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	_ = c.tourCollection.FindOne(ctx.Request.Context(), bson.M{"_id": booking.TourID}).Decode(&booking.Tour)
	_ = c.userCollection.FindOne(ctx.Request.Context(), bson.M{"_id": booking.UserID}).Decode(&booking.User)

	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   gin.H{"booking": booking.ToResponse()},
	})
}

// CreateBooking godoc
// @Summary      Create booking
// @Description  Manually creates a booking (Admin only)
// @Tags         Bookings
// @Accept       json
// @Produce      json
// @Param        booking body CreateBookingDTO true "Booking details"
// @Security     BearerAuth
// @Success      201 {object} map[string]interface{} "Created booking"
// @Failure      400 {object} map[string]interface{} "Invalid input data"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /api/v1/bookings [post]
func (c *BookingController) CreateBooking(ctx *gin.Context) {
	var req CreateBookingDTO

	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	tourObjID, err := primitive.ObjectIDFromHex(req.TourID)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid tour ID"))
		return
	}

	userObjID, err := primitive.ObjectIDFromHex(req.UserID)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid user ID"))
		return
	}

	booking := models.Booking{
		ID:        primitive.NewObjectID(),
		TourID:    tourObjID,
		UserID:    userObjID,
		Price:     req.Price,
		PriceKobo: int64(req.Price*100 + 0.5),
		Status:    models.BookingPaid,
		Reference: fmt.Sprintf("ADMIN-%d", time.Now().UnixNano()),
	}

	if err := booking.BeforeSave(); err != nil {
		ctx.Error(utils.NewBadRequestError(err.Error()))
		return
	}

	_, err = c.bookingCollection.InsertOne(ctx.Request.Context(), booking)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(http.StatusCreated, gin.H{
		"status": "success",
		"data":   gin.H{"booking": booking.ToResponse()},
	})
}

// UpdateBooking godoc
// @Summary      Update booking
// @Description  Updates status of an existing booking (Admin only)
// @Tags         Bookings
// @Accept       json
// @Produce      json
// @Param        id path string true "Booking ID"
// @Param        booking body UpdateBookingDTO true "Status update payload"
// @Security     BearerAuth
// @Success      200 {object} map[string]interface{} "Updated booking"
// @Failure      400 {object} map[string]interface{} "Invalid input data or booking ID"
// @Failure      404 {object} map[string]interface{} "Booking not found"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /api/v1/bookings/{id} [patch]
func (c *BookingController) UpdateBooking(ctx *gin.Context) {
	id := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid booking ID"))
		return
	}

	var req UpdateBookingDTO
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	update := bson.M{
		"$set": bson.M{
			"status":    req.Status,
			"updatedAt": time.Now(),
		},
	}

	var updatedBooking models.Booking
	err = c.bookingCollection.FindOneAndUpdate(
		ctx.Request.Context(),
		bson.M{"_id": objID},
		update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updatedBooking)

	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("Booking not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   gin.H{"booking": updatedBooking.ToResponse()},
	})
}

// DeleteBooking godoc
// @Summary      Delete booking
// @Description  Removes a booking record (Admin only)
// @Tags         Bookings
// @Param        id path string true "Booking ID"
// @Security     BearerAuth
// @Success      240 "No Content"
// @Failure      400 {object} map[string]interface{} "Invalid booking ID"
// @Failure      404 {object} map[string]interface{} "Booking not found"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /api/v1/bookings/{id} [delete]
func (c *BookingController) DeleteBooking(ctx *gin.Context) {
	id := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid booking ID"))
		return
	}

	result, err := c.bookingCollection.DeleteOne(ctx.Request.Context(), bson.M{"_id": objID})
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	if result.DeletedCount == 0 {
		ctx.Error(utils.NewNotFoundError("Booking not found"))
		return
	}

	ctx.Status(http.StatusNoContent)
}

// MockPaymentPage godoc
// @Summary      Render mock payment UI
// @Description  Renders an HTML interface for completing test mode payments
// @Tags         Bookings
// @Produce      html
// @Param        reference query string true "Test reference"
// @Success      200 {string} string "HTML content"
// @Router       /api/v1/bookings/mock-payment [get]
func (c *BookingController) MockPaymentPage(ctx *gin.Context) {
	reference := html.EscapeString(ctx.Query("reference"))

	htmlContent := `
	<!DOCTYPE html>
	<html>
	<head>
		<title>Mock Payment - Test Mode</title>
		<style>
			body { font-family: Arial; text-align: center; padding: 50px; }
			.container { max-width: 500px; margin: auto; }
			.success-box { background: #d4edda; color: #155724; padding: 30px; border-radius: 5px; margin: 20px 0; }
			.info-box { background: #e2e3e5; padding: 15px; border-radius: 5px; margin: 20px 0; }
			button { background: #28a745; color: white; padding: 15px 30px; font-size: 16px; border: none; cursor: pointer; border-radius: 5px; }
			button:hover { background: #218838; }
		</style>
	</head>
	<body>
		<div class="container">
			<h1>🔧 Mock Payment Page</h1>
			<div class="info-box">
				<p><strong>Test Reference:</strong> ` + reference + `</p>
			</div>
			<div class="success-box">
				<button onclick="completePayment()">Complete Test Payment</button>
			</div>
		</div>
		<script>
			function completePayment() {
				fetch('/api/v1/bookings/verify-payment?reference=` + reference + `')
					.then(res => res.json())
					.then(data => {
						if (data.status === 'success') {
							window.location.href = '/booking-success?reference=` + reference + `';
						} else {
							alert('Verification failed');
						}
					});
			}
		</script>
	</body>
	</html>`

	ctx.Data(http.StatusOK, "text/html; charset=utf-8", []byte(htmlContent))
}
