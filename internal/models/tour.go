package models

import (
	"math"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Tour struct {
	ID              primitive.ObjectID   `bson:"_id,omitempty" json:"id"`
	Name            string               `bson:"name" json:"name"`
	Slug            string               `bson:"slug" json:"slug"`
	Duration        int                  `bson:"duration" json:"duration"`
	RatingsAverage  float64              `bson:"ratingsAverage" json:"ratingsAverage"`
	RatingsQuantity int                  `bson:"ratingsQuantity" json:"ratingsQuantity"`
	MaxGroupSize    int                  `bson:"maxGroupSize" json:"maxGroupSize"`
	Difficulty      string               `bson:"difficulty" json:"difficulty"`
	Price           float64              `bson:"price" json:"price"`
	PriceDiscount   float64              `bson:"priceDiscount" json:"priceDiscount"`
	Summary         string               `bson:"summary" json:"summary"`
	Description     string               `bson:"description" json:"description"`
	ImageCover      string               `bson:"imageCover" json:"imageCover"`
	Images          []string             `bson:"images" json:"images"`
	CreatedAt       time.Time            `bson:"createdAt" json:"createdAt"`
	StartDates      []time.Time          `bson:"startDates" json:"startDates"`
	SecretTour      bool                 `bson:"secretTour" json:"secretTour"`
	StartLocation   StartLocation        `bson:"startLocation" json:"startLocation"`
	Locations       []Location           `bson:"locations" json:"locations"`
	Guides          []primitive.ObjectID `bson:"guides" json:"guides"`
	Reviews         []Review             `bson:"-" json:"reviews,omitempty"`
}

type StartLocation struct {
	Type        string    `bson:"type" json:"type"`
	Coordinates []float64 `bson:"coordinates" json:"coordinates"`
	Address     string    `bson:"address" json:"address"`
	Description string    `bson:"description" json:"description"`
}

type Location struct {
	Type        string    `bson:"type" json:"type"`
	Coordinates []float64 `bson:"coordinates" json:"coordinates"`
	Address     string    `bson:"address" json:"address"`
	Description string    `bson:"description" json:"description"`
	Day         int       `bson:"day" json:"day"`
}

// TourResponse is the tour data returned to client
type TourResponse struct {
	ID              primitive.ObjectID   `json:"id"`
	Name            string               `json:"name"`
	Slug            string               `json:"slug"`
	Duration        int                  `json:"duration"`
	DurationWeeks   float64              `json:"durationWeeks"`
	RatingsAverage  float64              `json:"ratingsAverage"`
	RatingsQuantity int                  `json:"ratingsQuantity"`
	MaxGroupSize    int                  `json:"maxGroupSize"`
	Difficulty      string               `json:"difficulty"`
	Price           float64              `json:"price"`
	PriceDiscount   float64              `json:"priceDiscount"`
	Summary         string               `json:"summary"`
	Description     string               `json:"description"`
	ImageCover      string               `json:"imageCover"`
	Images          []string             `json:"images"`
	CreatedAt       time.Time            `json:"createdAt"`
	StartDates      []time.Time          `json:"startDates"`
	StartLocation   StartLocation        `json:"startLocation"`
	Locations       []Location           `json:"locations"`
	Guides          []primitive.ObjectID `json:"guides,omitempty"`
	Reviews         []ReviewResponse     `json:"reviews,omitempty"`
}

// Validation constants
const (
	DifficultyEasy      = "easy"
	DifficultyMedium    = "medium"
	DifficultyDifficult = "difficult"
	MinNameLength       = 10
	MaxNameLength       = 40
	MinRating           = 1.0
	MaxRating           = 5.0
	DefaultRating       = 4.5
)

var ValidDifficulties = []string{DifficultyEasy, DifficultyMedium, DifficultyDifficult}

// GenerateSlug creates a URL-friendly slug from the tour name
func GenerateSlug(name string) string {
	slug := strings.ToLower(name)
	slug = strings.ReplaceAll(slug, " ", "-")
	slug = strings.ReplaceAll(slug, "_", "-")
	// Remove any special characters
	slug = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, slug)
	// Remove multiple consecutive dashes
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	// Trim dashes from start and end
	slug = strings.Trim(slug, "-")
	return slug
}

// CalculateDurationWeeks returns duration in weeks
func CalculateDurationWeeks(duration int) float64 {
	return float64(duration) / 7
}

// RoundRatingsAverage rounds the rating to 1 decimal place
func RoundRatingsAverage(value float64) float64 {
	return math.Round(value*10) / 10
}

// ValidatePriceDiscount checks if discount is less than price
func ValidatePriceDiscount(price, discount float64) bool {
	return discount < price
}

// ToResponse converts Tour to TourResponse
func (t *Tour) ToResponse() TourResponse {
	// Calculate duration weeks
	durationWeeks := float64(t.Duration) / 7

	// Convert reviews to ReviewResponse if they exist
	var reviewResponses []ReviewResponse
	if len(t.Reviews) > 0 {
		reviewResponses = make([]ReviewResponse, len(t.Reviews))
		for i, review := range t.Reviews {
			reviewResponses[i] = review.ToResponse()
		}
	}

	return TourResponse{
		ID:              t.ID,
		Name:            t.Name,
		Slug:            t.Slug,
		Duration:        t.Duration,
		DurationWeeks:   durationWeeks,
		RatingsAverage:  t.RatingsAverage,
		RatingsQuantity: t.RatingsQuantity,
		MaxGroupSize:    t.MaxGroupSize,
		Difficulty:      t.Difficulty,
		Price:           t.Price,
		PriceDiscount:   t.PriceDiscount,
		Summary:         t.Summary,
		Description:     t.Description,
		ImageCover:      t.ImageCover,
		Images:          t.Images,
		CreatedAt:       t.CreatedAt,
		StartDates:      t.StartDates,
		StartLocation:   t.StartLocation,
		Locations:       t.Locations,
		Guides:          t.Guides,
		Reviews:         reviewResponses,
	}
}

// BeforeSave runs validations before saving a tour
func (t *Tour) BeforeSave() error {
	// Generate slug if name exists and slug is empty
	if t.Name != "" && t.Slug == "" {
		t.Slug = GenerateSlug(t.Name)
	}

	// Set default rating if not set
	if t.RatingsAverage == 0 {
		t.RatingsAverage = DefaultRating
	}

	// Round ratings average
	if t.RatingsAverage != 0 {
		t.RatingsAverage = RoundRatingsAverage(t.RatingsAverage)
	}

	// Set created at if new
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}

	return nil
}
