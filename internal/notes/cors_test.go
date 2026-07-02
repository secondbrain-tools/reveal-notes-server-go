package notes

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWrapSocketCORSPreflight(t *testing.T) {
	ts := newAuthTestServerWithToken(t, "secret")
	req := httptest.NewRequest(http.MethodOptions, "/socket.io/?EIO=4&transport=polling", nil)
	req.Header.Set("Origin", "http://example.com")
	resp := ts.Do(req)
	
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for OPTIONS preflight, got %d", resp.Code)
	}
	
	origin := resp.Header().Get("Access-Control-Allow-Origin")
	if origin != "http://example.com" {
		t.Fatalf("expected Access-Control-Allow-Origin: http://example.com, got %q", origin)
	}
	
	if resp.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("expected Access-Control-Allow-Credentials: true")
	}
}

func TestWrapSocketCORSSetsHeadersOnRejection(t *testing.T) {
	ts := newAuthTestServerWithToken(t, "secret")
	req := httptest.NewRequest(http.MethodGet, "/socket.io/?EIO=4&transport=polling&t=test", nil)
	req.Header.Set("Origin", "http://example.com")
	resp := ts.Do(req)
	
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", resp.Code)
	}
	
	origin := resp.Header().Get("Access-Control-Allow-Origin")
	if origin != "http://example.com" {
		t.Fatalf("expected Access-Control-Allow-Origin on error response, got %q", origin)
	}
}

func TestWrapSocketCORSDisabledWhenNoAccessToken(t *testing.T) {
	// When no token is configured, the global CORS wrapper still answers
	// the preflight before Socket.IO sees it.
	ts := newAuthTestServerWithToken(t, "")
	req := httptest.NewRequest(http.MethodOptions, "/socket.io/?EIO=4&transport=polling", nil)
	req.Header.Set("Origin", "http://example.com")
	resp := ts.Do(req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204 from global CORS handling, got %d", resp.Code)
	}
	if origin := resp.Header().Get("Access-Control-Allow-Origin"); origin != "http://example.com" {
		t.Fatalf("expected CORS header, got %q", origin)
	}
}

func TestHealthCORSAllowsAnyOrigin(t *testing.T) {
	ts := newAuthTestServerWithToken(t, "secret")

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Authorization", "Bearer secret")
	resp := ts.Do(req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 for cross-origin health request, got %d", resp.Code)
	}
	if origin := resp.Header().Get("Access-Control-Allow-Origin"); origin != "http://example.com" {
		t.Fatalf("expected Access-Control-Allow-Origin: http://example.com, got %q", origin)
	}
	if resp.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("expected Access-Control-Allow-Credentials: true")
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/health", nil)
	preflight.Header.Set("Origin", "http://example.com")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflightResp := ts.Do(preflight)

	if preflightResp.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for health preflight, got %d", preflightResp.Code)
	}
	if origin := preflightResp.Header().Get("Access-Control-Allow-Origin"); origin != "http://example.com" {
		t.Fatalf("expected preflight Access-Control-Allow-Origin: http://example.com, got %q", origin)
	}
}
