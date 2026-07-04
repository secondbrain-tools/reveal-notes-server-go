package uploader

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

// nginx413Body is the verbatim nginx-default 413 page from the original
// upload failure that motivated this diagnostic helper. Keeping it exact
// matters: this is the regression case.
const nginx413Body = `<html>
<head><title>413 Request Entity Too Large</title></head>
<body>
<center><h1>413 Request Entity Too Large</h1></center>
<hr><center>nginx</center>
</body>
</html>`

const cloudflare504Body = `<!DOCTYPE html>
<!--[if IE 8]><html lang="en-US" class="ie8"> <![endif]-->
<html lang="en-US">
<head><title>504 Gateway Timeout</title></head>
<body>
<center><h1>504 Gateway Timeout</h1></center>
<hr><center>cloudflare</center>
</body>
</html>`

func TestDiagnoseUploadError(t *testing.T) {
	tests := []struct {
		name             string
		status           int
		contentType      string
		body             []byte
		archive          int
		wantSource       string
		wantHintContains string // empty means Hint must be empty
	}{
		{
			name:             "nginx 413 with content-type (matches original failure)",
			status:           http.StatusRequestEntityTooLarge,
			contentType:      "text/html; charset=utf-8",
			body:             []byte(nginx413Body),
			archive:          30199733,
			wantSource:       "proxy",
			wantHintContains: "client_max_body_size",
		},
		{
			name:             "nginx 413 with no content-type, sniffed from body",
			status:           http.StatusRequestEntityTooLarge,
			contentType:      "",
			body:             []byte(nginx413Body),
			archive:          30199733,
			wantSource:       "proxy",
			wantHintContains: "client_max_body_size",
		},
		{
			name:             "Cloudflare 504",
			status:           http.StatusGatewayTimeout,
			contentType:      "text/html",
			body:             []byte(cloudflare504Body),
			archive:          0,
			wantSource:       "proxy",
			wantHintContains: "upstream",
		},
		{
			name:        "Go http.Error plain text 400",
			status:      http.StatusBadRequest,
			contentType: "text/plain; charset=utf-8",
			body:        []byte("Upload must be a zip file\n"),
			archive:     1234,
			wantSource:  "backend",
		},
		{
			name:        "Go JSON 4xx error",
			status:      http.StatusUnprocessableEntity,
			contentType: "application/json",
			body:        []byte(`{"error":"invalid name"}`),
			archive:     1234,
			wantSource:  "backend",
		},
		{
			name:        "Unauthorized plain text",
			status:      http.StatusUnauthorized,
			contentType: "text/plain; charset=utf-8",
			body:        []byte("Unauthorized\n"),
			archive:     0,
			wantSource:  "backend",
		},
		{
			name:        "Empty body 5xx (still backend, no HTML markers)",
			status:      http.StatusInternalServerError,
			contentType: "",
			body:        nil,
			archive:     100,
			wantSource:  "backend",
		},
		{
			name:        "2xx is ok",
			status:      http.StatusCreated,
			contentType: "application/json",
			body:        []byte(`{"name":"x"}`),
			wantSource:  "ok",
		},
		{
			name:             "IIS-style 413 with <address> marker",
			status:           http.StatusRequestEntityTooLarge,
			contentType:      "text/html",
			body:             []byte(`<html><body><h2>413 Request Entity Too Large</h2><hr><address>Microsoft-IIS/10.0</address></body></html>`),
			archive:          1234,
			wantSource:       "proxy",
			wantHintContains: "client_max_body_size",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hdr := http.Header{}
			if tc.contentType != "" {
				hdr.Set("Content-Type", tc.contentType)
			}
			resp := &http.Response{
				StatusCode: tc.status,
				Header:     hdr,
			Status:     "413 Request Entity Too Large",
				Body:       io.NopCloser(bytes.NewReader(tc.body)),
			}
			diag := DiagnoseUploadError(resp, tc.body, tc.archive)

			if diag.Source != tc.wantSource {
				t.Errorf("Source = %q, want %q", diag.Source, tc.wantSource)
			}
			if tc.wantSource != "proxy" && diag.Hint != "" {
				t.Errorf("Hint = %q, want empty for source=%q", diag.Hint, tc.wantSource)
			}
			if tc.wantHintContains != "" && !strings.Contains(diag.Hint, tc.wantHintContains) {
				t.Errorf("Hint %q does not contain %q", diag.Hint, tc.wantHintContains)
			}
		})
	}
}

func TestFormatUploadFailureRendersProxyHint(t *testing.T) {
	hdr := http.Header{}
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	resp := &http.Response{
		StatusCode: http.StatusRequestEntityTooLarge,
		Header:     hdr,
			Status:     "413 Request Entity Too Large",
		Body:       io.NopCloser(bytes.NewReader([]byte(nginx413Body))),
	}
	out := FormatUploadFailure(resp, []byte(nginx413Body), 30199733)

	// The original raw HTML must NOT be dumped — that was the noisy failure
	// mode. The status text should appear, parsed from <title>, and the
	// note line should be appended.
	if strings.Contains(out, "<html>") || strings.Contains(out, "<center>") {
		t.Errorf("FormatUploadFailure leaked raw HTML body: %q", out)
	}
	if !strings.Contains(out, "413 Request Entity Too Large") {
		t.Errorf("FormatUploadFailure missing status line: %q", out)
	}
	if !strings.Contains(out, "Note:") || !strings.Contains(out, "client_max_body_size") {
		t.Errorf("FormatUploadFailure missing hint: %q", out)
	}
}

func TestFormatUploadFailureKeepsBackendBodyVerbatim(t *testing.T) {
	hdr := http.Header{}
	hdr.Set("Content-Type", "text/plain; charset=utf-8")
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     hdr,
			Status:     "413 Request Entity Too Large",
		Body:       io.NopCloser(bytes.NewReader([]byte("Upload must be a zip file\n"))),
	}
	out := FormatUploadFailure(resp, []byte("Upload must be a zip file\n"), 1234)

	if !strings.Contains(out, "Upload must be a zip file") {
		t.Errorf("expected backend error body verbatim, got: %q", out)
	}
	if strings.Contains(out, "Note:") {
		t.Errorf("backend error should not carry a hint, got: %q", out)
	}
}

func TestFormatArchiveSize(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, ""},
		{512, " (archive was 512 bytes / ~0 KiB)"},
		{2048, " (archive was 2048 bytes / ~2 KiB)"},
		{(1 << 20) - 1, " (archive was 1048575 bytes / ~1023 KiB)"},
		{1 << 20, " (archive was 1048576 bytes / ~1 MiB)"},
		{30199733, " (archive was 30199733 bytes / ~28 MiB)"},
	}
	for _, tc := range tests {
		got := formatArchiveSize(tc.in)
		if got != tc.want {
			t.Errorf("formatArchiveSize(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
