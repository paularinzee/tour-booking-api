package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	log.Println("=== GetCheckoutSession called ===")

	// Check if mock mode is enabled
	mockMode := os.Getenv("PAYSTACK_MOCK_MODE") == "true"
	log.Printf("Mock mode: %v", mockMode)

	if mockMode {
		c.getMockCheckoutSession(ctx)
		return
	}

	c.getRealCheckoutSession(ctx)
}

// getMockCheckoutSession - Mock version for testing
func (c *BookingController) getMockCheckoutSession(ctx *gin.Context) {
	log.Println("Using MOCK checkout session")

	tourID := ctx.Param("tourId")
	log.Printf("Tour ID: %s", tourID)

	tourObjID, err := primitive.ObjectIDFromHex(tourID)
	if err != nil {
		log.Printf("Invalid tour ID: %v", err)
		ctx.Error(utils.NewBadRequestError("Invalid tour ID"))
		return
	}

	// Get user ID from context
	userID, exists := ctx.Get("userID")
	if !exists {
		log.Println("User not authenticated")
		ctx.Error(utils.NewUnauthorizedError("Not authenticated"))
		return
	}

	userIDStr := userID.(string)
	userObjID, _ := primitive.ObjectIDFromHex(userIDStr)
	log.Printf("User ID: %s", userIDStr)

	// Get tour details
	var tour models.Tour
	err = c.tourCollection.FindOne(context.Background(), bson.M{"_id": tourObjID}).Decode(&tour)
	if err != nil {
		log.Printf("Tour not found: %v", err)
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("Tour not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	log.Printf("Tour: %s, Price: %.2f", tour.Name, tour.Price)

	domain := os.Getenv("DOMAIN")
	if domain == "" {
		domain = "http://localhost:8080"
	}

	// Generate unique reference
	reference := fmt.Sprintf("MOCK-TOUR-%s-%d", tourID, time.Now().UnixNano())
	log.Printf("Generated reference: %s", reference)

	// Calculate amount in Kobo
	amountKobo := int64(tour.Price * 100)

	// Create booking record with pending status
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
		log.Printf("BeforeSave error: %v", err)
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	_, err = c.bookingCollection.InsertOne(context.Background(), booking)
	if err != nil {
		log.Printf("Insert error: %v", err)
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	log.Printf("Booking created with ID: %s", booking.ID.Hex())

	ctx.JSON(200, gin.H{
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
	log.Println("Using REAL Paystack checkout session")

	tourID := ctx.Param("tourId")
	log.Printf("Tour ID: %s", tourID)

	tourObjID, err := primitive.ObjectIDFromHex(tourID)
	if err != nil {
		log.Printf("Invalid tour ID: %v", err)
		ctx.Error(utils.NewBadRequestError("Invalid tour ID"))
		return
	}

	// Get user ID from context
	userID, exists := ctx.Get("userID")
	if !exists {
		log.Println("User not authenticated")
		ctx.Error(utils.NewUnauthorizedError("Not authenticated"))
		return
	}

	userIDStr := userID.(string)
	userObjID, _ := primitive.ObjectIDFromHex(userIDStr)
	log.Printf("User ID: %s", userIDStr)

	// Get tour details
	var tour models.Tour
	err = c.tourCollection.FindOne(context.Background(), bson.M{"_id": tourObjID}).Decode(&tour)
	if err != nil {
		log.Printf("Tour not found: %v", err)
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("Tour not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	log.Printf("Tour: %s, Price: %.2f", tour.Name, tour.Price)

	// Get user details
	var user models.User
	err = c.userCollection.FindOne(context.Background(), bson.M{"_id": userObjID}).Decode(&user)
	if err != nil {
		log.Printf("User not found: %v", err)
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	log.Printf("User: %s, Email: %s", user.Name, user.Email)

	// Generate unique reference
	reference := fmt.Sprintf("TOUR-%s-%d", tourID, time.Now().UnixNano())
	log.Printf("Generated reference: %s", reference)

	// Calculate amount in Kobo
	amountKobo := int64(tour.Price * 100)

	// Get callback URL
	domain := os.Getenv("DOMAIN")
	if domain == "" {
		domain = "http://localhost:8080"
	}
	callbackURL := domain + "/api/v1/bookings/verify-payment"

	// Initialize Paystack transaction
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

	paymentResp, err := c.paymentService.InitializePayment(context.Background(), paymentReq)
	if err != nil {
		log.Printf("Paystack initialization error: %v", err)
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Create booking record with pending status
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
		log.Printf("BeforeSave error: %v", err)
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	_, err = c.bookingCollection.InsertOne(context.Background(), booking)
	if err != nil {
		log.Printf("Insert error: %v", err)
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	log.Printf("Booking created with ID: %s", booking.ID.Hex())

	ctx.JSON(200, gin.H{
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
	log.Println("=== VerifyPayment called ===")

	reference := ctx.Query("reference")
	log.Printf("Reference: %s", reference)

	if reference == "" {
		log.Println("Error: Missing reference")
		ctx.JSON(400, gin.H{
			"status":  "error",
			"message": "Missing payment reference",
		})
		return
	}

	// Check if we're in mock mode
	mockMode := os.Getenv("PAYSTACK_MOCK_MODE") == "true"
	log.Printf("Mock mode: %v", mockMode)

	if mockMode {
		log.Println("Using mock verification")

		// Update booking to paid
		_, err := c.bookingCollection.UpdateOne(
			context.Background(),
			bson.M{"reference": reference},
			bson.M{"$set": bson.M{
				"status":    models.BookingPaid,
				"updatedAt": time.Now(),
			}},
		)

		if err != nil {
			log.Printf("Error updating booking: %v", err)
			ctx.JSON(500, gin.H{
				"status":  "error",
				"message": "Failed to update booking",
			})
			return
		}

		// Get the updated booking
		var booking models.Booking
		c.bookingCollection.FindOne(context.Background(), bson.M{"reference": reference}).Decode(&booking)

		log.Printf("Payment verified successfully")

		// Return JSON response - frontend will handle redirect
		ctx.JSON(200, gin.H{
			"status":  "success",
			"message": "Payment verified successfully",
			"data": gin.H{
				"booking":   booking.ToResponse(),
				"reference": reference,
			},
		})
		return
	}

	// Real Paystack verification
	log.Println("Using real Paystack verification")
	success, err := c.paymentService.VerifyPayment(context.Background(), reference)
	if err != nil {
		log.Printf("Paystack verification error: %v", err)
		ctx.JSON(500, gin.H{
			"status":  "error",
			"message": "Payment verification failed: " + err.Error(),
		})
		return
	}

	if !success {
		log.Println("Payment verification failed")
		ctx.JSON(400, gin.H{
			"status":  "error",
			"message": "Payment verification failed",
		})
		return
	}

	// Update booking status
	_, err = c.bookingCollection.UpdateOne(
		context.Background(),
		bson.M{"reference": reference},
		bson.M{"$set": bson.M{
			"status":    models.BookingPaid,
			"updatedAt": time.Now(),
		}},
	)

	if err != nil {
		log.Printf("Error updating booking: %v", err)
		ctx.JSON(500, gin.H{
			"status":  "error",
			"message": "Failed to update booking",
		})
		return
	}

	// Get the updated booking
	var booking models.Booking
	c.bookingCollection.FindOne(context.Background(), bson.M{"reference": reference}).Decode(&booking)

	log.Printf("Payment verified and booking updated")

	// Return JSON response - frontend will handle redirect
	ctx.JSON(200, gin.H{
		"status":  "success",
		"message": "Payment verified successfully",
		"data": gin.H{
			"booking":   booking.ToResponse(),
			"reference": reference,
		},
	})
}

// Webhook - POST /api/v1/bookings/webhook
// This is called by Paystack to notify about payment events
func (c *BookingController) Webhook(ctx *gin.Context) {
	// Get the Paystack signature from headers
	signature := ctx.GetHeader("x-paystack-signature")
	if signature == "" {
		ctx.Error(utils.NewUnauthorizedError("Missing signature"))
		return
	}

	// Read raw body
	body, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request body"))
		return
	}

	// Verify webhook signature (optional - can be implemented for security)
	// This would involve hashing the body with your secret key

	// Parse webhook event
	var webhookData struct {
		Event string `json:"event"`
		Data  struct {
			Reference string `json:"reference"`
			Status    string `json:"status"`
			Amount    int64  `json:"amount"`
			Metadata  struct {
				TourId string `json:"tourId"`
				UserId string `json:"userId"`
			} `json:"metadata"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &webhookData); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid webhook data"))
		return
	}

	// Handle different event types
	switch webhookData.Event {
	case "charge.success":
		// Update booking status to paid
		_, err = c.bookingCollection.UpdateOne(
			context.Background(),
			bson.M{"reference": webhookData.Data.Reference},
			bson.M{"$set": bson.M{
				"status":    models.BookingPaid,
				"updatedAt": time.Now(),
			}},
		)
		if err != nil {
			ctx.Error(utils.NewInternalServerError(err))
			return
		}

	case "charge.failed", "charge.dispute.created":
		// Update booking status to cancelled
		_, err = c.bookingCollection.UpdateOne(
			context.Background(),
			bson.M{"reference": webhookData.Data.Reference},
			bson.M{"$set": bson.M{
				"status":    models.BookingCancelled,
				"updatedAt": time.Now(),
			}},
		)
		if err != nil {
			ctx.Error(utils.NewInternalServerError(err))
			return
		}
	}

	ctx.JSON(200, gin.H{"status": "success"})
}

// GetAllBookings - GET /api/v1/bookings (Admin only)
func (c *BookingController) GetAllBookings(ctx *gin.Context) {
	cursor, err := c.bookingCollection.Find(context.Background(), bson.M{})
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}
	defer cursor.Close(context.Background())

	var bookings []models.Booking
	if err = cursor.All(context.Background(), &bookings); err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Populate tour and user data
	for i := range bookings {
		var tour models.Tour
		c.tourCollection.FindOne(context.Background(), bson.M{"_id": bookings[i].TourID}).Decode(&tour)
		bookings[i].Tour = &tour

		var user models.User
		c.userCollection.FindOne(context.Background(), bson.M{"_id": bookings[i].UserID}).Decode(&user)
		bookings[i].User = &user
	}

	responses := make([]models.BookingResponse, len(bookings))
	for i, booking := range bookings {
		responses[i] = booking.ToResponse()
	}

	ctx.JSON(200, gin.H{
		"status":  "success",
		"results": len(responses),
		"data": gin.H{
			"bookings": responses,
		},
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
	err = c.bookingCollection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&booking)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			ctx.Error(utils.NewNotFoundError("Booking not found"))
			return
		}
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	// Populate tour and user
	c.tourCollection.FindOne(context.Background(), bson.M{"_id": booking.TourID}).Decode(&booking.Tour)
	c.userCollection.FindOne(context.Background(), bson.M{"_id": booking.UserID}).Decode(&booking.User)

	ctx.JSON(200, gin.H{
		"status": "success",
		"data": gin.H{
			"booking": booking.ToResponse(),
		},
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
		PriceKobo: int64(req.Price * 100),
		Status:    models.BookingPaid,
		Reference: fmt.Sprintf("ADMIN-%d", time.Now().UnixNano()),
	}

	if err := booking.BeforeSave(); err != nil {
		ctx.Error(utils.NewBadRequestError(err.Error()))
		return
	}

	_, err = c.bookingCollection.InsertOne(context.Background(), booking)
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	ctx.JSON(201, gin.H{
		"status": "success",
		"data": gin.H{
			"booking": booking.ToResponse(),
		},
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

	var updateData bson.M
	if err := ctx.ShouldBindJSON(&updateData); err != nil {
		ctx.Error(utils.NewBadRequestError("Invalid request: " + err.Error()))
		return
	}

	// Only allow updating status
	allowedFields := map[string]bool{
		"status": true,
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

	filteredUpdate["updatedAt"] = time.Now()
	update := bson.M{"$set": filteredUpdate}

	var updatedBooking models.Booking
	err = c.bookingCollection.FindOneAndUpdate(
		context.Background(),
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

	ctx.JSON(200, gin.H{
		"status": "success",
		"data": gin.H{
			"booking": updatedBooking.ToResponse(),
		},
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

	result, err := c.bookingCollection.DeleteOne(context.Background(), bson.M{"_id": objID})
	if err != nil {
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	if result.DeletedCount == 0 {
		ctx.Error(utils.NewNotFoundError("Booking not found"))
		return
	}

	ctx.JSON(204, gin.H{"status": "success", "data": nil})
}

// TestCreateBooking - POST /api/v1/bookings/test/:tourId
// Simple test endpoint that creates a booking directly
func (c *BookingController) TestCreateBooking(ctx *gin.Context) {
	log.Println("=== TestCreateBooking called ===")

	tourID := ctx.Param("tourId")
	log.Printf("Tour ID: %s", tourID)

	tourObjID, err := primitive.ObjectIDFromHex(tourID)
	if err != nil {
		log.Printf("Invalid tour ID: %v", err)
		ctx.Error(utils.NewBadRequestError("Invalid tour ID"))
		return
	}

	// Get user ID from context
	userID, exists := ctx.Get("userID")
	if !exists {
		log.Println("User not authenticated")
		ctx.Error(utils.NewUnauthorizedError("Not authenticated"))
		return
	}

	userIDStr := userID.(string)
	userObjID, _ := primitive.ObjectIDFromHex(userIDStr)
	log.Printf("User ID: %s", userIDStr)

	// Get tour details
	var tour models.Tour
	err = c.tourCollection.FindOne(context.Background(), bson.M{"_id": tourObjID}).Decode(&tour)
	if err != nil {
		log.Printf("Tour not found: %v", err)
		ctx.Error(utils.NewNotFoundError("Tour not found"))
		return
	}

	log.Printf("Tour found: %s, Price: %.2f", tour.Name, tour.Price)

	// Create booking directly
	booking := models.Booking{
		ID:        primitive.NewObjectID(),
		TourID:    tourObjID,
		UserID:    userObjID,
		Price:     tour.Price,
		PriceKobo: int64(tour.Price * 100),
		Status:    models.BookingPaid,
		Reference: fmt.Sprintf("TEST-%d", time.Now().UnixNano()),
	}

	if err := booking.BeforeSave(); err != nil {
		log.Printf("BeforeSave error: %v", err)
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	_, err = c.bookingCollection.InsertOne(context.Background(), booking)
	if err != nil {
		log.Printf("Insert error: %v", err)
		ctx.Error(utils.NewInternalServerError(err))
		return
	}

	log.Printf("Booking created successfully with ID: %s", booking.ID.Hex())

	ctx.JSON(201, gin.H{
		"status": "success",
		"data": gin.H{
			"booking": booking.ToResponse(),
		},
	})
}

// MockPaymentPage - GET /api/v1/bookings/mock-payment
func (c *BookingController) MockPaymentPage(ctx *gin.Context) {
	reference := ctx.Query("reference")

	html := `
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
			.footer { margin-top: 30px; font-size: 12px; color: #666; }
		</style>
	</head>
	<body>
		<div class="container">
			<h1>🔧 Mock Payment Page</h1>
			<p>This is a simulated payment page for testing.</p>
			
			<div class="info-box">
				<p><strong>Test Reference:</strong> ` + reference + `</p>
				<p><strong>Amount:</strong> ₦10,000 (Test Mode)</p>
			</div>
			
			<div class="success-box">
				<h2>✅ Test Mode</h2>
				<p>Click the button below to simulate a successful payment.</p>
				<button onclick="completePayment()">Complete Test Payment</button>
			</div>
			
			<div class="footer">
				<p>This is a mock payment page for development testing.</p>
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
							alert('Payment verification failed: ' + (data.message || 'Unknown error'));
						}
					})
					.catch(err => {
						alert('Error: ' + err.message);
					});
			}
		</script>
	</body>
	</html>
	`

	ctx.Header("Content-Type", "text/html")
	ctx.String(200, html)
}

// BookingSuccess - GET /api/v1/booking-success
func (c *BookingController) BookingSuccess(ctx *gin.Context) {
	reference := ctx.Query("reference")

	html := `
	<!DOCTYPE html>
	<html>
	<head>
		<title>Booking Successful</title>
		<style>
			body { font-family: Arial; text-align: center; padding: 50px; }
			.success-box { background: #d4edda; color: #155724; padding: 40px; border-radius: 5px; max-width: 500px; margin: auto; }
			button { background: #007bff; color: white; padding: 12px 25px; border: none; cursor: pointer; border-radius: 5px; font-size: 16px; }
			button:hover { background: #0056b3; }
		</style>
	</head>
	<body>
		<div class="success-box">
			<h1>✅ Booking Successful!</h1>
			<p>Your tour has been booked successfully.</p>
			<p><strong>Reference:</strong> ` + reference + `</p>
			<br>
			<button onclick="window.location.href='/api/v1/tours'">View Tours</button>
		</div>
	</body>
	</html>
	`

	ctx.Header("Content-Type", "text/html")
	ctx.String(200, html)
}
