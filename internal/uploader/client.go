package uploader

import (
	"bytes"
	"context"
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
