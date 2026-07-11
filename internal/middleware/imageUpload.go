package middleware

import (
	"errors"
	"fmt"
	"image/jpeg"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/paularinzee/natour/pkg/utils"
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

// UploadTourImages processes and saves incoming multipart/form-data image payloads.
func UploadTourImages() gin.HandlerFunc {
	return func(c *gin.Context) {
		contentType := c.GetHeader("Content-Type")
		if !strings.HasPrefix(contentType, "multipart/form-data") {
			c.Next()
			return
		}

		// Enforce request body sizing limits early at the framework level
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxFileSize)

		if err := c.Request.ParseMultipartForm(maxFileSize); err != nil {
			slog.Warn("Image upload rejected: form payload structural error or size limit exceeded", "error", err)
			c.Error(utils.NewBadRequestError("Invalid multipart payload or file size exceeds maximum constraint"))
			c.Abort()
			return
		}

		if c.Request.MultipartForm == nil {
			c.Next()
			return
		}

		tourID := c.Param("id")
		if tourID == "" {
			tourID = uuid.New().String()
		}

		if err := os.MkdirAll(uploadPath, 0755); err != nil {
			slog.Error("Infrastructure error: unable to provision upload targets directory", "error", err)
			// Pass the actual 'err' object into your custom utility instead of a raw string
			c.Error(utils.NewInternalServerError(err))
			c.Abort()
			return
		}

		result := &ImageUploadResult{
			Images: make([]string, 0, 3),
		}

		// 1. Used '_' to discard the unused header assignment
		if imageCoverFile, _, err := c.Request.FormFile("imageCover"); err == nil {
			defer imageCoverFile.Close()

			if !isValidFileMime(imageCoverFile) {
				c.Error(utils.NewBadRequestError("Invalid image cover format. Allowed: jpeg, png, webp, gif"))
				c.Abort()
				return
			}

			filename := generateFilename(tourID, "cover", "jpeg")
			if err := processAndSaveImage(imageCoverFile, filename, imageWidth, imageHeight); err != nil {
				slog.Error("Image processing failed for cover image", "error", err)
				// 2. Pass the underlying 'err' directly into your internal server error utility
				c.Error(utils.NewInternalServerError(err))
				c.Abort()
				return
			}

			result.ImageCover = filename
		}

		// 2. Process side images (multiple files, maximum 3)
		if imageFiles := c.Request.MultipartForm.File["images"]; len(imageFiles) > 0 {
			if len(imageFiles) > 3 {
				c.Error(utils.NewBadRequestError("Maximum of 3 gallery images allowed"))
				c.Abort()
				return
			}

			// Define scoped block closure function to avoid fd leak tracking anomalies
			processIteration := func(fileHeader *multipart.FileHeader, index int) error {
				file, err := fileHeader.Open()
				if err != nil {
					return err
				}
				defer file.Close()

				if !isValidFileMime(file) {
					return errors.New("unsupported image format file type detected")
				}

				filename := generateFilename(tourID, fmt.Sprintf("image-%d", index+1), "jpeg")
				if err := processAndSaveImage(file, filename, imageWidth, imageHeight); err != nil {
					return err
				}

				result.Images = append(result.Images, filename)
				return nil
			}

			for i, fileHeader := range imageFiles {
				if err := processIteration(fileHeader, i); err != nil {
					slog.Error("Gallery image processing iteration failed", "error", err, "index", i)
					c.Error(utils.NewBadRequestError(fmt.Sprintf("Failed processing image at position %d: %v", i+1, err)))
					c.Abort()
					return
				}
			}
		}

		if result.ImageCover != "" || len(result.Images) > 0 {
			c.Set("uploadedImages", result)
		}

		c.Next()
	}
}

func processAndSaveImage(file multipart.File, filename string, width, height int) error {
	// Reset file offset marker back to original stream index position
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}

	img, err := imaging.Decode(file)
	if err != nil {
		return err
	}

	resizedImg := imaging.Resize(img, width, height, imaging.Lanczos)
	outPath := filepath.Join(uploadPath, filename)

	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	options := &jpeg.Options{Quality: imageQuality}
	return jpeg.Encode(out, resizedImg, options)
}

func generateFilename(tourID, prefix, ext string) string {
	timestamp := time.Now().Unix()
	uniqueID := uuid.New().String()[:8]
	return fmt.Sprintf("tour-%s-%s-%d-%s.%s", tourID, prefix, timestamp, uniqueID, ext)
}

func isValidFileMime(file multipart.File) bool {
	buffer := make([]byte, 512)
	if _, err := file.Seek(0, 0); err != nil {
		return false
	}
	if _, err := file.Read(buffer); err != nil {
		return false
	}
	// Reset pointer for downstream readers
	if _, err := file.Seek(0, 0); err != nil {
		return false
	}

	contentType := http.DetectContentType(buffer)
	validMimes := map[string]bool{
		"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true,
	}
	return validMimes[contentType]
}

func GetUploadedImages(c *gin.Context) *ImageUploadResult {
	if val, exists := c.Get("uploadedImages"); exists {
		if result, ok := val.(*ImageUploadResult); ok {
			return result
		}
	}
	return &ImageUploadResult{}
}
