package utils

import (
	"fmt"
	"os"
	"path/filepath"
)

const UploadPath = "public/img/tours"

// DeleteImages removes multiple image files from the file system
func DeleteImages(filenames ...string) {
	for _, filename := range filenames {
		if filename == "" {
			continue
		}
		filePath := filepath.Join(UploadPath, filename)
		if err := os.Remove(filePath); err != nil {
			// Only log if file exists (ignore "file not found" errors)
			if !os.IsNotExist(err) {
				fmt.Printf("Warning: Could not delete image %s: %v\n", filePath, err)
			}
		}
	}
}

// DeleteImageCover deletes the cover image
func DeleteImageCover(imageCover string) {
	DeleteImages(imageCover)
}

// DeleteImageGallery deletes multiple images from a slice
func DeleteImageGallery(images []string) {
	DeleteImages(images...)
}
