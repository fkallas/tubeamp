package ytm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// setCookies builds the []*http.Cookie a response would yield from the given
// Set-Cookie header lines, exercising net/http's real parsing (Max-Age=0 etc.).
func setCookies(lines ...string) []*http.Cookie {
	resp := &http.Response{Header: http.Header{}}
	for _, l := range lines {
		resp.Header.Add("Set-Cookie", l)
	}
	return resp.Cookies()
}

func loadAuthFrom(t *testing.T, cookie string) (*Auth, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth")
	if err := os.WriteFile(path, []byte(cookie+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := LoadAuth(path)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	return a, path
}

func readHeader(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back auth file: %v", err)
	}
	return strings.TrimSpace(string(data))
}

// TestMerge_rotatesAndWrites: a rotated value replaces the old one in memory,
// the file is rewritten, the generation counter advances, and order is kept.
func TestMerge_rotatesAndWrites(t *testing.T) {
	a, path := loadAuthFrom(t, "SID=a; SAPISID=sap; SIDCC=old")

	a.merge(setCookies("SIDCC=new; Path=/"))

	if got, want := a.Header(), "SID=a; SAPISID=sap; SIDCC=new"; got != want {
		t.Errorf("Header() = %q, want %q", got, want)
	}
	if got, want := readHeader(t, path), "SID=a; SAPISID=sap; SIDCC=new"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	if a.Generation() != 1 {
		t.Errorf("Generation() = %d, want 1", a.Generation())
	}
}

// TestMerge_rotatedSAPISID: a rotated SAPISID takes effect in SAPISID().
func TestMerge_rotatedSAPISID(t *testing.T) {
	a, _ := loadAuthFrom(t, "SID=a; SAPISID=old_sap")

	a.merge(setCookies("SAPISID=new_sap"))

	got, err := a.SAPISID()
	if err != nil {
		t.Fatalf("SAPISID: %v", err)
	}
	if got != "new_sap" {
		t.Errorf("SAPISID() = %q, want %q", got, "new_sap")
	}
}

// TestMerge_expiredDropped: a Max-Age=0 cookie drops the name everywhere.
func TestMerge_expiredDropped(t *testing.T) {
	a, path := loadAuthFrom(t, "SID=a; SAPISID=sap; SIDCC=old")

	a.merge(setCookies("SIDCC=delete-me; Max-Age=0"))

	if got, want := a.Header(), "SID=a; SAPISID=sap"; got != want {
		t.Errorf("Header() = %q, want %q", got, want)
	}
	if got, want := readHeader(t, path), "SID=a; SAPISID=sap"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	if a.Generation() != 1 {
		t.Errorf("Generation() = %d, want 1", a.Generation())
	}
}

// TestMerge_expiresInPastDropped: an Expires in the past also drops the cookie.
func TestMerge_expiresInPastDropped(t *testing.T) {
	a, _ := loadAuthFrom(t, "SID=a; SIDCC=old")

	a.merge(setCookies("SIDCC=x; Expires=Thu, 01 Jan 1970 00:00:00 GMT"))

	if got, want := a.Header(), "SID=a"; got != want {
		t.Errorf("Header() = %q, want %q", got, want)
	}
}

// TestMerge_unchangedNoWrite: an identical value triggers no file write and no
// generation bump (no needless churn), proven with a write counter.
func TestMerge_unchangedNoWrite(t *testing.T) {
	a, _ := loadAuthFrom(t, "SID=a; SAPISID=sap; SIDCC=same")
	var writes int32
	a.writeFn = func(string, []byte) error {
		atomic.AddInt32(&writes, 1)
		return nil
	}

	// Same value for an existing cookie: nothing changes.
	a.merge(setCookies("SIDCC=same; Path=/"))
	// Deleting an absent cookie: nothing changes.
	a.merge(setCookies("NOPE=x; Max-Age=0"))
	// No cookies at all: nothing changes.
	a.merge(nil)

	if got := atomic.LoadInt32(&writes); got != 0 {
		t.Errorf("write count = %d, want 0", got)
	}
	if a.Generation() != 0 {
		t.Errorf("Generation() = %d, want 0", a.Generation())
	}

	// A genuine change does write exactly once.
	a.merge(setCookies("SIDCC=changed"))
	if got := atomic.LoadInt32(&writes); got != 1 {
		t.Errorf("write count after change = %d, want 1", got)
	}
}

// TestMerge_orderPreservedAndAppend: unchanged entries keep their position, an
// updated entry keeps its position, and a genuinely new cookie is appended.
func TestMerge_orderPreservedAndAppend(t *testing.T) {
	a, _ := loadAuthFrom(t, "A=1; B=2; C=3")

	a.merge(setCookies("B=20", "D=4"))

	if got, want := a.Header(), "A=1; B=20; C=3; D=4"; got != want {
		t.Errorf("Header() = %q, want %q", got, want)
	}
}

// TestMerge_noPathMemoryOnly: an Auth with no source path updates memory but
// never tries to write a file.
func TestMerge_noPathMemoryOnly(t *testing.T) {
	a := &Auth{Cookie: "SID=a; SIDCC=old"}
	called := false
	a.writeFn = func(string, []byte) error { called = true; return nil }

	a.merge(setCookies("SIDCC=new"))

	if called {
		t.Error("writeFn called for an Auth with no path")
	}
	if got, want := a.Header(), "SID=a; SIDCC=new"; got != want {
		t.Errorf("Header() = %q, want %q", got, want)
	}
}

// TestMerge_concurrentThroughClient: a 20-goroutine burst of overlapping
// requests (each absorbing a rotated cookie) must keep the file parseable and
// never lose an unrelated cookie. Runs against a fake server, never the real
// endpoint. Run under -race to prove the mutex holds.
func TestMerge_concurrentThroughClient(t *testing.T) {
	var n int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := strconv.FormatInt(atomic.AddInt64(&n, 1), 10)
		// Rotate a couple of cookies on every response, like Google does.
		http.SetCookie(w, &http.Cookie{Name: "SIDCC", Value: "v" + i})
		http.SetCookie(w, &http.Cookie{Name: "__Secure-1PSIDCC", Value: "s" + i})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	a, path := loadAuthFrom(t, "SID=keep; HSID=keep2; SAPISID=sap; SIDCC=init")
	c := NewClient(a)
	c.baseURL = srv.URL

	const workers = 20
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := c.post(ctx, "search", map[string]any{}); err != nil {
				t.Errorf("post: %v", err)
			}
		}()
	}
	wg.Wait()

	// The file must be parseable and still carry every unrelated cookie.
	reloaded, err := LoadAuth(path)
	if err != nil {
		t.Fatalf("reload after burst: %v", err)
	}
	_, jar := parseCookieHeader(reloaded.Cookie)
	for _, name := range []string{"SID", "HSID", "SAPISID", "SIDCC", "__Secure-1PSIDCC"} {
		if _, ok := jar[name]; !ok {
			t.Errorf("cookie %q lost after concurrent burst; header=%q", name, reloaded.Cookie)
		}
	}
	if jar["SID"] != "keep" || jar["HSID"] != "keep2" || jar["SAPISID"] != "sap" {
		t.Errorf("unrelated cookies mutated: %q", reloaded.Cookie)
	}
}
