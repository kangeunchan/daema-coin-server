package server

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUploadFileToSeaweedFS(t *testing.T) {
	uploaded := false
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dir/assign":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"fid":"3,abc123","url":"` + r.Host + `"}`))
		case "/3,abc123":
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				t.Fatalf("upload content type = %q, want multipart/form-data", r.Header.Get("Content-Type"))
			}
			uploaded = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"menu.png","size":9}`))
		default:
			t.Fatalf("unexpected SeaweedFS request path %s", r.URL.Path)
		}
	}))
	defer storage.Close()

	t.Setenv("SEAWEEDFS_MASTER_URL", storage.URL)
	t.Setenv("SEAWEEDFS_PUBLIC_BASE_URL", "https://cdn.example.test")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "menu.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("image-bytes")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	reader := multipart.NewReader(&body, writer.Boundary())
	form, err := reader.ReadForm(1024)
	if err != nil {
		t.Fatal(err)
	}
	fileHeader := form.File["file"][0]
	file, err := fileHeader.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	result, err := uploadFileToSeaweedFS(context.Background(), file, fileHeader)
	if err != nil {
		t.Fatal(err)
	}
	if !uploaded {
		t.Fatal("file was not uploaded to SeaweedFS volume server")
	}
	if result.FileID != "3,abc123" {
		t.Fatalf("FileID = %q, want 3,abc123", result.FileID)
	}
	if result.URL != "https://cdn.example.test/3,abc123" {
		t.Fatalf("URL = %q, want public URL", result.URL)
	}
	if result.Provider != "seaweedfs" {
		t.Fatalf("Provider = %q, want seaweedfs", result.Provider)
	}
}

func TestSeaweedPublicURLDefaultsToServerProxy(t *testing.T) {
	t.Setenv("SEAWEEDFS_PUBLIC_BASE_URL", "")

	got := seaweedPublicURL(seaweedAssignResponse{FileID: "3,abc123", URL: "volume:8080"})
	if got != "/api/files/3,abc123" {
		t.Fatalf("seaweedPublicURL() = %q, want server proxy URL", got)
	}
}

func TestHandleFileDownloadStreamsFromSeaweedFS(t *testing.T) {
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3,abc123" {
			t.Fatalf("unexpected SeaweedFS request path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"file-etag"`)
		_, _ = w.Write([]byte("image-bytes"))
	}))
	defer storage.Close()

	t.Setenv("SEAWEEDFS_VOLUME_BASE_URL", storage.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/files/3,abc123", nil)
	rec := httptest.NewRecorder()

	(&server{}).routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("ETag") != `"file-etag"` {
		t.Fatalf("ETag = %q, want upstream ETag", rec.Header().Get("ETag"))
	}
	if rec.Body.String() != "image-bytes" {
		t.Fatalf("body = %q, want image-bytes", rec.Body.String())
	}
}

func TestHandleFileDownloadRequiresVolumeBaseURL(t *testing.T) {
	t.Setenv("SEAWEEDFS_VOLUME_BASE_URL", "")

	req := httptest.NewRequest(http.MethodGet, "/api/files/3,abc123", nil)
	rec := httptest.NewRecorder()

	(&server{}).routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "SEAWEEDFS_VOLUME_BASE_URL") {
		t.Fatalf("response body does not mention required env: %s", rec.Body.String())
	}
}
