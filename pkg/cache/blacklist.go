package cache

import (
	"time"

	"github.com/patrickmn/go-cache"
)

var TokenBlacklist *cache.Cache

func InitTokenBlacklist() {
	// Clean up expired tokens every 10 minutes
	TokenBlacklist = cache.New(24*time.Hour, 10*time.Minute)
}

// AddToBlacklist adds a token to the blacklist
func AddToBlacklist(tokenString string, expiration time.Time) {
	ttl := time.Until(expiration)
	if ttl > 0 {
		TokenBlacklist.Set(tokenString, true, ttl)
	}
}

// IsBlacklisted checks if a token is blacklisted
func IsBlacklisted(tokenString string) bool {
	_, found := TokenBlacklist.Get(tokenString)
	return found
}
