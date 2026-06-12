package ytm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setOAuthEndpoints points the OAuth code/token endpoints at test servers and a
// no-op sleep, restoring the originals on cleanup. These tests must not run in
// parallel since the endpoints are package-global.
func setOAuthEndpoints(t *testing.T, codeURL, tokenURL string) {
	t.Helper()
	oc, ot, osleep := oauthCodeURL, oauthTokenURL, oauthSleep
	oauthCodeURL, oauthTokenURL = codeURL, tokenURL
	oauthSleep = func(context.Context, time.Duration) {} // fast: no real waiting
	t.Cleanup(func() { oauthCodeURL, oauthTokenURL, oauthSleep = oc, ot, osleep })
}

// ---- token store load / save / expiry ----

func TestOAuthToken_SaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "oauth.json") // parent dir must be created
	tok := &OAuthToken{
		AccessToken:  "access-abc",
		RefreshToken: "refresh-xyz",
		Scope:        oauthScope,
		TokenType:    "Bearer",
		ExpiresAt:    1700000000,
		ExpiresIn:    3599,
	}
	if err := tok.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("perms = %o, want 600", fi.Mode().Perm())
	}

	got, err := LoadOAuthToken(path)
	if err != nil {
		t.Fatalf("LoadOAuthToken: %v", err)
	}
	if *got != *tok {
		t.Errorf("roundtrip mismatch:\n got %+v\nwant %+v", *got, *tok)
	}
}

func TestLoadOAuthToken_errors(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOAuthToken(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("missing file: want error")
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOAuthToken(bad); err == nil {
		t.Error("malformed json: want error")
	}
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{"scope":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOAuthToken(empty); err == nil {
		t.Error("no tokens: want error")
	}
}

func TestOAuthToken_IsExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name      string
		expiresAt int64
		want      bool
	}{
		{"long valid", now.Unix() + 3600, false},
		{"just past 60s window", now.Unix() + 61, false},
		{"inside 60s window", now.Unix() + 30, true},
		{"already expired", now.Unix() - 10, true},
		{"zero (unset)", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tok := &OAuthToken{ExpiresAt: c.expiresAt}
			if got := tok.IsExpired(now); got != c.want {
				t.Errorf("IsExpired = %v, want %v", got, c.want)
			}
		})
	}
}

func TestOAuthToken_Authorization(t *testing.T) {
	if got := (&OAuthToken{TokenType: "Bearer", AccessToken: "tok"}).Authorization(); got != "Bearer tok" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer tok")
	}
	// Empty token type defaults to Bearer.
	if got := (&OAuthToken{AccessToken: "tok"}).Authorization(); got != "Bearer tok" {
		t.Errorf("Authorization = %q, want %q (default type)", got, "Bearer tok")
	}
}

// ---- Refresh ----

func TestOAuthToken_Refresh_ok(t *testing.T) {
	var gotForm map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = map[string]string{
			"grant_type":    r.PostForm.Get("grant_type"),
			"refresh_token": r.PostForm.Get("refresh_token"),
			"client_id":     r.PostForm.Get("client_id"),
			"client_secret": r.PostForm.Get("client_secret"),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","expires_in":3600,"scope":"s","token_type":"Bearer"}`))
	}))
	defer srv.Close()
	setOAuthEndpoints(t, "", srv.URL)

	tok := &OAuthToken{AccessToken: "old", RefreshToken: "refresh-1", TokenType: "Bearer"}
	before := time.Now().Unix()
	if err := tok.Refresh(context.Background(), OAuthCreds{ClientID: "cid", ClientSecret: "csecret"}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.AccessToken != "new-access" {
		t.Errorf("AccessToken = %q, want new-access", tok.AccessToken)
	}
	if tok.RefreshToken != "refresh-1" {
		t.Errorf("RefreshToken changed to %q, want refresh-1 (preserved)", tok.RefreshToken)
	}
	if tok.ExpiresAt < before+3500 {
		t.Errorf("ExpiresAt = %d, want ~now+3600", tok.ExpiresAt)
	}
	if gotForm["grant_type"] != oauthGrantRefresh || gotForm["refresh_token"] != "refresh-1" ||
		gotForm["client_id"] != "cid" || gotForm["client_secret"] != "csecret" {
		t.Errorf("refresh form = %+v", gotForm)
	}
}

func TestOAuthToken_Refresh_invalidGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
	}))
	defer srv.Close()
	setOAuthEndpoints(t, "", srv.URL)

	tok := &OAuthToken{AccessToken: "old", RefreshToken: "dead", TokenType: "Bearer"}
	err := tok.Refresh(context.Background(), OAuthCreds{ClientID: "cid", ClientSecret: "csecret"})
	if err == nil {
		t.Fatal("Refresh: want error for invalid_grant")
	}
	var oerr *OAuthError
	if !errors.As(err, &oerr) || oerr.Code != "invalid_grant" {
		t.Errorf("err = %v, want *OAuthError code invalid_grant", err)
	}
	if tok.AccessToken != "old" {
		t.Errorf("AccessToken mutated on failed refresh: %q", tok.AccessToken)
	}
}

