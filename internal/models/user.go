package models

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/crypto/bcrypt"
)

// UserRole defines the role type
type UserRole string

const (
	RoleUser      UserRole = "user"
	RoleGuide     UserRole = "guide"
	RoleLeadGuide UserRole = "lead-guide"
	RoleAdmin     UserRole = "admin"
)

// ValidRoles returns all valid user roles
func ValidRoles() []UserRole {
	return []UserRole{RoleUser, RoleGuide, RoleLeadGuide, RoleAdmin}
}

// User represents the user model
type User struct {
	ID                   primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Name                 string             `bson:"name" json:"name"`
	Email                string             `bson:"email" json:"email"`
	Photo                string             `bson:"photo" json:"photo"`
	Role                 UserRole           `bson:"role" json:"role"`
	Password             string             `bson:"password,omitempty" json:"-"`
	PasswordConfirm      string             `bson:"-" json:"-"`
	PasswordChangedAt    *time.Time         `bson:"passwordChangedAt,omitempty" json:"-"`
	PasswordResetToken   string             `bson:"passwordResetToken,omitempty" json:"-"`
	PasswordResetExpires *time.Time         `bson:"passwordResetExpires,omitempty" json:"-"`
	Active               bool               `bson:"active" json:"active"`
	CreatedAt            time.Time          `bson:"createdAt" json:"createdAt"`
	UpdatedAt            time.Time          `bson:"updatedAt" json:"updatedAt"`
}

// UserResponse is the user data returned to client (excludes sensitive fields)
type UserResponse struct {
	ID        primitive.ObjectID `json:"id"`
	Name      string             `json:"name"`
	Email     string             `json:"email"`
	Photo     string             `json:"photo"`
	Role      UserRole           `json:"role"`
	CreatedAt time.Time          `json:"createdAt"`
}

// ToResponse converts User to UserResponse (excludes sensitive data)
func (u *User) ToResponse() UserResponse {
	return UserResponse{
		ID:        u.ID,
		Name:      u.Name,
		Email:     u.Email,
		Photo:     u.Photo,
		Role:      u.Role,
		CreatedAt: u.CreatedAt,
	}
}

// HashPassword hashes the user's password
func (u *User) HashPassword() error {
	if u.Password == "" {
		return nil
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(u.Password), 12)
	if err != nil {
		return err
	}

	u.Password = string(hashedPassword)
	u.PasswordConfirm = "" // Clear password confirm field
	return nil
}

// CheckPassword compares a candidate password with the stored hash
func (u *User) CheckPassword(candidatePassword string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(candidatePassword))
	return err == nil
}

// ChangedPasswordAfter checks if password was changed after JWT was issued
func (u *User) ChangedPasswordAfter(JWTTimestamp int64) bool {
	if u.PasswordChangedAt == nil {
		return false
	}

	changedTimestamp := u.PasswordChangedAt.Unix()
	return JWTTimestamp < changedTimestamp
}

// CreatePasswordResetToken generates a password reset token
func (u *User) CreatePasswordResetToken() (string, error) {
	// Generate random token
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	resetToken := hex.EncodeToString(bytes)

	// Hash the token for storage
	hash := sha256.Sum256([]byte(resetToken))
	u.PasswordResetToken = hex.EncodeToString(hash[:])

	// Set expiration (10 minutes)
	expiresAt := time.Now().Add(10 * time.Minute)
	u.PasswordResetExpires = &expiresAt

	return resetToken, nil
}

// VerifyPasswordResetToken checks if a token is valid and not expired
func (u *User) VerifyPasswordResetToken(token string) bool {
	if u.PasswordResetToken == "" || u.PasswordResetExpires == nil {
		return false
	}

	// Check if expired
	if time.Now().After(*u.PasswordResetExpires) {
		return false
	}

	// Hash the provided token and compare
	hash := sha256.Sum256([]byte(token))
	hashedToken := hex.EncodeToString(hash[:])

	return u.PasswordResetToken == hashedToken
}

// ClearPasswordResetToken clears the reset token after use
func (u *User) ClearPasswordResetToken() {
	u.PasswordResetToken = ""
	u.PasswordResetExpires = nil
}

// UpdatePasswordChangedAt updates the password changed timestamp
func (u *User) UpdatePasswordChangedAt() {
	now := time.Now()
	// Subtract 1 second to ensure token is created after password change
	u.PasswordChangedAt = &now
}

// IsActive returns whether the user account is active
func (u *User) IsActive() bool {
	return u.Active
}

// Deactivate sets the user account as inactive
func (u *User) Deactivate() {
	u.Active = false
}

// Activate sets the user account as active
func (u *User) Activate() {
	u.Active = true
}

// HasRole checks if user has a specific role
func (u *User) HasRole(role UserRole) bool {
	return u.Role == role
}

// IsAdmin checks if user is an admin
func (u *User) IsAdmin() bool {
	return u.Role == RoleAdmin
}

// IsGuide checks if user is a guide or lead-guide
func (u *User) IsGuide() bool {
	return u.Role == RoleGuide || u.Role == RoleLeadGuide
}

// GenerateJWT generates a JWT token for the user
func (u *User) GenerateJWT(secret string, expiresIn time.Duration) (string, error) {
	// Create token with claims
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"id":    u.ID.Hex(),
		"email": u.Email,
		"role":  u.Role,
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(expiresIn).Unix(),
	})

	// Sign token with secret
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", err
	}

	return tokenString, nil
}

// BeforeSave runs validations and hashes password before saving
func (u *User) BeforeSave(isNew bool) error {
	// Validate required fields
	if u.Name == "" {
		return fmt.Errorf("name is required")
	}

	if u.Email == "" {
		return fmt.Errorf("email is required")
	}

	// Validate email format
	if !isValidEmail(u.Email) {
		return fmt.Errorf("please provide a valid email")
	}

	// Validate role
	validRole := false
	for _, role := range ValidRoles() {
		if u.Role == role {
			validRole = true
			break
		}
	}
	if !validRole && u.Role != "" {
		return fmt.Errorf("invalid role")
	}

	// Set default role if not set
	if u.Role == "" {
		u.Role = RoleUser
	}

	// Set default photo if not set
	if u.Photo == "" {
		u.Photo = "default.jpg"
	}

	// Set default active status
	if isNew {
		u.Active = true
	}

	// Set timestamps
	if isNew {
		u.CreatedAt = time.Now()
	}
	u.UpdatedAt = time.Now()

	// Hash password if it's being set/changed
	if u.Password != "" {
		// Validate password length
		if len(u.Password) < 8 {
			return fmt.Errorf("password must be at least 8 characters")
		}

		// Validate password confirmation
		if u.Password != u.PasswordConfirm {
			return fmt.Errorf("passwords are not the same")
		}

		if err := u.HashPassword(); err != nil {
			return err
		}

		// Update password changed timestamp
		if !isNew {
			u.UpdatePasswordChangedAt()
		}
	}

	return nil
}

// isValidEmail validates email format
func isValidEmail(email string) bool {
	emailRegex := regexp.MustCompile(`^[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}$`)
	return emailRegex.MatchString(strings.ToLower(email))
}

// NormalizeEmail converts email to lowercase
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// SanitizeName removes extra spaces from name
func SanitizeName(name string) string {
	return strings.TrimSpace(name)
}
