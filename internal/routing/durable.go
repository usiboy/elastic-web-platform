package routing

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"elastic-web-platform/internal/bundle"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DurableRegistry persists trusted, single-node candidates and host intent.
// It has no tenant-facing API and does not prove DNS, TLS or gateway ACK.
type DurableRegistry struct {
	db        *pgxpool.Pool
	artifacts bundle.Store
}

func NewDurableRegistry(db *pgxpool.Pool, artifacts bundle.Store) *DurableRegistry {
	return &DurableRegistry{db: db, artifacts: artifacts}
}

// RegisterOperatorFixture is intentionally not exposed via an HTTP API. It is
// only for trusted static candidates while builder provenance is not available.
func (r *DurableRegistry) RegisterOperatorFixture(ctx context.Context, b *bundle.Static) (string, error) {
	if b == nil || b.Verify() != nil {
		return "", errors.New("invalid static output")
	}
	if err := r.artifacts.Put(ctx, b); err != nil {
		return "", err
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	id := "dep_" + hex.EncodeToString(bytes)
	m := b.Manifest
	_, err := r.db.Exec(ctx, `INSERT INTO site_deployments(id,team_id,project_id,bundle_digest,source_sha256) VALUES($1,$2,$3,$4,$5)`, id, m.TeamID, m.ProjectID, m.Digest, m.SourceSHA256)
	if err != nil {
		return "", fmt.Errorf("register candidate: %w", err)
	}
	return id, nil
}

// RegisterRuntimePilot records an operator-only local Node endpoint; digest is
// an inventory claim, not an admitted signed runtime artifact or public API.
func (r *DurableRegistry) RegisterRuntimePilot(ctx context.Context, team, project, digest string, port, concurrency int, timeout time.Duration) (string, error) {
	if port < 1024 || port > 65535 {
		return "", errors.New("invalid runtime pilot")
	}
	return r.registerRuntimePilot(ctx, team, project, digest, port, "", concurrency, timeout)
}

var runtimePilotSocket = regexp.MustCompile(`^/run/ewp-r2-[a-z0-9-]+/[a-z0-9.-]+[.]sock$`)

func (r *DurableRegistry) RegisterRuntimePilotUnix(ctx context.Context, team, project, digest, socket string, concurrency int, timeout time.Duration) (string, error) {
	if !runtimePilotSocket.MatchString(socket) || strings.Contains(socket, "..") {
		return "", errors.New("invalid runtime pilot socket")
	}
	return r.registerRuntimePilot(ctx, team, project, digest, 0, socket, concurrency, timeout)
}

func (r *DurableRegistry) registerRuntimePilot(ctx context.Context, team, project, digest string, port int, socket string, concurrency int, timeout time.Duration) (string, error) {
	if team == "" || project == "" || len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" || concurrency < 1 || concurrency > 32 || timeout < 100*time.Millisecond || timeout > 60*time.Second {
		return "", errors.New("invalid runtime pilot")
	}
	if err := probeRuntime(ctx, port, socket); err != nil {
		return "", err
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", err
	}
	id := "dep_" + hex.EncodeToString(idBytes)
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO site_deployments(id,team_id,project_id,bundle_digest,source_sha256)
		VALUES($1,$2,$3,$4,$4)`, id, team, project, digest); err != nil {
		return "", err
	}
	var portValue, socketValue any
	if socket == "" {
		portValue = port
	} else {
		socketValue = socket
	}
	if _, err = tx.Exec(ctx, `INSERT INTO site_runtime_pilots(deployment_id,team_id,project_id,upstream_port,upstream_socket,max_concurrency,timeout_ms,state)
		VALUES($1,$2,$3,$4,$5,$6,$7,'ready')`, id, team, project, portValue, socketValue, concurrency, timeout.Milliseconds()); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func probeRuntime(ctx context.Context, port int, socket string) error {
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	target := fmt.Sprintf("http://127.0.0.1:%d/api/health", port)
	if socket != "" {
		client.Transport = &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}
		target = "http://runtime.internal/api/health"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("runtime health readback unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("runtime health readback failed")
	}
	return nil
}

func (r *DurableRegistry) candidate(ctx context.Context, id string) (string, string, string, error) {
	var team, project, digest string
	var port sql.NullInt32
	var socket sql.NullString
	var state sql.NullString
	err := r.db.QueryRow(ctx, `SELECT d.team_id,d.project_id,d.bundle_digest,rt.upstream_port,rt.upstream_socket,rt.state
		FROM site_deployments d LEFT JOIN site_runtime_pilots rt ON rt.deployment_id=d.id
		WHERE d.id=$1 AND d.status='candidate'`, id).Scan(&team, &project, &digest, &port, &socket, &state)
	if err != nil {
		return "", "", "", err
	}
	if port.Valid || socket.Valid {
		if state.String != "ready" || probeRuntime(ctx, int(port.Int32), socket.String) != nil {
			return "", "", "", errors.New("runtime candidate is not healthy")
		}
		return team, project, digest, nil
	}
	b, err := r.artifacts.Get(ctx, team, project, digest)
	if err != nil || b.Verify() != nil {
		return "", "", "", errors.New("candidate bytes unavailable")
	}
	return team, project, digest, nil
}

func (r *DurableRegistry) BindFixed(ctx context.Context, host, id string) error {
	if _, err := normalizedHost(host); err != nil {
		return err
	}
	team, project, _, err := r.candidate(ctx, id)
	if err != nil {
		return err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),4)`, host); err != nil {
		return err
	}
	var existingID string
	var fixed bool
	err = tx.QueryRow(ctx, `SELECT deployment_id,is_fixed FROM site_hosts WHERE hostname=$1 FOR UPDATE`, host).Scan(&existingID, &fixed)
	if err == nil {
		if fixed && existingID == id {
			if err := setCandidateHost(ctx, tx, id, host); err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
		return errors.New("fixed Host is already owned")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO site_hosts(hostname,team_id,project_id,deployment_id,generation,is_fixed,is_private) VALUES($1,$2,$3,$4,1,true,true)`, host, team, project, id); err != nil {
		return err
	}
	if err := setCandidateHost(ctx, tx, id, host); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO site_route_events(hostname,team_id,project_id,deployment_id,generation) VALUES($1,$2,$3,$4,1)`, host, team, project, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func setCandidateHost(ctx context.Context, tx pgx.Tx, id, host string) error {
	var confirmed string
	err := tx.QueryRow(ctx, `UPDATE site_deployments SET desired_host=$2 WHERE id=$1 AND (desired_host IS NULL OR desired_host=$2) RETURNING id`, id, host).Scan(&confirmed)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("deployment is bound to another fixed Host")
	}
	return err
}

func (r *DurableRegistry) SwapAlias(ctx context.Context, host, team, project string, expected int64, id string, private bool) (int64, error) {
	if _, err := normalizedHost(host); err != nil {
		return 0, err
	}
	targetTeam, targetProject, _, err := r.candidate(ctx, id)
	if err != nil || team != targetTeam || project != targetProject {
		return 0, errors.New("candidate is not owned by alias scope")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),4)`, host); err != nil {
		return 0, err
	}
	var currentTeam, currentProject, previousID string
	var generation int64
	var fixed, wasPrivate bool
	err = tx.QueryRow(ctx, `SELECT team_id,project_id,deployment_id,generation,is_fixed,is_private FROM site_hosts WHERE hostname=$1 FOR UPDATE`, host).Scan(&currentTeam, &currentProject, &previousID, &generation, &fixed, &wasPrivate)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if expected != 0 {
			return 0, errors.New("alias generation conflict")
		}
		generation = 1
		if _, err = tx.Exec(ctx, `INSERT INTO site_hosts(hostname,team_id,project_id,deployment_id,generation,is_fixed,is_private) VALUES($1,$2,$3,$4,1,false,$5)`, host, team, project, id, private); err != nil {
			return 0, err
		}
	case err != nil:
		return 0, err
	default:
		if fixed || currentTeam != team || currentProject != project || wasPrivate != private {
			return 0, errors.New("Host is already owned by a different scope")
		}
		if generation != expected {
			return 0, errors.New("alias generation conflict")
		}
		generation++
		if _, err = tx.Exec(ctx, `UPDATE site_hosts SET deployment_id=$2,generation=$3,updated_at=now() WHERE hostname=$1`, host, id, generation); err != nil {
			return 0, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO site_route_events(hostname,team_id,project_id,previous_deployment_id,deployment_id,generation) VALUES($1,$2,$3,$4,$5,$6)`, host, team, project, nullable(previousID), id, generation); err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return generation, nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// LoadSnapshot fails closed if any currently routed artifact is absent or
