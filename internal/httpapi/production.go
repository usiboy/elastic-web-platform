package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"elastic-web-platform/internal/routing"

	"github.com/jackc/pgx/v5"
)

func (s *server) productionHost(projectID string) string {
	if !strings.HasPrefix(projectID, "p_") || len(projectID) != 34 {
		return ""
	}
	if len(s.productionSuffix) > 180 || !previewSuffixPattern.MatchString(s.productionSuffix) || strings.Contains(s.productionSuffix, "..") {
		return ""
	}
	return "p-" + strings.TrimPrefix(projectID, "p_") + "." + s.productionSuffix
}

func (s *server) promoteStaticProduction(w http.ResponseWriter, r *http.Request, actor identity, projectID string) {
	if actor.role != "owner" {
		writeError(w, 403, "forbidden")
		return
	}
	if s.artifacts == nil {
		writeError(w, 503, "artifact_store_unavailable")
		return
	}
	host := s.productionHost(projectID)
	if host == "" {
		writeError(w, 503, "production_domain_unavailable")
		return
	}
	var input struct {
		DeploymentID       string `json:"deploymentId"`
		ExpectedGeneration *int64 `json:"expectedGeneration"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if input.ExpectedGeneration == nil || *input.ExpectedGeneration < 0 || !strings.HasPrefix(input.DeploymentID, "dep_") {
		writeError(w, 422, "invalid_promotion")
		return
	}
	var observed, digest string
	err := s.db.QueryRow(r.Context(), `SELECT observed_state,bundle_digest FROM site_deployments WHERE team_id=$1 AND project_id=$2 AND id=$3`, actor.teamID, projectID, input.DeploymentID).Scan(&observed, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	if observed != "preview_ready" {
		writeError(w, 409, "preview_not_observed")
		return
	}
	if _, err := s.artifacts.Get(r.Context(), actor.teamID, projectID, digest); err != nil {
		writeError(w, 503, "artifact_store_unavailable")
		return
	}
	gen, err := routing.NewDurableRegistry(s.db, s.artifacts).SwapAlias(r.Context(), host, actor.teamID, projectID, *input.ExpectedGeneration, input.DeploymentID, false)
	if err != nil {
		writeError(w, 409, "alias_conflict")
		return
	}
	var observedGeneration int64
	if err := s.db.QueryRow(r.Context(), `SELECT observed_generation FROM site_hosts WHERE hostname=$1 AND team_id=$2 AND project_id=$3`, host, actor.teamID, projectID).Scan(&observedGeneration); err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	writeJSON(w, 202, map[string]any{"desiredHost": host, "deploymentId": input.DeploymentID, "desiredGeneration": gen, "observedGeneration": observedGeneration, "state": "requested"})
}

func (s *server) getProductionAlias(w http.ResponseWriter, r *http.Request, actor identity, projectID string) {
	if actor.role == "agent" && (actor.projectID != projectID || !hasAction(actor.actions, "deployments:read")) {
		writeError(w, 404, "not_found")
		return
	}
	host := s.productionHost(projectID)
	if host == "" {
		writeError(w, 503, "production_domain_unavailable")
		return
	}
	var deployment string
	var observedID sql.NullString
	var desired, observed int64
	err := s.db.QueryRow(r.Context(), `SELECT deployment_id,generation,observed_generation,observed_deployment_id FROM site_hosts WHERE hostname=$1 AND team_id=$2 AND project_id=$3 AND NOT is_fixed AND NOT is_private`, host, actor.teamID, projectID).Scan(&deployment, &desired, &observed, &observedID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	state := "requested"
	if desired == observed && observedID.Valid && observedID.String == deployment {
		state = "active"
	}
	writeJSON(w, 200, map[string]any{"desiredHost": host, "deploymentId": deployment, "desiredGeneration": desired, "observedGeneration": observed, "state": state})
}
