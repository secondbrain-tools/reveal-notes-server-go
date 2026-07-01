package uploader

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"

	"remote-notes-server/internal/notes"
)

func BuildUploadRequest(ctx context.Context, serverURL, name string, archive []byte, bearerToken string) (*http.Request, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := notes.ValidatePresentationName(name); err != nil {
		return nil, err
	}

	baseURL, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("parse server url: %w", err)
	}
	baseURL.Path = path.Join("/", strings.Trim(baseURL.Path, "/"), "api", "presentations", name)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name+".zip")
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(archive); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL.String(), &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	return req, nil
}

// FetchRemoteHash makes a GET request to /api/presentations/{name}/hash and returns
// the stored archive hash. Returns empty string if the server returns 404 (no hash available).
func FetchRemoteHash(ctx context.Context, client *http.Client, serverURL, name string, bearerToken string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := notes.ValidatePresentationName(name); err != nil {
		return "", err
	}
	if client == nil {
		client = http.DefaultClient
	}

	baseURL, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("parse server url: %w", err)
	}
	baseURL.Path = path.Join("/", strings.Trim(baseURL.Path, "/"), "api", "presentations", name, "hash")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL.String(), nil)
	if err != nil {
		return "", err
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch remote hash: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// No hash available (presentation missing or pre-feature upload)
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch remote hash: %s", resp.Status)
	}

	var result struct {
		Hash string `json:"hash"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode hash response: %w", err)
	}
	return result.Hash, nil
}

// UploadPresentation sends a zip archive to the server.
func UploadPresentation(ctx context.Context, client *http.Client, serverURL, name string, archive []byte, bearerToken string) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := BuildUploadRequest(ctx, serverURL, name, archive, bearerToken)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}
