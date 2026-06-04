package middleware

import (
	"fmt"
	"image/jpeg"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	maxFileSize  = 10 * 1024 * 1024 // 10MB
	uploadPath   = "public/img/tours"
	imageQuality = 90
	imageWidth   = 500
	imageHeight  = 500
)

type ImageUploadResult struct {
	ImageCover string
	Images     []string
}

func UploadTourImages() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if this is a multipart request
		contentType := c.GetHeader("Content-Type")
		if !strings.HasPrefix(contentType, "multipart/form-data") {
			c.Next() // Skip if not multipart
			return
		}

		// Parse multipart form (max 10MB)
		if err := c.Request.ParseMultipartForm(maxFileSize); err != nil {
			c.Next() // Continue even if no files
			return
		}

		// Check if there are actually any files
		if c.Request.MultipartForm == nil {
			c.Next()
			return
		}

		// Get tour ID (for update) or use temp ID (for create)
		tourID := c.Param("id")
		isCreateOperation := tourID == ""

		if isCreateOperation {
			tourID = uuid.New().String()
		}

		// Create upload directory if not exists
		if err := os.MkdirAll(uploadPath, 0755); err != nil {
			c.Next() // Continue even if directory creation fails
			return
		}

		result := &ImageUploadResult{
			Images: []string{},
		}

		// Process imageCover (single file)
		if imageCoverFile, header, err := c.Request.FormFile("imageCover"); err == nil {
			defer imageCoverFile.Close()

			if !isValidImageType(header.Filename) {
				c.Next()
				return
			}

			if header.Size > maxFileSize {
				c.Next()
				return
			}

			filename := generateFilename(tourID, "cover", "jpeg")
			if err := processAndSaveImage(imageCoverFile, filename, imageWidth, imageHeight); err == nil {
				result.ImageCover = filename
			}
		}

		// Process images (multiple files, max 3)
		if c.Request.MultipartForm != nil && c.Request.MultipartForm.File["images"] != nil {
			imageFiles := c.Request.MultipartForm.File["images"]

			if len(imageFiles) > 3 {
				c.Next()
				return
			}

			for i, fileHeader := range imageFiles {
				file, err := fileHeader.Open()
				if err != nil {
					continue
				}
				defer file.Close()

				if !isValidImageType(fileHeader.Filename) {
					continue
				}

				if fileHeader.Size > maxFileSize {
					continue
				}

				filename := generateFilename(tourID, fmt.Sprintf("image-%d", i+1), "jpeg")
				if err := processAndSaveImage(file, filename, imageWidth, imageHeight); err == nil {
					result.Images = append(result.Images, filename)
				}
			}
		}

		// Only store if we actually processed any images
		if result.ImageCover != "" || len(result.Images) > 0 {
			c.Set("uploadedImages", result)
		}

		c.Next()
	}
}

func processAndSaveImage(file multipart.File, filename string, width, height int) error {
	// Decode image
	img, err := imaging.Decode(file)
	if err != nil {
		return err
	}

	// Resize image
	resizedImg := imaging.Resize(img, width, height, imaging.Lanczos)

	// Create output path
	outPath := filepath.Join(uploadPath, filename)

	// Create output file
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Save as JPEG with quality
	options := &jpeg.Options{Quality: imageQuality}
	if err := jpeg.Encode(out, resizedImg, options); err != nil {
		return err
	}

	return nil
}

func generateFilename(tourID, prefix, ext string) string {
	timestamp := time.Now().Unix()
	uniqueID := uuid.New().String()[:8]
	return fmt.Sprintf("tour-%s-%s-%d-%s.%s", tourID, prefix, timestamp, uniqueID, ext)
}

func isValidImageType(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	validExts := map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true,
	}
	return validExts[ext]
}

// Helper to get uploaded images from context
func GetUploadedImages(c *gin.Context) *ImageUploadResult {
	if val, exists := c.Get("uploadedImages"); exists {
		return val.(*ImageUploadResult)
	}
	return &ImageUploadResult{}
}
