package config

import (
	"log"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

type Config struct {
	MongoURI string
	DBName   string
	Port     string
}

func LoadConfig() *Config {
	err := godotenv.Load()
	if err != nil {
		err = godotenv.Load(filepath.Join("..", ".env"))
		if err != nil {
			log.Println("No .env file found, using environment variables")
		}
	}

	return &Config{
		MongoURI: getEnv("MONGODB_URI", "mongodb://localhost:27017"),
		DBName:   getEnv("DB_NAME", "tour-booking"),
		Port:     getEnv("PORT", "8080"),
	}

}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
