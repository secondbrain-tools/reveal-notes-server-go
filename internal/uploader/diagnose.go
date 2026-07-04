package uploader

import (
	"bytes"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Diagnosis summarizes the likely source of an upload HTTP failure and,
// when the failure originates from a front-end HTTP proxy rather than
// the notes server itself, provides a one-line remediation hint.
//
// Source is "ok" for 2xx responses, "backend" for errors that came from
// the notes server (which emits plain-text or JSON via http.Error/encoding/json),
// and "proxy" for errors that look like they came from a front-end HTTP
// proxy such as nginx, Cloudflare, Envoy, Apache httpd, OpenResty, IIS
// or Caddy.
type Diagnosis struct {
	Source string
	Hint   string
}

// DiagnoseUploadError inspects an upload HTTP response and classifies it.
// `body` is the response body (already read by the caller). `archiveBytes`
// is the size of the payload that was sent and is only used to enrich
// hints for 413-style failures.
func DiagnoseUploadError(resp *http.Response, body []byte, archiveBytes int) Diagnosis {
	if resp == nil || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return Diagnosis{Source: "ok"}
	}

	ct := resp.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(ct)
	trimmed := bytes.TrimSpace(body)

	// Detect "this is a proxy error page". A response qualifies when:
	//   1. Content-Type is text/html, OR
	//   2. The body opens with an HTML tag, OR
	//   3. The body contains any of the well-known proxy markers below
	//      (case-insensitive). The notes server never emits HTML.
	looksLikeProxy := false
	switch {
	case mediaType == "text/html":
		looksLikeProxy = true
	case bytes.HasPrefix(trimmed, []byte("<")):
		looksLikeProxy = true
	default:
		bodyLower := strings.ToLower(string(trimmed))
		for _, marker := range proxyMarkers {
			if strings.Contains(bodyLower, marker) {
				looksLikeProxy = true
				break
			}
		}
	}

	if !looksLikeProxy {
		return Diagnosis{Source: "backend"}
	}

	hint := "this looks like a front-end HTTP proxy error (nginx, Cloudflare, " +
		"Envoy, Apache httpd, OpenResty, IIS, Caddy, ...), not from the notes " +
		"server itself"
	switch resp.StatusCode {
	case http.StatusRequestEntityTooLarge:
		hint += "; the notes server caps uploads at 100 MiB, so a 413 means " +
			"the proxy is more restrictive"
		if archiveBytes > 0 {
			hint += formatArchiveSize(archiveBytes)
		}
		hint += " — raise the proxy's client_max_body_size (nginx) or equivalent " +
			"body-size limit, or route /presentations uploads to the notes " +
			"server directly"
	case http.StatusBadGateway:
		hint += "; the proxy could not reach the notes server upstream — " +
			"check upstream address, DNS, and TLS"
	case http.StatusGatewayTimeout:
		hint += "; the proxy gave up waiting for the notes server upstream — " +
			"check upstream timeouts and the notes server's logs"
	case http.StatusServiceUnavailable:
		hint += "; the proxy is short-circuiting with its own error page — " +
			"check upstream health checks and limits"
	default:
		hint += "; the proxy is intercepting what should be a backend response " +
			"— check the proxy's error_intercept / proxy_intercept settings"
	}
	return Diagnosis{Source: "proxy", Hint: hint}
}

// FormatUploadFailure renders a one-line upload-failure summary, augmented
// with the proxy hint when the diagnosis classifies the error as such.
// For proxy errors we prefer the <title>/<h1> from the HTML body over
// dumping the raw markup, which is otherwise unreadable.
func FormatUploadFailure(resp *http.Response, body []byte, archiveBytes int) string {
	diag := DiagnoseUploadError(resp, body, archiveBytes)

	trimmed := bytes.TrimSpace(body)
	switch {
	case diag.Source == "proxy":
		title := extractHTMLTitle(trimmed)
		if title != "" {
			return fmt.Sprintf("upload failed: %s (%s)\nNote: %s",
				resp.Status, title, diag.Hint)
		}
		return fmt.Sprintf("upload failed: %s\nNote: %s", resp.Status, diag.Hint)
	default:
		if len(trimmed) > 0 {
			return fmt.Sprintf("upload failed: %s: %s", resp.Status, trimmed)
		}
		return fmt.Sprintf("upload failed: %s", resp.Status)
	}
}

// proxyMarkers are substrings commonly found in default HTTP-proxy error
// pages. Matching is case-insensitive (the body is lowercased first).
var proxyMarkers = []string{
	"<center>nginx",
	"<center>openresty",
	"<center>envoy",
	"<center>caddy",
	"<address>",
	"cloudflare",
	"<hr><center>",
	"<title>413",
	"<title>502",
	"<title>503",
	"<title>504",
	"request entity too large",
}

// htmlTitleRE extracts the contents of the first <title>...</title>
// (or <h1>...</h1>) tag in the body, ignoring case and attributes.
var (
	htmlTitleRE = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	htmlH1RE    = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
)

func extractHTMLTitle(body []byte) string {
	if m := htmlTitleRE.FindSubmatch(body); len(m) >= 2 {
		return cleanStatusTitle(strings.TrimSpace(string(m[1])))
	}
	if m := htmlH1RE.FindSubmatch(body); len(m) >= 2 {
		return cleanStatusTitle(strings.TrimSpace(string(m[1])))
	}
	return ""
}

// cleanStatusTitle strips a leading "<status> " from titles like
// "413 Request Entity Too Large", leaving "Request Entity Too Large".
func cleanStatusTitle(t string) string {
	for i, r := range t {
		if r == ' ' || r == '\t' {
			if _, err := strconv.Atoi(t[:i]); err == nil {
				return strings.TrimSpace(t[i+1:])
			}
			break
		}
	}
	return t
}

func formatArchiveSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf(" (archive was %d bytes / ~%d MiB)", n, n>>20)
	case n > 0:
		return fmt.Sprintf(" (archive was %d bytes / ~%d KiB)", n, n>>10)
	default:
		return ""
	}
}
