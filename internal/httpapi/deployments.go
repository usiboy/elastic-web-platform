package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/framework"

	"github.com/jackc/pgx/v5"
)

var sourceIDPattern = regexp.MustCompile(`^src_[0-9a-f]{32}$`)
var previewSuffixPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*[a-z0-9]$`)

type staticCandidate struct {
	DeploymentID  string `json:"deploymentId"`
	DesiredHost   string `json:"desiredHost"`
	DesiredState  string `json:"desiredState"`
	ObservedState string `json:"observedState"`
	StatusURL     string `json:"statusUrl"`
}

func mayCreateDeployment(actor identity, projectID string) (bool, bool) {
	if actor.role == "agent" {
		if actor.projectID != projectID {
			return false, false
		}
		return hasAction(actor.actions, "deployments:create"), true
	}
	return actor.role == "owner" || actor.role == "developer", true
}

func (s *server) createStaticCandidate(w http.ResponseWriter, r *http.Request, actor identity, projectID string) {
	allowed, scope := mayCreateDeployment(actor, projectID)
	if !scope {
		writeError(w, 404, "not_found")
		return
	}
	if !allowed {
		writeError(w, 403, "forbidden")
		return
	}
	if s.sources == nil || s.artifacts == nil || len(s.previewSuffix) > 180 || !previewSuffixPattern.MatchString(s.previewSuffix) || strings.Contains(s.previewSuffix, "..") {
		writeError(w, 503, "static_delivery_unavailable")
		return
	}
	key := idempotencyKey(r)
	if key == "" {
		writeError(w, 422, "idempotency_key_required")
		return
	}
	var input struct {
		SourceID string `json:"sourceId"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if !sourceIDPattern.MatchString(input.SourceID) {
		writeError(w, 422, "invalid_source_id")
		return
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer tx.Rollback(ctx)
	var locked string
	err = tx.QueryRow(ctx, `SELECT id FROM projects WHERE id=$1 AND team_id=$2 FOR UPDATE`, projectID, actor.teamID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	digest := payloadHash(input)
	var previous []byte
	var existingID string
	err = tx.QueryRow(ctx, `SELECT request_hash,deployment_id FROM deployment_requests WHERE team_id=$1 AND project_id=$2 AND request_key=$3`, actor.teamID, projectID, key).Scan(&previous, &existingID)
	if err == nil {
		if !sameDigest(previous, digest) {
			writeError(w, 409, "idempotency_conflict")
			return
		}
		var result staticCandidate
		result.StatusURL = "/v1/teams/" + actor.teamID + "/projects/" + projectID + "/deployments/" + existingID
		err = tx.QueryRow(ctx, `SELECT id,desired_host,status,observed_state FROM site_deployments WHERE team_id=$1 AND project_id=$2 AND id=$3`, actor.teamID, projectID, existingID).Scan(&result.DeploymentID, &result.DesiredHost, &result.DesiredState, &result.ObservedState)
		if err != nil {
			writeError(w, 503, "database_unavailable")
			return
		}
		writeJSON(w, 200, result)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "database_unavailable")
		return
	}
	var sourceSHA string
	err = tx.QueryRow(ctx, `SELECT sha256 FROM source_uploads WHERE team_id=$1 AND project_id=$2 AND id=$3 AND status='stored_unverified'`, actor.teamID, projectID, input.SourceID).Scan(&sourceSHA)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	data, err := s.sources.Get(ctx, actor.teamID, projectID, sourceSHA)
	if err != nil {
		writeError(w, 503, "source_store_unavailable")
		return
	}
	actual := sha256.Sum256(data)
	if sourceSHA != hex.EncodeToString(actual[:]) {
		writeError(w, 503, "source_mismatch")
		return
	}
	frameworkConfig, err := framework.Detect(data)
	if err != nil {
		writeError(w, 422, "unsupported_source")
		return
	}
	if frameworkConfig.RequiresBuild {
		writeError(w, 409, "isolated_build_required")
		return
	}
	static, err := bundle.CaptureStaticSource(actor.teamID, projectID, data)
	if err != nil {
		writeError(w, 422, "static_source_rejected")
		return
	}
	if static.Manifest.SourceSHA256 != sourceSHA {
		writeError(w, 503, "static_capture_failed")
		return
	}
	if err := s.artifacts.Put(ctx, static); err != nil {
		writeError(w, 503, "artifact_store_unavailable")
		return
	}
	id, err := newID("dep_")
	if err != nil {
		writeError(w, 503, "entropy_unavailable")
		return
	}
	host := "d-" + strings.TrimPrefix(id, "dep_") + "." + s.previewSuffix
	_, err = tx.Exec(ctx, `INSERT INTO site_deployments(id,team_id,project_id,bundle_digest,source_sha256,source_kind,source_id,desired_host) VALUES($1,$2,$3,$4,$5,'upload',$6,$7)`, id, actor.teamID, projectID, static.Manifest.Digest, sourceSHA, input.SourceID, host)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO site_hosts(hostname,team_id,project_id,deployment_id,generation,is_fixed,is_private) VALUES($1,$2,$3,$4,1,true,true)`, host, actor.teamID, projectID, id)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO site_route_events(hostname,team_id,project_id,deployment_id,generation) VALUES($1,$2,$3,$4,1)`, host, actor.teamID, projectID, id)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO deployment_requests(team_id,project_id,request_key,request_hash,deployment_id) VALUES($1,$2,$3,$4,$5)`, actor.teamID, projectID, key, digest[:], id)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,actor_token_id,action,subject_id) VALUES($1,$2,'static.candidate_created',$3)`, actor.teamID, actor.id, id)
	}
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, 202, staticCandidate{DeploymentID: id, DesiredHost: host, DesiredState: "candidate", ObservedState: "not_deployed", StatusURL: "/v1/teams/" + actor.teamID + "/projects/" + projectID + "/deployments/" + id})
}

func (s *server) getStaticCandidate(w http.ResponseWriter, r *http.Request, actor identity, projectID, id string) {
	if actor.role == "agent" && (actor.projectID != projectID || !hasAction(actor.actions, "deployments:read")) {
		writeError(w, 404, "not_found")
		return
	}
	if s.artifacts == nil {
		writeError(w, 503, "artifact_store_unavailable")
		return
	}
	var result staticCandidate
	var digest string
	result.StatusURL = "/v1/teams/" + actor.teamID + "/projects/" + projectID + "/deployments/" + id
	err := s.db.QueryRow(r.Context(), `SELECT id,desired_host,status,observed_state,bundle_digest FROM site_deployments WHERE team_id=$1 AND project_id=$2 AND id=$3`, actor.teamID, projectID, id).Scan(&result.DeploymentID, &result.DesiredHost, &result.DesiredState, &result.ObservedState, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	if _, err := s.artifacts.Get(r.Context(), actor.teamID, projectID, digest); err != nil {
		writeError(w, 503, "artifact_store_unavailable")
		return
	}
	writeJSON(w, 200, result)
}