// ---- device-flow polling ----

func TestPollToken_pendingThenSuccess(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != oauthGrantDevice {
			t.Errorf("grant_type = %q, want device grant", r.PostForm.Get("grant_type"))
		}
		if r.PostForm.Get("code") != "dev-code" {
			t.Errorf("code = %q, want dev-code", r.PostForm.Get("code"))
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch calls {
		case 1:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
		case 2:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"slow_down"}`))
		default:
			_, _ = w.Write([]byte(`{"access_token":"acc","refresh_token":"ref","expires_in":3600,"scope":"s","token_type":"Bearer"}`))
		}
	}))
	defer srv.Close()
	setOAuthEndpoints(t, "", srv.URL)

	dc := DeviceCode{DeviceCode: "dev-code", UserCode: "ABCD-EFGH", Interval: 1}
	tok, err := PollToken(context.Background(), OAuthCreds{ClientID: "cid", ClientSecret: "csecret"}, dc)
	if err != nil {
		t.Fatalf("PollToken: %v", err)
	}
	if calls != 3 {
		t.Errorf("token endpoint calls = %d, want 3 (pending, slow_down, success)", calls)
	}
	if tok.AccessToken != "acc" || tok.RefreshToken != "ref" {
		t.Errorf("token = %+v, want acc/ref", tok)
	}
	if tok.ExpiresAt <= time.Now().Unix() {
		t.Errorf("ExpiresAt = %d, want in the future", tok.ExpiresAt)
	}
}

func TestPollToken_denied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"access_denied"}`))
	}))
	defer srv.Close()
	setOAuthEndpoints(t, "", srv.URL)

	dc := DeviceCode{DeviceCode: "dev-code", Interval: 1}
	if _, err := PollToken(context.Background(), OAuthCreds{ClientID: "c", ClientSecret: "s"}, dc); err == nil {
		t.Fatal("PollToken: want error for access_denied")
	}
}

func TestPollToken_ctxCancelled(t *testing.T) {
	// A server that never authorizes; a cancelled context must abort the loop.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
	}))
	defer srv.Close()
	setOAuthEndpoints(t, "", srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dc := DeviceCode{DeviceCode: "dev-code", Interval: 1}
	if _, err := PollToken(ctx, OAuthCreds{ClientID: "c", ClientSecret: "s"}, dc); err == nil {
		t.Fatal("PollToken: want error on cancelled context")
	}
}

// ---- RequestDeviceCode ----

func TestRequestDeviceCode_ok(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("scope") != oauthScope {
			t.Errorf("scope = %q, want %q", r.PostForm.Get("scope"), oauthScope)
		}
		if r.PostForm.Get("client_id") != "cid" {
			t.Errorf("client_id = %q, want cid", r.PostForm.Get("client_id"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"ABCD-EFGH","verification_url":"https://www.google.com/device","expires_in":1800,"interval":5}`))
	}))
	defer srv.Close()
	setOAuthEndpoints(t, srv.URL, "")

	dc, err := RequestDeviceCode(context.Background(), OAuthCreds{ClientID: "cid", ClientSecret: "csecret"})
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}
	if dc.DeviceCode != "dc" || dc.UserCode != "ABCD-EFGH" || dc.VerificationURL == "" || dc.Interval != 5 {
		t.Errorf("device code = %+v", dc)
	}
}

// ---- Client OAuth post path ----

// TestClient_CookiePost_unchanged verifies cookie mode still signs with the
// Cookie header + SAPISIDHASH and never sends a Bearer token.
func TestClient_CookiePost_unchanged(t *testing.T) {
	var gotAuth, gotCookie, gotAuthUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		gotAuthUser = r.Header.Get("X-Goog-AuthUser")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	a := &Auth{Cookie: "SAPISID=secret_sap; SID=abc"}
	c := NewClient(a)
	c.baseURL = srv.URL
	c.SetAuthUser(2)

	if _, _, err := c.AccountInfo(context.Background()); err != nil {
		t.Fatalf("AccountInfo: %v", err)
	}
	if !strings.HasPrefix(gotAuth, "SAPISIDHASH ") {
		t.Errorf("Authorization = %q, want SAPISIDHASH prefix", gotAuth)
	}
	if !strings.Contains(gotCookie, "SAPISID=secret_sap") {
		t.Errorf("Cookie = %q, want it to carry the cookie set", gotCookie)
	}
	if gotAuthUser != "2" {
		t.Errorf("X-Goog-AuthUser = %q, want 2", gotAuthUser)
	}
}

// ensure tokenResponse decodes a representative Google body without error
// (guards against tag drift).
func TestTokenResponse_decodes(t *testing.T) {
	var tr tokenResponse
	body := `{"access_token":"a","expires_in":3599,"refresh_token":"r","scope":"s","token_type":"Bearer"}`
	if err := json.Unmarshal([]byte(body), &tr); err != nil {
		t.Fatal(err)
	}
	if tr.AccessToken != "a" || tr.ExpiresIn != 3599 || tr.RefreshToken != "r" {
		t.Errorf("decoded = %+v", tr)
	}
}
