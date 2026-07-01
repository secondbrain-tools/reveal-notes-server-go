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
	// When no token is configured, wrapSocket is a no-op and passes
	// through to engine.io, which handles CORS itself.
	ts := newAuthTestServerWithToken(t, "")
	req := httptest.NewRequest(http.MethodOptions, "/socket.io/?EIO=4&transport=polling", nil)
	req.Header.Set("Origin", "http://example.com")
	resp := ts.Do(req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204 from engine.io CORS handling, got %d", resp.Code)
	}
	if origin := resp.Header().Get("Access-Control-Allow-Origin"); origin != "http://example.com" {
		t.Fatalf("expected engine.io CORS header, got %q", origin)
	}
}
