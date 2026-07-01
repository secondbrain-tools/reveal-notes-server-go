package notes

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pollResponse is the decoded engine.io polling payload. Engine.IO v4
// separates multiple packets with the Record Separator byte (0x1E), so we
// split on that and only inspect the first message — enough to decide
// whether the server accepted or rejected the socket.io CONNECT.
type pollResponse struct {
	raw     string
	packets []string
}

func (p *pollResponse) first() string {
	if len(p.packets) == 0 {
		return ""
	}
	return p.packets[0]
}

// engineIOSeparator is the Record Separator byte (0x1E) that engine.io v4
// uses to delimit multiple packets in a single polling response.
const engineIOSeparator = "\x1e"

// parsePollingPayload splits an engine.io v4 polling response into its
// individual packets. A response may contain a single packet (most common)
// or multiple packets separated by the record-separator byte.
func parsePollingPayload(body string) (*pollResponse, error) {
	resp := &pollResponse{raw: body}
	if body == "" {
		return nil, fmt.Errorf("empty polling payload")
	}
	parts := strings.Split(body, engineIOSeparator)
	// strings.Split with a single-element separator leaves an empty trailing
	// element when body ends with the separator; trim it.
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty polling payload")
	}
	resp.packets = parts
	return resp, nil
}

// pollHandshake performs a full engine.io + socket.io polling handshake
// against the test server. It returns the engine.io session id and the
// first packet from the post-CONNECT poll (the CONNECT ACK on success or
// the CONNECT_ERROR on rejection).
func pollHandshake(t *testing.T, ts *testServer, query string, connectBody string) (sid string, firstPacket string, status int) {
	t.Helper()

	openURL := "/socket.io/?EIO=4&transport=polling" + query
	req := httptest.NewRequest(http.MethodGet, openURL, nil)
	resp := ts.Do(req)
	if resp.Code != http.StatusOK {
		return "", "", resp.Code
	}
	body, _ := io.ReadAll(resp.Body)
	pr, err := parsePollingPayload(string(body))
	if err != nil {
		t.Fatalf("open: %v (raw body: %q)", err, string(body))
	}
	if got := pr.first(); !strings.HasPrefix(got, "0{") {
		t.Fatalf("expected engine.io open packet, got %q", got)
	}
	var open struct {
		SID string `json:"sid"`
	}
	if err := json.Unmarshal([]byte(pr.first()[1:]), &open); err != nil {
		t.Fatalf("decode open: %v", err)
	}
	if open.SID == "" {
		t.Fatal("engine.io open response missing sid")
	}

	postURL := "/socket.io/?EIO=4&transport=polling&sid=" + open.SID + query
	postReq := httptest.NewRequest(http.MethodPost, postURL, strings.NewReader(connectBody))
	postReq.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	postResp := ts.Do(postReq)
	if postResp.Code != http.StatusOK {
		return open.SID, "", postResp.Code
	}

	getURL := postURL
	getReq := httptest.NewRequest(http.MethodGet, getURL, nil)
	getResp := ts.Do(getReq)
	if getResp.Code != http.StatusOK {
		return open.SID, "", getResp.Code
	}
	getBody, _ := io.ReadAll(getResp.Body)
	pr2, err := parsePollingPayload(string(getBody))
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	return open.SID, pr2.first(), http.StatusOK
}

// connectPacket is the JSON body the socket.io client sends for a CONNECT
// packet on the default namespace. Socket.IO wraps the client's `auth`
// option under an "auth" key, so the data is `{"auth": {"token": "..."}}`.
// The whole payload is wrapped as engine.io type 4 (message) and socket.io
// type 0 (CONNECT) — the "40" prefix.
func connectPacket(authToken string) string {
	payload, _ := json.Marshal(map[string]any{"auth": map[string]any{"token": authToken}})
	// "4" = engine.io MESSAGE, "" = default namespace, "0" = socket.io CONNECT.
	return "40" + string(payload)
}

