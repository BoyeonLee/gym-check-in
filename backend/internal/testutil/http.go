package testutil

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Login posts to /api/admin/login and returns (access, refresh) tokens.
// Until the auth handler exists (next step), this helper skips the test so
// callers can already commit to the signature.
func Login(t *testing.T, server http.Handler, username, password string) (string, string) {
	t.Helper()

	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Skipf("testutil.Login: /api/admin/login not yet wired (got 404)")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("testutil.Login: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("testutil.Login: decode: %v body=%s", err, rec.Body.String())
	}
	return resp.AccessToken, resp.RefreshToken
}

// AuthRequest issues an authenticated request and returns the recorder.
// `body` is JSON-marshalled when non-nil. Pass an empty access token for
// public routes that nonetheless need to be exercised through this helper.
func AuthRequest(t *testing.T, server http.Handler, method, path, accessToken string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("testutil.AuthRequest: marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	return rec
}
