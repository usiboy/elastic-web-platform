package routing

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const previewCookie = "__Host-ewp_preview"

type PreviewSessions struct {
	db            *pgxpool.Pool
	controlOrigin string
}

func NewPreviewSessions(db *pgxpool.Pool, controlOrigin string) *PreviewSessions {
	return &PreviewSessions{db: db, controlOrigin: strings.TrimSuffix(controlOrigin, "/")}
}

// Exchange consumes a single-use code scoped to this exact Deployment. Codes
// are sent in POST bodies and never placed in redirects or browser URL paths.
func (s *PreviewSessions) Exchange(w http.ResponseWriter, r *http.Request, route Route) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.TLS == nil || s.controlOrigin == "" || r.Header.Get("Origin") != s.controlOrigin {
		http.Error(w, "invalid preview origin", http.StatusForbidden)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		http.Error(w, "invalid request", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	code := r.PostForm.Get("code")
	if len(code) < 20 || len(code) > 128 || !strings.HasPrefix(code, "ewp_") {
		http.Error(w, "invalid code", http.StatusUnauthorized)
		return
	}
	hash := sha256.Sum256([]byte(code))
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		http.Error(w, "preview service unavailable", http.StatusServiceUnavailable)
		return
	}
	defer tx.Rollback(ctx)
	var actorID string
	err = tx.QueryRow(ctx, `UPDATE preview_exchanges e SET consumed_at=now() FROM access_tokens a
		WHERE e.code_hash=$1 AND e.team_id=$2 AND e.project_id=$3 AND e.deployment_id=$4 AND e.consumed_at IS NULL AND e.expires_at>now()
		AND a.id=e.actor_token_id AND a.team_id=e.team_id AND a.revoked_at IS NULL AND a.expires_at>now()
		RETURNING e.actor_token_id`, hash[:], route.TeamID, route.ProjectID, route.DeploymentID).Scan(&actorID)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "preview code expired", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "preview service unavailable", http.StatusServiceUnavailable)
		return
	}
	bytes := make([]byte, 32)
	if _, err = rand.Read(bytes); err != nil {
		http.Error(w, "preview service unavailable", http.StatusServiceUnavailable)
		return
	}
	secret := "v1_" + base64.RawURLEncoding.EncodeToString(bytes)
	sessionHash := sha256.Sum256([]byte(secret))
	expiry := time.Now().Add(10 * time.Minute)
	_, err = tx.Exec(ctx, `INSERT INTO preview_sessions(session_hash,team_id,project_id,deployment_id,actor_token_id,expires_at) VALUES($1,$2,$3,$4,$5,$6)`, sessionHash[:], route.TeamID, route.ProjectID, route.DeploymentID, actorID, expiry)
	if err != nil {
		http.Error(w, "preview service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		http.Error(w, "preview service unavailable", http.StatusServiceUnavailable)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: previewCookie, Value: secret, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: expiry})
	w.Header().Set("Location", "/")
	w.WriteHeader(http.StatusSeeOther)
}

// Authorize checks current membership token state on every private asset
// request, so revocation cannot be bypassed by a previously issued cookie.
func (s *PreviewSessions) Authorize(r *http.Request, route Route) bool {
	if r.TLS == nil || !route.Private {
		return false
	}
	cookie, err := r.Cookie(previewCookie)
	if err != nil || len(cookie.Value) < 20 || len(cookie.Value) > 128 {
		return false
	}
	hash := sha256.Sum256([]byte(cookie.Value))
	var id string
	err = s.db.QueryRow(r.Context(), `SELECT s.actor_token_id FROM preview_sessions s JOIN access_tokens a ON a.id=s.actor_token_id
		WHERE s.session_hash=$1 AND s.team_id=$2 AND s.project_id=$3 AND s.deployment_id=$4
		AND s.expires_at>now() AND s.revoked_at IS NULL AND a.team_id=s.team_id AND a.expires_at>now() AND a.revoked_at IS NULL`, hash[:], route.TeamID, route.ProjectID, route.DeploymentID).Scan(&id)
	return err == nil
}
