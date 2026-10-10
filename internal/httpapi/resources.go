package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ResourceAmount is a reservation in the control database, not a measurement
// or a claim that CPU/memory/storage limits were enforced by a runtime.
type resourceAmount struct {
	CPUMilli   int64 `json:"cpuMilli"`
	MemoryMiB  int64 `json:"memoryMiB"`
	StorageMiB int64 `json:"storageMiB"`
}

type resourceChange struct {
	resourceAmount
	ExpectedVersion *int64 `json:"expectedVersion"`
}

func validResourceChange(change resourceChange, allowRelease bool) bool {
	if change.ExpectedVersion == nil || *change.ExpectedVersion < 0 || *change.ExpectedVersion > 1<<40 {
		return false
	}
	if allowRelease && change.CPUMilli == 0 && change.MemoryMiB == 0 && change.StorageMiB == 0 {
		return true
	}
	return change.CPUMilli >= 100 && change.CPUMilli <= 100000 && change.MemoryMiB >= 128 && change.MemoryMiB <= 1048576 && change.StorageMiB >= 128 && change.StorageMiB <= 1048576
}

func (s *server) setTeamResourceBudget(w http.ResponseWriter, r *http.Request, teamID string) {
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if s.bootstrap == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(s.bootstrap)) != 1 {
		writeError(w, 401, "unauthenticated")
		return
	}
	var input resourceChange
	if !readJSON(w, r, &input) {
		return
	}
	if !validResourceChange(input, false) {
		writeError(w, 422, "invalid_resource_budget")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer tx.Rollback(r.Context())
	var found string
	err = tx.QueryRow(r.Context(), `SELECT id FROM teams WHERE id=$1 FOR UPDATE`, teamID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	var version int64
	err = tx.QueryRow(r.Context(), `SELECT version FROM team_resource_budgets WHERE team_id=$1`, teamID).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "database_unavailable")
		return
	}
	if version != *input.ExpectedVersion {
		writeError(w, 409, "resource_version_conflict")
		return
	}
	var reserved resourceAmount
	err = tx.QueryRow(r.Context(), `SELECT COALESCE(sum(cpu_milli),0),COALESCE(sum(memory_mib),0),COALESCE(sum(storage_mib),0) FROM project_resource_reservations WHERE team_id=$1`, teamID).Scan(&reserved.CPUMilli, &reserved.MemoryMiB, &reserved.StorageMiB)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	if exceeds(input.resourceAmount, reserved) {
		writeError(w, 409, "resource_budget_below_reservations")
		return
	}
	version++
	_, err = tx.Exec(r.Context(), `INSERT INTO team_resource_budgets(team_id,cpu_milli,memory_mib,storage_mib,version) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(team_id) DO UPDATE SET cpu_milli=excluded.cpu_milli,memory_mib=excluded.memory_mib,storage_mib=excluded.storage_mib,version=excluded.version,updated_at=now()`, teamID, input.CPUMilli, input.MemoryMiB, input.StorageMiB, version)
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'resources.team_budget_set',$2)`, teamID, teamID); err != nil {
		writeDatabaseError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeDatabaseError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	status := http.StatusOK
	if version == 1 {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"teamId": teamID, "version": version, "budget": input.resourceAmount, "state": "budgeted_not_provisioned"})
}

func exceeds(budget, requested resourceAmount) bool {
	return requested.CPUMilli > budget.CPUMilli || requested.MemoryMiB > budget.MemoryMiB || requested.StorageMiB > budget.StorageMiB
}

func (s *server) getTeamResources(w http.ResponseWriter, r *http.Request, actor identity) {
	if actor.role == "agent" {
		writeError(w, 403, "forbidden")
		return
	}
	var budget, reserved resourceAmount
	var version int64
	err := s.db.QueryRow(r.Context(), `SELECT b.cpu_milli,b.memory_mib,b.storage_mib,b.version,
		COALESCE(sum(r.cpu_milli),0),COALESCE(sum(r.memory_mib),0),COALESCE(sum(r.storage_mib),0)
		FROM team_resource_budgets b LEFT JOIN project_resource_reservations r ON r.team_id=b.team_id
		WHERE b.team_id=$1 GROUP BY b.team_id`, actor.teamID).Scan(
		&budget.CPUMilli, &budget.MemoryMiB, &budget.StorageMiB, &version,
		&reserved.CPUMilli, &reserved.MemoryMiB, &reserved.StorageMiB,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "resource_budget_unavailable")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"teamId": actor.teamID, "version": version, "budget": budget, "reserved": reserved,
		"available": resourceAmount{budget.CPUMilli - reserved.CPUMilli, budget.MemoryMiB - reserved.MemoryMiB, budget.StorageMiB - reserved.StorageMiB}, "state": "budgeted_not_provisioned"})
}

func (s *server) getProjectResources(w http.ResponseWriter, r *http.Request, actor identity, projectID string) {
	if actor.role == "agent" && (actor.projectID != projectID || !hasAction(actor.actions, "projects:read")) {
		writeError(w, 404, "not_found")
		return
	}
	var amounts resourceAmount
	var version int64
	err := s.db.QueryRow(r.Context(), `SELECT COALESCE(r.cpu_milli,0),COALESCE(r.memory_mib,0),COALESCE(r.storage_mib,0),COALESCE(r.version,0)
		FROM projects p LEFT JOIN project_resource_reservations r ON r.team_id=p.team_id AND r.project_id=p.id WHERE p.team_id=$1 AND p.id=$2`, actor.teamID, projectID).Scan(&amounts.CPUMilli, &amounts.MemoryMiB, &amounts.StorageMiB, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	state := "unallocated"
	if amounts.CPUMilli != 0 {
		state = "reserved_not_provisioned"
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"teamId": actor.teamID, "projectId": projectID, "version": version, "resources": amounts, "state": state})
}

func (s *server) setProjectResources(w http.ResponseWriter, r *http.Request, actor identity, projectID string) {
	if actor.role == "agent" && actor.projectID != projectID {
		writeError(w, 404, "not_found")
		return
	}
	if actor.role != "owner" {
		writeError(w, 403, "forbidden")
		return
	}
	var input resourceChange
	if !readJSON(w, r, &input) {
		return
	}
	if !validResourceChange(input, true) {
		writeError(w, 422, "invalid_resource_reservation")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer tx.Rollback(r.Context())
	// 同一个 Team 的所有预算与项目更新锁同一行，避免并行项目超售。
	var found string
	err = tx.QueryRow(r.Context(), `SELECT id FROM teams WHERE id=$1 FOR UPDATE`, actor.teamID).Scan(&found)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	err = tx.QueryRow(r.Context(), `SELECT id FROM projects WHERE team_id=$1 AND id=$2`, actor.teamID, projectID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	var budget resourceAmount
	err = tx.QueryRow(r.Context(), `SELECT cpu_milli,memory_mib,storage_mib FROM team_resource_budgets WHERE team_id=$1`, actor.teamID).Scan(&budget.CPUMilli, &budget.MemoryMiB, &budget.StorageMiB)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "resource_budget_unavailable")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	var current resourceAmount
	var version int64
	err = tx.QueryRow(r.Context(), `SELECT cpu_milli,memory_mib,storage_mib,version FROM project_resource_reservations WHERE team_id=$1 AND project_id=$2`, actor.teamID, projectID).Scan(&current.CPUMilli, &current.MemoryMiB, &current.StorageMiB, &version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "database_unavailable")
		return
	}
	if version != *input.ExpectedVersion {
		writeError(w, 409, "resource_version_conflict")
		return
	}
	var reserved resourceAmount
	err = tx.QueryRow(r.Context(), `SELECT COALESCE(sum(cpu_milli),0),COALESCE(sum(memory_mib),0),COALESCE(sum(storage_mib),0) FROM project_resource_reservations WHERE team_id=$1`, actor.teamID).Scan(&reserved.CPUMilli, &reserved.MemoryMiB, &reserved.StorageMiB)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	requested := resourceAmount{reserved.CPUMilli - current.CPUMilli + input.CPUMilli, reserved.MemoryMiB - current.MemoryMiB + input.MemoryMiB, reserved.StorageMiB - current.StorageMiB + input.StorageMiB}
	if exceeds(budget, requested) {
		writeError(w, 409, "resource_budget_exceeded")
		return
	}
	version++
	_, err = tx.Exec(r.Context(), `INSERT INTO project_resource_reservations(team_id,project_id,cpu_milli,memory_mib,storage_mib,version) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(team_id,project_id) DO UPDATE SET cpu_milli=excluded.cpu_milli,memory_mib=excluded.memory_mib,storage_mib=excluded.storage_mib,version=excluded.version,updated_at=now()`, actor.teamID, projectID, input.CPUMilli, input.MemoryMiB, input.StorageMiB, version)
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(team_id,actor_token_id,action,subject_id) VALUES($1,$2,'resources.project_reserved',$3)`, actor.teamID, actor.id, projectID); err != nil {
		writeDatabaseError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeDatabaseError(w, err)
		return
	}
	state := "reserved_not_provisioned"
	if input.CPUMilli == 0 {
		state = "unallocated"
	}
	status := http.StatusOK
	if version == 1 {
		status = http.StatusCreated
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]any{"teamId": actor.teamID, "projectId": projectID, "version": version, "resources": input.resourceAmount, "state": state})
}
