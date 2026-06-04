package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/gin-contrib/cors"
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

	// ========== CORS CONFIGURATION ==========
	// Allow all origins (development)
	r.Use(cors.Default())

	// Or custom CORS configuration for production:
	// r.Use(cors.New(cors.Config{
	// 	AllowOrigins:     []string{"http://localhost:3000", "https://yourdomain.com"},
	// 	AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
	// 	AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
	// 	ExposeHeaders:    []string{"Content-Length", "X-RateLimit-Limit", "X-RateLimit-Remaining"},
	// 	AllowCredentials: true,
	// 	MaxAge:           12 * time.Hour,
	// }))

	// ========== LOGGER MIDDLEWARE ==========
	// Option 1: Gin's default logger (simple)
	r.Use(gin.Logger())

	// Option 2: Custom JSON logger (uncomment to use)
	// loggerConfig := middleware.DefaultLoggerConfig()
	// loggerConfig.LogRequestBody = false
	// loggerConfig.PrettyPrint = true
	// r.Use(middleware.Logger(loggerConfig))

	// Option 3: Simple development logger
	// r.Use(middleware.SimpleLogger())

	r.Use(middleware.ErrorHandler())
	r.Static("/uploads", "./public")

	// API routes
	api := r.Group("/api/v1")
	{
		// ========== PUBLIC ROUTES (Strict rate limit - 50 per minute) ==========
		publicGroup := api.Group("/")
		publicGroup.Use(middleware.PublicLimiter) // Changed from RateLimiter to PublicLimiter
		{
			publicGroup.POST("/auth/signup", authController.SignUp)
			publicGroup.POST("/auth/login", authController.Login)
			publicGroup.POST("/auth/forgotpassword", authController.ForgotPassword)
			publicGroup.PATCH("/auth/resetpassword/:token", authController.ResetPassword)

			// Public tour routes
			publicGroup.GET("/tours", tourController.GetAllTours)
			publicGroup.GET("/tours/top-5-cheap", tourController.AliasTopTours, tourController.GetAllTours)
			publicGroup.GET("/tours/tour-stats", tourController.GetTourStats)
			publicGroup.GET("/tours/:id", tourController.GetTour)
			publicGroup.GET("/tours-within/:distance/center/:latlng/unit/:unit", tourController.GetToursWithin)
			publicGroup.GET("/distances/:latlng/unit/:unit", tourController.GetDistances)
		}
	}

	// ========== PROTECTED ROUTES (Default rate limit - 100 per minute) ==========
	protectedGroup := api.Group("/")
	protectedGroup.Use(middleware.AuthMiddleware(jwtSecret))
	protectedGroup.Use(middleware.DefaultLimiter) // Changed from RateLimiter to DefaultLimiter
	{
		// User self-service
		protectedGroup.GET("/auth/me", authController.GetMe)
		protectedGroup.PATCH("/auth/updateme", authController.UpdateMe)
		protectedGroup.PATCH("/auth/updatepassword", authController.UpdatePassword)
		protectedGroup.POST("/auth/logout", authController.Logout)
		protectedGroup.DELETE("/auth/deleteme", userController.DeleteMe)

		// Monthly plan (requires guide or admin)
		protectedGroup.GET("/tours/monthly-plan/:year",
			middleware.AllowRoles("admin", "lead-guide", "guide"),
			tourController.GetMonthlyPlan)

		// Review routes
		protectedGroup.GET("/reviews", reviewController.GetAllReviews)
		protectedGroup.GET("/reviews/:id", reviewController.GetReview)
		protectedGroup.GET("/tours/:id/reviews", reviewController.GetTourReviews)
		protectedGroup.POST("/tours/:id/reviews",
			middleware.AllowRoles("user"),
			reviewController.CreateReview)
		protectedGroup.PATCH("/reviews/:id", reviewController.UpdateReview)
		protectedGroup.DELETE("/reviews/:id", reviewController.DeleteReview)

		// Booking checkout
		protectedGroup.GET("/bookings/checkout-session/:tourId", bookingController.GetCheckoutSession)
	}

	// ========== ADMIN ROUTES (Higher rate limit - 200 per minute) ==========
	adminGroup := api.Group("/")
	adminGroup.Use(middleware.AuthMiddleware(jwtSecret))
	adminGroup.Use(middleware.AllowRoles("admin", "lead-guide"))
	adminGroup.Use(middleware.AdminLimiter) // Changed from RateLimiter to AdminLimiter
	{
		// Tour management
		adminGroup.POST("/tours",
			middleware.UploadTourImages(),
			tourController.CreateTour)

		adminGroup.PATCH("/tours/:id",
			middleware.UploadTourImages(),
			tourController.UpdateTour)

		adminGroup.DELETE("/tours/:id", tourController.DeleteTour)

		// User management
		adminGroup.GET("/users", userController.GetAllUsers)
		adminGroup.POST("/users", userController.CreateUser)
		adminGroup.GET("/users/:id", userController.GetUser)
		adminGroup.PATCH("/users/:id", userController.UpdateUser)
		adminGroup.DELETE("/users/:id", userController.DeleteUser)

		// Booking management
		adminGroup.GET("/bookings", bookingController.GetAllBookings)
		adminGroup.POST("/bookings", bookingController.CreateBooking)
		adminGroup.GET("/bookings/:id", bookingController.GetBooking)
		adminGroup.PATCH("/bookings/:id", bookingController.UpdateBooking)
		adminGroup.DELETE("/bookings/:id", bookingController.DeleteBooking)
	}

	// ========== PUBLIC CALLBACKS (No rate limit) ==========
	// Payment verification callback - called by Paystack
	api.GET("/bookings/verify-payment", bookingController.VerifyPayment)

	// Webhook for Paystack - called by Paystack
	api.POST("/bookings/webhook", bookingController.Webhook)

	// Mock payment page (for testing only)
	api.GET("/bookings/mock-payment", bookingController.MockPaymentPage)

	// Booking success page (for testing only)
	api.GET("/booking-success", bookingController.BookingSuccess)

	// Test endpoint (requires auth, for development)
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
