package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/paularinzee/natour/internal/config"
	"github.com/paularinzee/natour/internal/controllers"
	"github.com/paularinzee/natour/internal/middleware"

	"github.com/paularinzee/natour/pkg/cache"
	"github.com/paularinzee/natour/pkg/email"
)

// @title Tour Booking API
// @version 1.0
// @description Tour booking application API
// @host localhost:8080
// @BasePath /api/v1

func main() {
	// Initialize token blacklist cache
	cache.InitTokenBlacklist()

	// Load config
	cfg := config.LoadConfig()

	// Connect to MongoDB
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		log.Fatal("Failed to connect to MongoDB:", err)
	}
	defer client.Disconnect(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = client.Ping(ctx, nil)
	if err != nil {
		log.Fatal("Could not connect to MongoDB:", err)
	}
	log.Println("Connected to MongoDB")

	db := client.Database(cfg.DBName)

	// Create indexes
	createIndexes(db)

	// Initialize controllers
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "your-secret-key-change-in-production"
	}
	jwtExpiresIn := 24 * time.Hour

	emailSender := email.NewMockEmailSender()

	authController := controllers.NewAuthController(db, jwtSecret, jwtExpiresIn, emailSender)
	tourController := controllers.NewTourController(db)
	reviewController := controllers.NewReviewController(db)
	userController := controllers.NewUserController(db)

	// Initialize Booking Controller
	bookingController, err := controllers.NewBookingController(db)
	if err != nil {
		log.Fatal("Failed to initialize booking controller:", err)
	}

	// Setup routes
	r := gin.Default()
	r.Use(middleware.ErrorHandler())
	r.Static("/uploads", "./public")

	// API routes
	api := r.Group("/api/v1")
	{

		// ========== PUBLIC ROUTES ==========
		api.POST("/auth/signup", authController.SignUp)
		api.POST("/auth/login", authController.Login)
		api.POST("/auth/forgotpassword", authController.ForgotPassword)
		api.PATCH("/auth/resetpassword/:token", authController.ResetPassword)

		// Public tour routes
		api.GET("/tours", tourController.GetAllTours)
		api.GET("/tours/top-5-cheap", tourController.AliasTopTours, tourController.GetAllTours)
		api.GET("/tours/tour-stats", tourController.GetTourStats)
		api.GET("/tours/:id", tourController.GetTour)
		api.GET("/tours-within/:distance/center/:latlng/unit/:unit", tourController.GetToursWithin)
		api.GET("/distances/:latlng/unit/:unit", tourController.GetDistances)
	}

	// ========== PROTECTED ROUTES (require authentication) ==========
	// User self-service
	api.GET("/auth/me", middleware.AuthMiddleware(jwtSecret), authController.GetMe)
	api.PATCH("/auth/updateme", middleware.AuthMiddleware(jwtSecret), authController.UpdateMe)
	api.PATCH("/auth/updatepassword", middleware.AuthMiddleware(jwtSecret), authController.UpdatePassword)
	api.POST("/auth/logout", middleware.AuthMiddleware(jwtSecret), authController.Logout)
	api.DELETE("/auth/deleteme", middleware.AuthMiddleware(jwtSecret), userController.DeleteMe)

	// Monthly plan (requires guide or admin)
	api.GET("/tours/monthly-plan/:year",
		middleware.AuthMiddleware(jwtSecret),
		middleware.AllowRoles("admin", "lead-guide", "guide"),
		tourController.GetMonthlyPlan)

	// Tour management (requires lead-guide or admin)
	api.POST("/tours",
		middleware.AuthMiddleware(jwtSecret),
		middleware.AllowRoles("admin", "lead-guide"),
		middleware.UploadTourImages(),
		tourController.CreateTour)

	api.PATCH("/tours/:id",
		middleware.AuthMiddleware(jwtSecret),
		middleware.AllowRoles("admin", "lead-guide"),
		middleware.UploadTourImages(),
		tourController.UpdateTour)

	api.DELETE("/tours/:id",
		middleware.AuthMiddleware(jwtSecret),
		middleware.AllowRoles("admin", "lead-guide"),
		tourController.DeleteTour)

	// Review routes
	api.GET("/reviews", middleware.AuthMiddleware(jwtSecret), reviewController.GetAllReviews)
	api.GET("/reviews/:id", middleware.AuthMiddleware(jwtSecret), reviewController.GetReview)
	api.GET("/tours/:id/reviews", middleware.AuthMiddleware(jwtSecret), reviewController.GetTourReviews)
	api.POST("/tours/:id/reviews",
		middleware.AuthMiddleware(jwtSecret),
		middleware.AllowRoles("user"),
		reviewController.CreateReview)
	api.PATCH("/reviews/:id", middleware.AuthMiddleware(jwtSecret), reviewController.UpdateReview)
	api.DELETE("/reviews/:id", middleware.AuthMiddleware(jwtSecret), reviewController.DeleteReview)

	// ========== ADMIN ONLY ROUTES ==========
	api.GET("/users",
		middleware.AuthMiddleware(jwtSecret),
		middleware.AllowRoles("admin"),
		userController.GetAllUsers)

	api.POST("/users",
		middleware.AuthMiddleware(jwtSecret),
		middleware.AllowRoles("admin"),
		userController.CreateUser)

	api.GET("/users/:id",
		middleware.AuthMiddleware(jwtSecret),
		middleware.AllowRoles("admin"),
		userController.GetUser)

	api.PATCH("/users/:id",
		middleware.AuthMiddleware(jwtSecret),
		middleware.AllowRoles("admin"),
		userController.UpdateUser)

	api.DELETE("/users/:id",
		middleware.AuthMiddleware(jwtSecret),
		middleware.AllowRoles("admin"),
		userController.DeleteUser)

	// ========== BOOKING ROUTES ==========
	// Checkout session (authenticated users)
	api.GET("/bookings/checkout-session/:tourId",
		middleware.AuthMiddleware(jwtSecret),
		bookingController.GetCheckoutSession)

	// Payment verification callback - NO AUTH (public)
	// This is called by Paystack or mock payment page
	api.GET("/bookings/verify-payment", bookingController.VerifyPayment)

	// Webhook for Paystack - NO AUTH (public)
	api.POST("/bookings/webhook", bookingController.Webhook)

	// Mock payment page - NO AUTH (public for testing)
	api.GET("/bookings/mock-payment", bookingController.MockPaymentPage)

	// Booking success page - NO AUTH (public for testing)
	api.GET("/booking-success", bookingController.BookingSuccess)

	// Admin only booking management (requires auth and admin role)
	api.GET("/bookings",
		middleware.AuthMiddleware(jwtSecret),
		middleware.AllowRoles("admin", "lead-guide"),
		bookingController.GetAllBookings)

	api.POST("/bookings",
		middleware.AuthMiddleware(jwtSecret),
		middleware.AllowRoles("admin", "lead-guide"),
		bookingController.CreateBooking)

	api.GET("/bookings/:id",
		middleware.AuthMiddleware(jwtSecret),
		middleware.AllowRoles("admin", "lead-guide"),
		bookingController.GetBooking)

	api.PATCH("/bookings/:id",
		middleware.AuthMiddleware(jwtSecret),
		middleware.AllowRoles("admin", "lead-guide"),
		bookingController.UpdateBooking)

	api.DELETE("/bookings/:id",
		middleware.AuthMiddleware(jwtSecret),
		middleware.AllowRoles("admin", "lead-guide"),
		bookingController.DeleteBooking)

	// Test endpoint (requires auth)
	api.POST("/bookings/test/:tourId",
		middleware.AuthMiddleware(jwtSecret),
		bookingController.TestCreateBooking)
	log.Printf("Server starting on port %s", cfg.Port)

	// Debug: Print all routes
	log.Println("\n=== ALL REGISTERED ROUTES ===")
	for _, route := range r.Routes() {
		log.Printf("%-6s %s", route.Method, route.Path)
	}
	log.Println("=============================")

	r.Run(":" + cfg.Port)
}

