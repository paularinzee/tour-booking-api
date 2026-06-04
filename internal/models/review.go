package models

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Review represents the review model
type Review struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Review    string             `bson:"review" json:"review" binding:"required"`
	Rating    int                `bson:"rating" json:"rating" binding:"min=1,max=5"`
	CreatedAt time.Time          `bson:"createdAt" json:"createdAt"`
	TourID    primitive.ObjectID `bson:"tour" json:"tourId" binding:"required"`
	UserID    primitive.ObjectID `bson:"user" json:"userId"`

	// Populated fields (not stored in DB)
	Tour *Tour `bson:"-" json:"tour,omitempty"`
	User *User `bson:"-" json:"user,omitempty"`
}

// ReviewResponse is the review data returned to client
type ReviewResponse struct {
	ID        primitive.ObjectID `json:"id"`
	Review    string             `json:"review"`
	Rating    int                `json:"rating"`
	CreatedAt time.Time          `json:"createdAt"`
	TourID    primitive.ObjectID `json:"tourId"`
	UserID    primitive.ObjectID `json:"userId"`
	User      *UserResponse      `json:"user,omitempty"`
}

// ToResponse converts Review to ReviewResponse
func (r *Review) ToResponse() ReviewResponse {
	resp := ReviewResponse{
		ID:        r.ID,
		Review:    r.Review,
		Rating:    r.Rating,
		CreatedAt: r.CreatedAt,
		TourID:    r.TourID,
		UserID:    r.UserID,
	}

	if r.User != nil {
		userResp := r.User.ToResponse()
		resp.User = &userResp
	}

	return resp
}

// BeforeSave runs validations before saving
func (r *Review) BeforeSave() error {
	if r.Review == "" {
		return fmt.Errorf("review cannot be empty")
	}

	if r.Rating < 1 || r.Rating > 5 {
		return fmt.Errorf("rating must be between 1 and 5")
	}

	if r.TourID.IsZero() {
		return fmt.Errorf("review must belong to a tour")
	}

	if r.UserID.IsZero() {
		return fmt.Errorf("review must belong to a user")
	}

	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}

	return nil
}
