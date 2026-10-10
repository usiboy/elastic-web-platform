// operator-runtime-loop controls only the approved synthetic A-Team pilot.
// It is root-only, not a tenant API and not a generic build attestation.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"elastic-web-platform/db/migrations"
	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/routing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	team            = "t_f9cf963718a72612c48940af8a8296e4"
	project         = "p_34401f5c05af1079010cb95ee3ec1275"
	baseline        = "dep_f243de7290f4ae2f40c0bcaa626185f5"
	digest          = "a40af88a426cd65227352c1675425717f15393294e8685a6a62dbee8d42fe4c1"
	socket          = "/run/ewp-r2-20261007/runtime.sock"
	digestV3        = "8668dfa8e229e7305b286b85122513651107631447125d78ddf9b307b80c6e78"
	socketV3        = "/run/ewp-r2-20261007/v3.sock"
	digestXianlixiu = "afaf12b90deb1c89fe9ee7cbeea6ba15462bebefa3e4df32aae5797088d1eb25"
	socketXianlixiu = "/run/ewp-r2-20261007/xianlixiu.sock"
	alias           = "r2-stage.preview.ecp.lingtong.com"
	previewSuffix   = ".preview.ecp.lingtong.com"
)

func validateAction(action string, expected int64, deployment string) error {
	switch action {
	case "register", "register-v3", "register-xianlixiu", "baseline":
		if expected != -1 || deployment != "" {
			return errors.New("unexpected version or deployment")
		}
	case "promote":
		if expected < 1 || expected > 31 || !routing.ValidRuntimeDeploymentID(deployment) {
			return errors.New("expected baseline version and runtime deployment")
		}
	case "rollback":
		if expected < 2 || expected > 32 || deployment != baseline {
			return errors.New("only fixed baseline rollback permitted")
		}
	default:
		return errors.New("only fixed synthetic runtime actions are supported")
	}
	return nil
}

func run(ctx context.Context, action string, expected int64, deployment string) error {
	if err := validateAction(action, expected, deployment); err != nil {
		return err
	}
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("ARTIFACT_STORE_DIR") == "" {
		return errors.New("protected site DB and artifact store required")
	}
	db, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()
	if err := migrations.Apply(ctx, db); err != nil {
		return err
	}
	control := routing.NewDurableRegistry(db, bundle.NewDiskStore(os.Getenv("ARTIFACT_STORE_DIR")))
	switch action {
	case "register", "register-v3", "register-xianlixiu":
		pinnedDigest, pinnedSocket := digest, socket
		if action == "register-v3" {
			pinnedDigest, pinnedSocket = digestV3, socketV3
		}
		if action == "register-xianlixiu" {
			pinnedDigest, pinnedSocket = digestXianlixiu, socketXianlixiu
		}
		id, err := control.RegisterRuntimePilotUnix(ctx, team, project, pinnedDigest, pinnedSocket, 2, 3*time.Second)
		if err != nil {
			return err
		}
		host := "d-" + id[4:] + previewSuffix
		if err := control.BindFixed(ctx, host, id); err != nil {
			return err
		}
		fmt.Printf("runtime_candidate=%s private_preview=%s\n", id, host)
	case "baseline":
		version, err := control.SwapAlias(ctx, alias, team, project, 0, baseline, false)
		if err != nil {
			return err
		}
		fmt.Printf("runtime_stage_alias=%s baseline=%s generation=%d\n", alias, baseline, version)
	case "promote":
		var pinned string
		if err := db.QueryRow(ctx, `SELECT rt.deployment_id FROM site_runtime_pilots rt JOIN site_deployments d ON d.id=rt.deployment_id
			WHERE rt.deployment_id=$1 AND rt.team_id=$2 AND rt.project_id=$3 AND rt.state='ready'
			AND ((d.bundle_digest=$4 AND rt.upstream_socket=$5) OR (d.bundle_digest=$6 AND rt.upstream_socket=$7))`,
			deployment, team, project, digest, socket, digestV3, socketV3).Scan(&pinned); err != nil {
			return errors.New("runtime candidate not pinned and healthy")
		}
		version, err := control.SwapAlias(ctx, alias, team, project, expected, pinned, false)
		if err != nil {
			return err
		}
		fmt.Printf("runtime_stage_alias=%s runtime=%s generation=%d\n", alias, pinned, version)
	case "rollback":
		version, err := control.SwapAlias(ctx, alias, team, project, expected, baseline, false)
		if err != nil {
			return err
		}
		fmt.Printf("runtime_stage_alias=%s baseline=%s generation=%d\n", alias, baseline, version)
	}
	return nil
}

func main() {
	action := flag.String("action", "", "register, register-v3, register-xianlixiu, baseline, promote or rollback")
	expected := flag.Int64("expected", -1, "expected stage alias generation")
	deployment := flag.String("deployment", "", "registered runtime Deployment ID")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := run(ctx, *action, *expected, *deployment); err != nil {
		log.Fatal("operator R2 loop: ", err)
	}
}