func createIndexes(db *mongo.Database) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	toursCollection := db.Collection("tours")
	usersCollection := db.Collection("users")
	reviewsCollection := db.Collection("reviews")
	bookingsCollection := db.Collection("bookings")

	// Create 2dsphere index for startLocation
	_, err := toursCollection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "startLocation", Value: "2dsphere"}},
		Options: options.Index().SetName("startLocation_2dsphere"),
	})
	if err != nil {
		log.Println("Warning: Failed to create 2dsphere index:", err)
	} else {
		log.Println("✓ Created 2dsphere index on startLocation")
	}

	// Create compound index for price and ratingsAverage
	_, err = toursCollection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "price", Value: 1},
			{Key: "ratingsAverage", Value: -1},
		},
		Options: options.Index().SetName("price_1_ratingsAverage_-1"),
	})
	if err != nil {
		log.Println("Warning: Failed to create price/ratings index:", err)
	} else {
		log.Println("✓ Created compound index on price and ratingsAverage")
	}

	// Create index on slug
	_, err = toursCollection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "slug", Value: 1}},
		Options: options.Index().SetName("slug_unique").SetUnique(true),
	})
	if err != nil {
		log.Println("Warning: Failed to create slug index:", err)
	} else {
		log.Println("✓ Created unique index on slug")
	}

	// Create unique index on email for users
	_, err = usersCollection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "email", Value: 1}},
		Options: options.Index().SetName("email_unique").SetUnique(true),
	})
	if err != nil {
		log.Println("Warning: Failed to create email index:", err)
	} else {
		log.Println("✓ Created unique index on email")
	}

	// Create unique compound index for reviews
	_, err = reviewsCollection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "tour", Value: 1},
			{Key: "user", Value: 1},
		},
		Options: options.Index().SetName("tour_user_unique").SetUnique(true),
	})
	if err != nil {
		log.Println("Warning: Failed to create review compound index:", err)
	} else {
		log.Println("✓ Created unique compound index on review (tour + user)")
	}

	// Create index on booking reference (unique)
	_, err = bookingsCollection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "reference", Value: 1}},
		Options: options.Index().SetName("reference_unique").SetUnique(true),
	})
	if err != nil {
		log.Println("Warning: Failed to create reference index:", err)
	} else {
		log.Println("✓ Created unique index on booking reference")
	}

	// Create compound index on user and tour for bookings
	_, err = bookingsCollection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "user", Value: 1},
			{Key: "tour", Value: 1},
		},
		Options: options.Index().SetName("user_tour_idx"),
	})
	if err != nil {
		log.Println("Warning: Failed to create user_tour index:", err)
	} else {
		log.Println("✓ Created index on user and tour for bookings")
	}
}
