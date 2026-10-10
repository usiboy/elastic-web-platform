package routing

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"elastic-web-platform/internal/bundle"
)

func TestPreviewExchangeCreatesHostOnlySessionAndRevocationClosesAssets(t *testing.T) {
	db := routeDatabase(t)
	ctx := context.Background()
	control := NewDurableRegistry(db, bundle.NewDiskStore(t.TempDir()))
	a, err := control.RegisterOperatorFixture(ctx, static(t, "t_alpha", "p_alpha", "private A"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := control.RegisterOperatorFixture(ctx, static(t, "t_other", "p_other", "private B"))
	if err != nil {
		t.Fatal(err)
	}
	if err := control.BindFixed(ctx, "a.preview.test", a); err != nil {
		t.Fatal(err)
	}
	if err := control.BindFixed(ctx, "b.preview.test", b); err != nil {
		t.Fatal(err)
	}
	snapshot, err := control.LoadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorToken := "ewp_owner_test"
	hash := sha256.Sum256([]byte(actorToken))
	if _, err := db.Exec(ctx, `INSERT INTO access_tokens(id,team_id,token_hash,role,expires_at) VALUES('key_owner','t_alpha',$1,'owner',$2)`, hash[:], time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	code := "ewp_" + strings.Repeat("a", 43)
	codeHash := sha256.Sum256([]byte(code))
	if _, err := db.Exec(ctx, `INSERT INTO preview_exchanges(code_hash,team_id,project_id,deployment_id,actor_token_id,expires_at) VALUES($1,'t_alpha','p_alpha',$2,'key_owner',$3)`, codeHash[:], a, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	sessions := NewPreviewSessions(db, "https://console.preview.test")
	h := NewGatewayWithSessions(snapshot, sessions)
	get := func(host, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "https://"+host+path, nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := get("a.preview.test", "/app.js", nil); w.Code != 401 {
		t.Fatalf("anonymous preview JS: %d", w.Code)
	}
	exchange := func(origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "https://a.preview.test/session/exchange", strings.NewReader(url.Values{"code": {code}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := exchange("https://attacker.test"); w.Code != 403 {
		t.Fatalf("wrong Origin used code: %d", w.Code)
	}
	w := exchange("https://console.preview.test")
	if w.Code != 303 || w.Header().Get("Location") != "/" {
		t.Fatalf("session exchange: %d %v", w.Code, w.Header())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-ewp_preview" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Domain != "" || cookies[0].Path != "/" {
		t.Fatalf("unsafe preview cookie: %+v", cookies)
	}
	if w := exchange("https://console.preview.test"); w.Code != 401 {
		t.Fatalf("one-time code replay: %d", w.Code)
	}
	if w := get("a.preview.test", "/app.js", cookies[0]); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("private JS: %d", w.Code)
	}
	if w := get("b.preview.test", "/index.html", cookies[0]); w.Code != 401 {
		t.Fatalf("session crossed Team/Host: %d", w.Code)
	}
	if _, err := db.Exec(ctx, `UPDATE access_tokens SET revoked_at=now() WHERE id='key_owner'`); err != nil {
		t.Fatal(err)
	}
	if w := get("a.preview.test", "/index.html", cookies[0]); w.Code != 401 {
		t.Fatalf("revoked actor kept preview: %d", w.Code)
	}
}
