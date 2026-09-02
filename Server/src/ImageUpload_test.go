package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func createTestPNG(width, height int) string {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestProcessAndSaveImageSmall(t *testing.T) {
	b64 := createTestPNG(200, 100)
	processed, err := ProcessAndSaveImage(b64, "my_chart.png", "Architecture Diagram", "image/png")
	if err != nil {
		t.Fatalf("ProcessAndSaveImage failed: %v", err)
	}
	dateDir := time.Now().Format("20060102")
	defer os.Remove(filepath.Join(imageUploadBaseDir, dateDir, processed.Filename))

	if processed.Width != 200 || processed.Height != 100 {
		t.Fatalf("expected 200x100, got %dx%d", processed.Width, processed.Height)
	}
	if processed.Resized {
		t.Fatalf("expected not resized for 200x100")
	}
	expectedURL := "https://bbs.mars-cloud.com/images/" + dateDir + "/" + processed.Filename
	if processed.URL != expectedURL {
		t.Fatalf("expected URL %s, got %s", expectedURL, processed.URL)
	}
	expectedRel := "/images/" + dateDir + "/" + processed.Filename
	if processed.RelativeURL != expectedRel {
		t.Fatalf("expected RelativeURL %s, got %s", expectedRel, processed.RelativeURL)
	}
	if processed.Markdown != "![Architecture Diagram]("+processed.URL+")" {
		t.Fatalf("unexpected Markdown: %s", processed.Markdown)
	}
}

func TestProcessAndSaveImageLargeAutoResize(t *testing.T) {
	// Create an image with 3000x1500 (exceeds 2048px max dimension)
	b64 := createTestPNG(3000, 1500)
	processed, err := ProcessAndSaveImage("data:image/png;base64,"+b64, "huge.png", "", "")
	if err != nil {
		t.Fatalf("ProcessAndSaveImage failed: %v", err)
	}
	dateDir := time.Now().Format("20060102")
	defer os.Remove(filepath.Join(imageUploadBaseDir, dateDir, processed.Filename))

	if !processed.Resized {
		t.Fatalf("expected resized to be true for 3000x1500")
	}
	if processed.Width != 2048 || processed.Height != 1024 {
		t.Fatalf("expected 2048x1024 after resize, got %dx%d", processed.Width, processed.Height)
	}
	if processed.AltText != "huge" {
		t.Fatalf("expected AltText 'huge', got %s", processed.AltText)
	}
}

func TestMCPUloadImageTool(t *testing.T) {
	store := NewStore(t.TempDir(), 20, false)
	if _, err := store.UpsertAuthToken(AuthTokenRecord{
		Token: "test-image-token", ClientID: "agent-tester", Kind: "dev-short",
		SourceIP: "127.0.0.1", IssuedAt: time.Now().Format(time.RFC3339Nano),
		ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano),
	}, false); err != nil {
		t.Fatal(err)
	}

	facade := &SmallTalkFacade{Store: store}
	handler := NewMCPHTTPHandler(facade)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "http-test-client", Version: "1.0"}, nil)
	httpClient := &http.Client{
		Transport: bearerRoundTripper{base: http.DefaultTransport, token: "test-image-token"},
	}
	transport := &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer session.Close()

	b64 := createTestPNG(50, 50)
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "smalltalk_upload_image",
		Arguments: map[string]any{
			"data":     b64,
			"filename": "icon.png",
			"alt_text": "App Icon",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error response from upload_image: %v", res)
	}
}
