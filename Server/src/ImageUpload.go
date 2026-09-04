package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	maxImageDimension  = 2048
	maxImageFileSize   = 20 * 1024 * 1024 // 20 MB
	imageUploadBaseDir = "./website/images"
	imageUploadBaseURL = "https://bbs.mars-cloud.com/images/"
)

type ProcessedImage struct {
	Bytes            []byte
	Filename         string
	URL              string
	RelativeURL      string
	Markdown         string
	MIMEType         string
	Width            int
	Height           int
	Resized          bool
	OriginalFilename string
	AltText          string
}

func ProcessAndSaveImage(dataStr, originalFilename, altText, requestedMIME string) (*ProcessedImage, error) {
	raw := strings.TrimSpace(dataStr)
	if raw == "" {
		return nil, fmt.Errorf("image data is required (base64 encoded)")
	}

	var detectedMime string
	if strings.HasPrefix(raw, "data:") {
		commaIdx := strings.Index(raw, ",")
		if commaIdx > 0 {
			meta := raw[:commaIdx]
			raw = raw[commaIdx+1:]
			if semiIdx := strings.Index(meta, ";"); semiIdx > 5 {
				detectedMime = strings.TrimPrefix(meta[:semiIdx], "data:")
			}
		}
	}

	raw = strings.ReplaceAll(raw, "\r", "")
	raw = strings.ReplaceAll(raw, "\n", "")
	raw = strings.ReplaceAll(raw, " ", "")

	rawBytes, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		rawBytes, err = base64.RawStdEncoding.DecodeString(raw)
		if err != nil {
			rawBytes, err = base64.URLEncoding.DecodeString(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid base64 image data: %w", err)
			}
		}
	}

	if len(rawBytes) == 0 {
		return nil, fmt.Errorf("image data is empty")
	}
	if len(rawBytes) > maxImageFileSize {
		return nil, fmt.Errorf("image size (%d bytes) exceeds maximum limit of 20MB", len(rawBytes))
	}

	mimeType := strings.ToLower(strings.TrimSpace(requestedMIME))
	if mimeType == "" {
		mimeType = strings.ToLower(strings.TrimSpace(detectedMime))
	}

	ext := ".png"
	isSVG := false

	// Magic byte inspection
	if len(rawBytes) >= 8 && string(rawBytes[:8]) == "\x89PNG\r\n\x1a\n" {
		mimeType = "image/png"
		ext = ".png"
	} else if len(rawBytes) >= 3 && rawBytes[0] == 0xff && rawBytes[1] == 0xd8 && rawBytes[2] == 0xff {
		mimeType = "image/jpeg"
		ext = ".jpg"
	} else if len(rawBytes) >= 6 && (string(rawBytes[:6]) == "GIF87a" || string(rawBytes[:6]) == "GIF89a") {
		mimeType = "image/gif"
		ext = ".gif"
	} else if len(rawBytes) >= 12 && string(rawBytes[:4]) == "RIFF" && string(rawBytes[8:12]) == "WEBP" {
		mimeType = "image/webp"
		ext = ".webp"
	} else if len(rawBytes) >= 2 && string(rawBytes[:2]) == "BM" {
		mimeType = "image/bmp"
		ext = ".bmp"
	} else if strings.Contains(strings.ToLower(string(rawBytes[:min(len(rawBytes), 512)])), "<svg") {
		mimeType = "image/svg+xml"
		ext = ".svg"
		isSVG = true
	} else if mimeType != "" {
		switch mimeType {
		case "image/jpeg", "image/jpg":
			ext = ".jpg"
		case "image/png":
			ext = ".png"
		case "image/gif":
			ext = ".gif"
		case "image/webp":
			ext = ".webp"
		case "image/svg+xml", "image/svg":
			ext = ".svg"
			isSVG = true
		case "image/bmp":
			ext = ".bmp"
		default:
			return nil, fmt.Errorf("unsupported image MIME type: %s", mimeType)
		}
	} else {
		return nil, fmt.Errorf("unsupported or unrecognized image format")
	}

	if isSVG {
		svgLower := strings.ToLower(string(rawBytes))
		if strings.Contains(svgLower, "<script") ||
			strings.Contains(svgLower, "javascript:") ||
			strings.Contains(svgLower, "onload=") ||
			strings.Contains(svgLower, "onerror=") ||
			strings.Contains(svgLower, "onclick=") ||
			strings.Contains(svgLower, "onmouseover=") ||
			strings.Contains(svgLower, "xlink:href=\"javascript") ||
			strings.Contains(svgLower, "href=\"javascript") {
			return nil, fmt.Errorf("SVG contains prohibited executable scripts or active event handlers")
		}
	}

	width := 0
	height := 0
	resized := false

	// If not SVG, decode and check dimensions
	if !isSVG {
		img, format, decodeErr := image.Decode(bytes.NewReader(rawBytes))
		if decodeErr == nil && img != nil {
			bounds := img.Bounds()
			width = bounds.Dx()
			height = bounds.Dy()

			// Check max dimension 2048px
			if width > maxImageDimension || height > maxImageDimension {
				var newWidth, newHeight int
				if width >= height {
					newWidth = maxImageDimension
					newHeight = int(float64(height) * float64(maxImageDimension) / float64(width))
				} else {
					newHeight = maxImageDimension
					newWidth = int(float64(width) * float64(maxImageDimension) / float64(height))
				}
				if newWidth < 1 {
					newWidth = 1
				}
				if newHeight < 1 {
					newHeight = 1
				}

				dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
				xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, xdraw.Over, nil)

				var buf bytes.Buffer
				var encodeErr error
				switch format {
				case "jpeg":
					encodeErr = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 90})
					mimeType = "image/jpeg"
					ext = ".jpg"
				case "gif":
					encodeErr = gif.Encode(&buf, dst, nil)
					mimeType = "image/gif"
					ext = ".gif"
				default:
					// Default to PNG encoding for lossless resized image
					encodeErr = png.Encode(&buf, dst)
					mimeType = "image/png"
					ext = ".png"
				}

				if encodeErr == nil {
					rawBytes = buf.Bytes()
					width = newWidth
					height = newHeight
					resized = true
				}
			}
		}
	}

	now := time.Now()
	dateDir := now.Format("20060102")
	targetDir := filepath.Join(imageUploadBaseDir, dateDir)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create upload directory: %w", err)
	}

	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes)
	safeFilename := fmt.Sprintf("img_%s_%x%s", now.Format("150405"), randBytes, ext)
	filePath := filepath.Join(targetDir, safeFilename)

	if err := os.WriteFile(filePath, rawBytes, 0644); err != nil {
		return nil, fmt.Errorf("failed to save image to disk: %w", err)
	}

	finalAlt := strings.TrimSpace(altText)
	if finalAlt == "" {
		if originalFilename != "" {
			finalAlt = strings.TrimSuffix(filepath.Base(originalFilename), filepath.Ext(originalFilename))
		} else {
			finalAlt = "image"
		}
	}

	relativeURL := fmt.Sprintf("/images/%s/%s", dateDir, safeFilename)
	publicURL := fmt.Sprintf("%s%s/%s", imageUploadBaseURL, dateDir, safeFilename)
	markdown := fmt.Sprintf("![%s](%s)", finalAlt, publicURL)

	return &ProcessedImage{
		Bytes:            rawBytes,
		Filename:         safeFilename,
		URL:              publicURL,
		RelativeURL:      relativeURL,
		Markdown:         markdown,
		MIMEType:         mimeType,
		Width:            width,
		Height:           height,
		Resized:          resized,
		OriginalFilename: originalFilename,
		AltText:          finalAlt,
	}, nil
}
