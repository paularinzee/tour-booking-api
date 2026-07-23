package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/paularinzee/natour/internal/config"
	"github.com/paularinzee/natour/internal/controllers"
	"github.com/paularinzee/natour/internal/middleware"

	"github.com/paularinzee/natour/pkg/cache"
	"github.com/paularinzee/natour/pkg/email"

	// Document package generation point parsed by swag CLI
	_ "github.com/paularinzee/natour/docs"
)

// @title Tour Booking API
// @version 1.0
// @description Tour booking application API
// @termsOfService  http://swagger.io/terms/

// @contact.name   API Support
// @contact.url    http://www.swagger.io/support
// @contact.email  support@swagger.io

// @host localhost:8080
// @BasePath /api/v1

// @securityDefinitions.apikey Bearer
// @in header
// @name Authorization
// @description Type "Bearer " followed by a space and your token.
func main() {
	// Initialize token blacklist cache
	cache.InitTokenBlacklist()

	// Load config
	cfg := config.LoadConfig()

	// Set up a unified context timeout for database startup connections
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStartup()

	// Connect to MongoDB
	client, err := mongo.Connect(startupCtx, options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		log.Fatal("Failed to connect to MongoDB:", err)
	}
	defer func() {
		if err := client.Disconnect(context.Background()); err != nil {
			log.Println("Error disconnecting from MongoDB:", err)
		}
	}()

	err = client.Ping(startupCtx, nil)
	if err != nil {
		log.Fatal("Could not connect to MongoDB:", err)
	}
	log.Println("Connected to MongoDB")

	db := client.Database(cfg.DBName)

	// Create indexes
	createIndexes(db)

	// Initialize configurations & values
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "your-secret-key-change-in-production"
	}
	jwtExpiresIn := 24 * time.Hour

	emailSender := email.NewMockEmailSender()

	// Initialize controllers
	authController := controllers.NewAuthController(db, jwtSecret, jwtExpiresIn, emailSender)
	tourController := controllers.NewTourController(db)
	reviewController := controllers.NewReviewController(db)
	userController := controllers.NewUserController(db)

	bookingController, err := controllers.NewBookingController(db)
	if err != nil {
		log.Fatal("Failed to initialize booking controller:", err)
	}

	// ========== ROUTER INITIALIZATION ==========
	// FIX: Instantiate exactly once globally to avoid compiler collision or overwritten settings
	r := gin.Default()

	// Register Swagger route
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// ========== GLOBAL MIDDLEWARES & STATICS ==========
	// Allow all origins (development settings)
	r.Use(cors.Default())

	// Or custom CORS configuration for production:
	// r.Use(cors.New(cors.Config{
	//  AllowOrigins:     []string{"http://localhost:3000", "https://yourdomain.com"},
	//  AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
	//  AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
	//  ExposeHeaders:    []string{"Content-Length", "X-RateLimit-Limit", "X-RateLimit-Remaining"},
	//  AllowCredentials: true,
	//  MaxAge:           12 * time.Hour,
	// }))

	r.Use(gin.Logger())
	r.Use(middleware.ErrorHandler())
	r.Static("/uploads", "./public")

	// API base group
	api := r.Group("/api/v1")
	{
		// ========== PUBLIC ROUTES (Strict rate limit - 50 per minute) ==========
		publicGroup := api.Group("/")
		publicGroup.Use(middleware.PublicLimiter)
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

		// ========== PROTECTED ROUTES (Default rate limit - 100 per minute) ==========
		protectedGroup := api.Group("/")
		protectedGroup.Use(middleware.AuthMiddleware(jwtSecret))
		protectedGroup.Use(middleware.DefaultLimiter)
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
		adminGroup.Use(middleware.AdminLimiter)
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

		// Mock payment endpoints (Conditional validation execution should happen inside the method bodies)
		api.GET("/bookings/mock-payment", bookingController.MockPaymentPage)
	}

	log.Printf("Server starting on port %s", cfg.Port)

	// Debug route mapping trace printer
	log.Println("\n=== ALL REGISTERED ROUTES ===")
	for _, route := range r.Routes() {
		log.Printf("%-6s %s", route.Method, route.Path)
	}
	log.Println("=============================")

	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatal("Failed to start router instance engine: ", err)
	}
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
