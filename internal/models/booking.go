package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type BookingStatus string

const (
	BookingPending   BookingStatus = "pending"
	BookingPaid      BookingStatus = "paid"
	BookingCancelled BookingStatus = "cancelled"
	BookingRefunded  BookingStatus = "refunded"
)

type Booking struct {
	ID               primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	TourID           primitive.ObjectID `bson:"tour" json:"tour"`
	UserID           primitive.ObjectID `bson:"user" json:"user"`
	Price            float64            `bson:"price" json:"price"`         // Price in NGN
	PriceKobo        int64              `bson:"priceKobo" json:"priceKobo"` // Price in Kobo (1 NGN = 100 Kobo)
	Status           BookingStatus      `bson:"status" json:"status"`
	Reference        string             `bson:"reference" json:"reference"` // Paystack transaction reference
	AccessCode       string             `bson:"accessCode,omitempty" json:"accessCode,omitempty"`
	AuthorizationURL string             `bson:"authorizationUrl,omitempty" json:"authorizationUrl,omitempty"`
	CreatedAt        time.Time          `bson:"createdAt" json:"createdAt"`
	UpdatedAt        time.Time          `bson:"updatedAt" json:"updatedAt"`

	// Populated fields (not stored)
	Tour *Tour `bson:"-" json:"tour,omitempty"`
	User *User `bson:"-" json:"user,omitempty"`
}

type BookingResponse struct {
	ID               primitive.ObjectID `json:"id"`
	TourID           primitive.ObjectID `json:"tourId"`
	UserID           primitive.ObjectID `json:"userId"`
	Price            float64            `json:"price"`
	Status           BookingStatus      `json:"status"`
	Reference        string             `json:"reference"`
	AccessCode       string             `json:"accessCode,omitempty"`
	AuthorizationURL string             `json:"authorizationUrl,omitempty"`
	CreatedAt        time.Time          `json:"createdAt"`
	Tour             *TourResponse      `json:"tour,omitempty"`
	User             *UserResponse      `json:"user,omitempty"`
}

func (b *Booking) ToResponse() BookingResponse {
	resp := BookingResponse{
		ID:               b.ID,
		TourID:           b.TourID,
		UserID:           b.UserID,
		Price:            b.Price,
		Status:           b.Status,
		Reference:        b.Reference,
		AccessCode:       b.AccessCode,
		AuthorizationURL: b.AuthorizationURL,
		CreatedAt:        b.CreatedAt,
	}

	if b.Tour != nil {
		tourResp := b.Tour.ToResponse()
		resp.Tour = &tourResp
	}

	if b.User != nil {
		userResp := b.User.ToResponse()
		resp.User = &userResp
	}

	return resp
}

func (b *Booking) BeforeSave() error {
	if b.CreatedAt.IsZero() {
		b.CreatedAt = time.Now()
	}
	b.UpdatedAt = time.Now()

	if b.Status == "" {
		b.Status = BookingPending
	}

	return nil
}
