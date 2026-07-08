package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"strings"
	"time"
)

var errFileStorageNotConfigured = errors.New("file storage is not configured")

type fileUploadResult struct {
	ContentType string
	FileID      string
	Filename    string
	Provider    string
	Size        int64
	URL         string
}

type seaweedAssignResponse struct {
	FileID    string `json:"fid"`
	PublicURL string `json:"publicUrl"`
	URL       string `json:"url"`
}

func (s *server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		s.handleAdminResourceCreate(w, r, resourceUploads, s.adminResources().CreateUpload)
		return
	}

	result, err := s.uploadMultipartFile(w, r)
	if err != nil {
		if errors.Is(err, errFileStorageNotConfigured) {
			s.fail(w, r, http.StatusServiceUnavailable, "FILE_STORAGE_NOT_CONFIGURED", "파일 저장소 설정이 필요합니다.", map[string]any{"required": []string{"SEAWEEDFS_MASTER_URL"}})
			return
		}
		s.fail(w, r, http.StatusBadRequest, "FILE_UPLOAD_FAILED", "파일을 업로드하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}

	body := map[string]any{
		"contentType": result.ContentType,
		"fileId":      result.FileID,
		"filename":    result.Filename,
		"provider":    result.Provider,
		"size":        result.Size,
		"url":         result.URL,
	}
	if purpose := strings.TrimSpace(r.FormValue("purpose")); purpose != "" {
		body["purpose"] = purpose
	}
	if boothID := strings.TrimSpace(r.FormValue("boothId")); boothID != "" {
		body["boothId"] = boothID
	}
	if productID := strings.TrimSpace(r.FormValue("productId")); productID != "" {
		body["productId"] = productID
	}

	item, err := s.adminResources().CreateUpload(r.Context(), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceUploads, "", "create", err)
		return
	}
	s.created(w, r, item)
}

func (s *server) uploadMultipartFile(w http.ResponseWriter, r *http.Request) (fileUploadResult, error) {
	maxBytes := envInt("FILE_UPLOAD_MAX_BYTES", 10<<20)
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxBytes))
	if err := r.ParseMultipartForm(int64(maxBytes)); err != nil {
		return fileUploadResult{}, err
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		return fileUploadResult{}, err
	}
	defer file.Close()

	return uploadFileToSeaweedFS(r.Context(), file, header)
}

func uploadFileToSeaweedFS(ctx context.Context, file multipart.File, header *multipart.FileHeader) (fileUploadResult, error) {
	masterURL := strings.TrimRight(env("SEAWEEDFS_MASTER_URL", ""), "/")
	if masterURL == "" {
		return fileUploadResult{}, errFileStorageNotConfigured
	}

	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	client := &http.Client{Timeout: envDuration("SEAWEEDFS_HTTP_TIMEOUT", 15*time.Second)}
	assign, err := assignSeaweedFile(ctx, client, masterURL)
	if err != nil {
		return fileUploadResult{}, err
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", path.Base(header.Filename))
	if err != nil {
		return fileUploadResult{}, err
	}
	size, err := io.Copy(part, file)
	if err != nil {
		return fileUploadResult{}, err
	}
	if err := writer.Close(); err != nil {
		return fileUploadResult{}, err
	}

	uploadURL := seaweedVolumeURL(firstNonEmpty(assign.PublicURL, assign.URL), assign.FileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &body)
	if err != nil {
		return fileUploadResult{}, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return fileUploadResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fileUploadResult{}, fmt.Errorf("seaweedfs upload status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	return fileUploadResult{
		ContentType: contentType,
		FileID:      assign.FileID,
		Filename:    path.Base(header.Filename),
		Provider:    "seaweedfs",
		Size:        size,
		URL:         seaweedPublicURL(assign),
	}, nil
}

func assignSeaweedFile(ctx context.Context, client *http.Client, masterURL string) (seaweedAssignResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, masterURL+"/dir/assign", nil)
	if err != nil {
		return seaweedAssignResponse{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return seaweedAssignResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return seaweedAssignResponse{}, fmt.Errorf("seaweedfs assign status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var assign seaweedAssignResponse
	if err := json.NewDecoder(resp.Body).Decode(&assign); err != nil {
		return seaweedAssignResponse{}, err
	}
	if assign.FileID == "" || assign.URL == "" {
		return seaweedAssignResponse{}, errors.New("seaweedfs assign response is missing fid or url")
	}
	return assign, nil
}

func seaweedVolumeURL(volumeURL, fileID string) string {
	if strings.HasPrefix(volumeURL, "http://") || strings.HasPrefix(volumeURL, "https://") {
		return strings.TrimRight(volumeURL, "/") + "/" + fileID
	}
	scheme := env("SEAWEEDFS_VOLUME_SCHEME", "http")
	return scheme + "://" + strings.TrimRight(volumeURL, "/") + "/" + fileID
}

func seaweedPublicURL(assign seaweedAssignResponse) string {
	if base := strings.TrimRight(env("SEAWEEDFS_PUBLIC_BASE_URL", ""), "/"); base != "" {
		return base + "/" + assign.FileID
	}
	publicURL := firstNonEmpty(assign.PublicURL, assign.URL)
	if strings.HasPrefix(publicURL, "http://") || strings.HasPrefix(publicURL, "https://") {
		return strings.TrimRight(publicURL, "/") + "/" + assign.FileID
	}
	scheme := env("SEAWEEDFS_VOLUME_SCHEME", "http")
	return scheme + "://" + strings.TrimRight(publicURL, "/") + "/" + assign.FileID
}