// corrupted. A caller must separately prove TLS and live gateway readback.
func (r *DurableRegistry) LoadSnapshot(ctx context.Context) (*Registry, error) {
	rows, err := r.db.Query(ctx, `SELECT h.hostname,h.team_id,h.project_id,h.deployment_id,h.generation,h.is_fixed,h.is_private,d.bundle_digest,
		rt.upstream_port,rt.upstream_socket,rt.max_concurrency,rt.timeout_ms,rt.state,e.previous_deployment_id
		FROM site_hosts h JOIN site_deployments d ON d.id=h.deployment_id AND d.team_id=h.team_id AND d.project_id=h.project_id
		LEFT JOIN site_runtime_pilots rt ON rt.deployment_id=d.id
		LEFT JOIN site_route_events e ON e.hostname=h.hostname AND e.generation=h.generation ORDER BY h.hostname LIMIT 1001`)
	if err != nil {
		return nil, err
	}
	type record struct {
		host, team, project, id, digest string
		generation                      int64
		fixed, private                  bool
		port, concurrency, timeout      sql.NullInt32
		socket                          sql.NullString
		state                           sql.NullString
		previous                        sql.NullString
	}
	var records []record
	for rows.Next() {
		var item record
		if err := rows.Scan(&item.host, &item.team, &item.project, &item.id, &item.generation, &item.fixed, &item.private, &item.digest, &item.port, &item.socket, &item.concurrency, &item.timeout, &item.state, &item.previous); err != nil {
			rows.Close()
			return nil, err
		}
		records = append(records, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(records) > 1000 {
		return nil, errors.New("route snapshot too large")
	}
	snapshot := NewRegistry()
	for _, item := range records {
		if _, err := normalizedHost(item.host); err != nil {
			return nil, err
		}
		if item.port.Valid || item.socket.Valid {
			if item.state.String == "failed" && item.fixed {
				continue
			}
			if item.state.String != "ready" {
				return nil, errors.New("routed runtime is not healthy")
			}
			if probeRuntime(ctx, int(item.port.Int32), item.socket.String) != nil {
				if item.fixed {
					continue // 失联的固定 Preview 不影响其他 Host 启动。
				}
				route := Route{TeamID: item.team, ProjectID: item.project, DeploymentID: item.id, Generation: item.generation, Private: item.private}
				if _, err := r.RollbackRuntime(ctx, item.host, route); err == nil {
					return r.LoadSnapshot(ctx) // 已原子回退，再读一份完整快照。
				}
				return nil, errors.New("routed runtime is not healthy")
			}
			var fallbacks []string
			if !item.fixed {
				fallbacks, err = r.loadFallbacks(ctx, snapshot, item.host, item.team, item.project, item.id, item.generation)
				if err != nil {
					return nil, err
				}
				if len(fallbacks) == 0 {
					return nil, errors.New("runtime alias has no healthy fallback")
				}
			}
			if snapshot.runtimes[item.id] == nil {
				endpoint := fmt.Sprintf("http://127.0.0.1:%d", item.port.Int32)
				if item.socket.Valid {
					endpoint = "unix://" + item.socket.String
				}
				if err := snapshot.RegisterRuntime(item.team, item.project, item.id, endpoint, int(item.concurrency.Int32), time.Duration(item.timeout.Int32)*time.Millisecond); err != nil {
					return nil, err
				}
			}
			snapshot.hosts[item.host] = routeEntry{Route: Route{TeamID: item.team, ProjectID: item.project, DeploymentID: item.id, Generation: item.generation, Private: item.private}, fixed: item.fixed, previous: item.previous.String, fallbacks: fallbacks}
			continue
		}
		b, err := r.artifacts.Get(ctx, item.team, item.project, item.digest)
		if err != nil || b.Verify() != nil {
			return nil, errors.New("routed artifact is unavailable")
		}
		snapshot.deployments[item.id] = b
		snapshot.hosts[item.host] = routeEntry{Route: Route{TeamID: item.team, ProjectID: item.project, DeploymentID: item.id, Generation: item.generation, Private: item.private}, fixed: item.fixed}
	}
	return snapshot, nil
}

func (r *DurableRegistry) loadFallbacks(ctx context.Context, snapshot *Registry, host, team, project, currentID string, generation int64) ([]string, error) {
	rows, err := r.db.Query(ctx, `SELECT previous_deployment_id FROM site_route_events
		WHERE hostname=$1 AND generation<=$2 AND previous_deployment_id IS NOT NULL ORDER BY generation DESC LIMIT 32`, host, generation)
	if err != nil {
		return nil, err
	}
	var history []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		history = append(history, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{currentID: true}
	var fallbacks []string
	for _, id := range history {
		if seen[id] {
			continue
		}
		seen[id] = true
		var digest string
		var port, concurrency, duration sql.NullInt32
		var socket, state sql.NullString
		if err := r.db.QueryRow(ctx, `SELECT d.bundle_digest,rt.upstream_port,rt.upstream_socket,rt.max_concurrency,rt.timeout_ms,rt.state
			FROM site_deployments d LEFT JOIN site_runtime_pilots rt ON rt.deployment_id=d.id
			WHERE d.id=$1 AND d.team_id=$2 AND d.project_id=$3`, id, team, project).Scan(&digest, &port, &socket, &concurrency, &duration, &state); err != nil {
			continue
		}
		if port.Valid || socket.Valid {
			if state.String != "ready" || probeRuntime(ctx, int(port.Int32), socket.String) != nil {
				continue
			}
			if snapshot.runtimes[id] == nil {
				endpoint := fmt.Sprintf("http://127.0.0.1:%d", port.Int32)
				if socket.Valid {
					endpoint = "unix://" + socket.String
				}
				if err := snapshot.RegisterRuntime(team, project, id, endpoint, int(concurrency.Int32), time.Duration(duration.Int32)*time.Millisecond); err != nil {
					return nil, err
				}
			}
		} else {
			previous, err := r.artifacts.Get(ctx, team, project, digest)
			if err != nil || previous.Verify() != nil {
				continue
			}
			snapshot.deployments[id] = previous
		}
		fallbacks = append(fallbacks, id)
	}
	return fallbacks, nil
}

// RollbackRuntime picks the most recent still healthy Deployment for this
// exact Host/Team, skipping already failed dynamic versions. CAS prevents a
// delayed failed request from undoing a newer promotion.
func (r *DurableRegistry) RollbackRuntime(ctx context.Context, host string, route Route) (string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),4)`, host); err != nil {
		return "", err
	}
	var currentID, team, project string
	var generation int64
	var fixed, private bool
	err = tx.QueryRow(ctx, `SELECT deployment_id,team_id,project_id,generation,is_fixed,is_private
		FROM site_hosts WHERE hostname=$1 FOR UPDATE`, host).Scan(&currentID, &team, &project, &generation, &fixed, &private)
	if err != nil || fixed || currentID != route.DeploymentID || team != route.TeamID || project != route.ProjectID || generation != route.Generation || private != route.Private {
		return "", errors.New("runtime alias generation changed")
	}
	rows, err := tx.Query(ctx, `SELECT previous_deployment_id FROM site_route_events
		WHERE hostname=$1 AND generation<=$2 AND previous_deployment_id IS NOT NULL ORDER BY generation DESC LIMIT 32`, host, generation)
	if err != nil {
		return "", err
	}
	var history []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", err
		}
		history = append(history, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	selected := ""
	seen := map[string]bool{currentID: true}
	for _, id := range history {
		if seen[id] {
			continue
		}
		seen[id] = true
		var digest string
		var port sql.NullInt32
		var socket, state sql.NullString
		if err := tx.QueryRow(ctx, `SELECT d.bundle_digest,rt.upstream_port,rt.upstream_socket,rt.state
			FROM site_deployments d LEFT JOIN site_runtime_pilots rt ON rt.deployment_id=d.id
			WHERE d.id=$1 AND d.team_id=$2 AND d.project_id=$3`, id, team, project).Scan(&digest, &port, &socket, &state); err != nil {
			continue
		}
		if port.Valid || socket.Valid {
			if state.String != "ready" || probeRuntime(ctx, int(port.Int32), socket.String) != nil {
				continue
			}
		} else {
			b, err := r.artifacts.Get(ctx, team, project, digest)
			if err != nil || b.Verify() != nil {
				continue
			}
		}
		selected = id
		break
	}
	if selected == "" {
		return "", errors.New("no verified runtime rollback target")
	}
	if _, err = tx.Exec(ctx, `UPDATE site_hosts SET deployment_id=$2,generation=$3,updated_at=now()
		WHERE hostname=$1`, host, selected, generation+1); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO site_route_events(hostname,team_id,project_id,previous_deployment_id,deployment_id,generation)
		VALUES($1,$2,$3,$4,$5,$6)`, host, team, project, currentID, selected, generation+1); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE site_runtime_pilots SET state='failed' WHERE deployment_id=$1 AND team_id=$2 AND project_id=$3`, currentID, team, project); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'runtime.rollback_after_failure',$2)`, team, currentID); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return selected, nil
}
