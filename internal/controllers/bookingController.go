package controllers

import (
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

// GetCheckoutSession - GET /api/v1/bookings/checkout-session/:tourId
func (c *BookingController) GetCheckoutSession(ctx *gin.Context) {
	mockMode := os.Getenv("PAYSTACK_MOCK_MODE") == "true"
	if mockMode && gin.Mode() != gin.ReleaseMode {
		c.getMockCheckoutSession(ctx)
		return
	}
	c.getRealCheckoutSession(ctx)
}

// getMockCheckoutSession - Mock version for non-production environments
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

	userIDStr := userID.(string)
	userObjID, _ := primitive.ObjectIDFromHex(userIDStr)

	var tour models.Tour
	// FIX: Use request context instead of context.Background()
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

	// FIX: Safer precision arithmetic (Ideally, store pricing as integer Kobo directly in your models)
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

// getRealCheckoutSession - Real Paystack version
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

	userIDStr := userID.(string)
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

	var user models.User
	err = c.userCollection.FindOne(ctx.Request.Context(), bson.M{"_id": userObjID}).Decode(&user)
	if err != nil {
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

// VerifyPayment - GET /api/v1/bookings/verify-payment
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

// Webhook - POST /api/v1/bookings/webhook
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

	// FIX: Strict signature validation to prevent spoofing variants
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

// GetAllBookings - GET /api/v1/bookings (Admin only)
func (c *BookingController) GetAllBookings(ctx *gin.Context) {
	// FIX: Solved N+1 problem completely using a fast, isolated $lookup pipeline execution
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

// GetBooking - GET /api/v1/bookings/:id (Admin only)
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

// CreateBooking - POST /api/v1/bookings (Admin only)
func (c *BookingController) CreateBooking(ctx *gin.Context) {
	var req struct {
		TourID string  `json:"tourId" binding:"required"`
		UserID string  `json:"userId" binding:"required"`
		Price  float64 `json:"price" binding:"required"`
	}

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

// UpdateBooking - PATCH /api/v1/bookings/:id (Admin only)
func (c *BookingController) UpdateBooking(ctx *gin.Context) {
	id := ctx.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid booking ID"))
		return
	}

	// FIX: Use explicit DTO instead of unverified direct bson.M assignment mapping
	var req struct {
		Status string `json:"status" binding:"required"`
	}
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

// DeleteBooking - DELETE /api/v1/bookings/:id (Admin only)
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

// MockPaymentPage - GET /api/v1/bookings/mock-payment
func (c *BookingController) MockPaymentPage(ctx *gin.Context) {
	// FIX: Use html.EscapeString to sanitize against injection vectors
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