func TestSocketIOConnectionAcceptsAuthPayload(t *testing.T) {
	ts := newAuthTestServerWithToken(t, "secret")

	_, first, code := pollHandshake(t, ts, "&token=secret", connectPacket("secret"))
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	// CONNECT ACK is a socket.io MESSAGE (type 4) wrapping a CONNECT packet
	// (type 0), so the response starts with "40{".
	if !strings.HasPrefix(first, "40{") {
		t.Fatalf("expected CONNECT ACK (40{...}), got %q", first)
	}
	if strings.Contains(first, "unauthorized") {
		t.Fatalf("unexpected unauthorized in CONNECT ACK: %q", first)
	}
}

func TestSocketIOConnectionAcceptsQueryStringToken(t *testing.T) {
	ts := newAuthTestServerWithToken(t, "secret")

	// No auth payload in the CONNECT, but the token is in the query string
	// (used for the HTTP-level edge). The handshake middleware should still
	// accept the connection.
	_, first, code := pollHandshake(t, ts, "&token=secret", connectPacket(""))
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.HasPrefix(first, "40{") {
		t.Fatalf("expected CONNECT ACK (40{...}), got %q", first)
	}
}

func TestSocketIOConnectionRejectsMissingToken(t *testing.T) {
	ts := newAuthTestServerWithToken(t, "secret")

	// No token anywhere: HTTP-level edge must reject the engine.io open
	// before we even reach the connection middleware.
	req := httptest.NewRequest(http.MethodGet, "/socket.io/?EIO=4&transport=polling&t=test", nil)
	if resp := ts.Do(req); resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 from edge, got %d", resp.Code)
	}
}

func TestSocketIOConnectionRejectsBadAuthPayload(t *testing.T) {
	ts := newAuthTestServerWithToken(t, "secret")

	// Correct query string token (so we pass the edge) but wrong auth
	// payload in the CONNECT — the connection middleware should reject.
	_, first, code := pollHandshake(t, ts, "&token=secret", connectPacket("nope"))
	if code != http.StatusOK {
		t.Fatalf("expected 200 (edge accepts), got %d", code)
	}
	// CONNECT_ERROR is socket.io type 4 wrapped in engine.io MESSAGE type 4.
	if !strings.HasPrefix(first, "44{") {
		t.Fatalf("expected CONNECT_ERROR (44{...}), got %q", first)
	}
	if !strings.Contains(first, "unauthorized") {
		t.Fatalf("expected unauthorized message, got %q", first)
	}
}

func TestSocketIOConnectionNoAuthWhenTokenEmpty(t *testing.T) {
	ts := newAuthTestServerWithToken(t, "")

	// No token configured: any handshake is accepted, no auth payload needed.
	_, first, code := pollHandshake(t, ts, "", connectPacket(""))
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.HasPrefix(first, "40{") {
		t.Fatalf("expected CONNECT ACK (40{...}), got %q", first)
	}
}

// Sanity check: the parser handles the real engine.io v4 open payload.
// If the engine.io version ever changes its format, this test will fail
// loudly before the integration tests do.
func TestParsePollingPayloadOpenPacket(t *testing.T) {
	body := `0{"sid":"abc","upgrades":["websocket"],"pingInterval":25000,"pingTimeout":20000}`
	pr, err := parsePollingPayload(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pr.first() != body {
		t.Fatalf("first packet mismatch: got %q want %q", pr.first(), body)
	}
}

// Multiple packets in one response are separated by the record-separator
// byte (0x1E). Verify the parser splits them correctly.
func TestParsePollingPayloadMultiplePackets(t *testing.T) {
	p1 := `0{"sid":"abc"}`
	p2 := `40{"sid":"abc"}`
	body := p1 + engineIOSeparator + p2
	pr, err := parsePollingPayload(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(pr.packets) != 2 {
		t.Fatalf("expected 2 packets, got %d (%q)", len(pr.packets), pr.packets)
	}
	if pr.packets[0] != p1 || pr.packets[1] != p2 {
		t.Fatalf("packets mismatch: %q", pr.packets)
	}
}

// Ensure the polling payload parser rejects malformed input.
func TestParsePollingPayloadMalformed(t *testing.T) {
	if _, err := parsePollingPayload(""); err == nil {
		t.Fatal("expected error for empty payload")
	}
}
