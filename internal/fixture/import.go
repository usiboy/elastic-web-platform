package fixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"

	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/framework"
	"elastic-web-platform/internal/routing"
	"elastic-web-platform/internal/source/uploads"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Input struct {
	TeamID, ProjectID, Host, SourceSHA256, BuiltSHA256, BuilderRef string
	SourceArchive, BuiltArchive []byte
}

type Prepared struct {
	Bundle *bundle.Static
	Framework string
	SourceSHA256, BuiltSHA256 string
}

type Result struct {
	DeploymentID string `json:"deploymentId"`
	Host string `json:"desiredHost"`
	SourceSHA256 string `json:"sourceSha256"`
	BundleDigest string `json:"bundleDigest"`
	Framework string `json:"framework"`
	Status string `json:"status"`
}

var scopeID=regexp.MustCompile(`^[a-z]+_[a-z0-9]{3,64}$`)
var exactHost=regexp.MustCompile(`^[a-z0-9][a-z0-9.-]+[a-z0-9]$`)
var hexSHA=regexp.MustCompile(`^[0-9a-f]{64}$`)

// Prepare is a bounded validation of operator-supplied bytes. The builderRef
// is an operator declaration, NOT a signed builder provenance or tenant code
// admission certificate.
func Prepare(input Input)(Prepared,error) {
	if !scopeID.MatchString(input.TeamID) || !scopeID.MatchString(input.ProjectID) || !exactHost.MatchString(input.Host) || !strings.Contains(input.Host,".") || strings.Contains(input.Host,"..") || !hexSHA.MatchString(input.SourceSHA256) || !hexSHA.MatchString(input.BuiltSHA256) || !strings.HasPrefix(input.BuilderRef,"operator-fixture:") || len(input.BuilderRef)>200 {
		return Prepared{},errors.New("invalid operator fixture scope")
	}
	source,err:=uploads.Validate(input.SourceArchive)
	if err!=nil || source.Digest!=input.SourceSHA256 { return Prepared{},errors.New("source archive does not match declared bytes") }
	preset,err:=framework.Detect(input.SourceArchive)
	if err!=nil || !preset.RequiresBuild || (preset.Name!="vite-vue" && preset.Name!="vite-react") { return Prepared{},errors.New("source is not a supported Vite fixture") }
	hash:=sha256.Sum256(input.BuiltArchive)
	if hex.EncodeToString(hash[:])!=input.BuiltSHA256 { return Prepared{},errors.New("built output archive does not match declared bytes") }
	b,err:=bundle.CaptureBuiltArchive(input.TeamID,input.ProjectID,input.SourceSHA256,input.BuilderRef,input.BuiltArchive)
	if err!=nil || b.Verify()!=nil { return Prepared{},errors.New("unsafe or invalid static output") }
	return Prepared{Bundle:b,Framework:preset.Name,SourceSHA256:source.Digest,BuiltSHA256:input.BuiltSHA256},nil
}

// ImportTrusted is operator-only and never exposed to tenant HTTP clients. A
// fixed Host cannot be reassigned: re-importing identical bytes is idempotent.
func ImportTrusted(ctx context.Context,db *pgxpool.Pool,store bundle.Store,input Input)(Result,error) {
	prepared,err:=Prepare(input)
	if err!=nil { return Result{},err }
	var existingID,sourceSHA,bundleDigest string
	var fixed,private bool
	err=db.QueryRow(ctx,`SELECT h.deployment_id,d.source_sha256,d.bundle_digest,h.is_fixed,h.is_private
		FROM site_hosts h JOIN site_deployments d ON d.id=h.deployment_id WHERE h.hostname=$1`,input.Host).Scan(&existingID,&sourceSHA,&bundleDigest,&fixed,&private)
	if err==nil {
		if !fixed || !private || sourceSHA!=input.SourceSHA256 || bundleDigest!=prepared.Bundle.Manifest.Digest { return Result{},errors.New("fixed Host is bound to different content") }
		if _,err:=store.Get(ctx,input.TeamID,input.ProjectID,bundleDigest); err!=nil { return Result{},errors.New("existing fixture bytes cannot be verified") }
		return Result{DeploymentID:existingID,Host:input.Host,SourceSHA256:sourceSHA,BundleDigest:bundleDigest,Framework:prepared.Framework,Status:"operator_fixture_candidate"},nil
	}
	if !errors.Is(err,pgx.ErrNoRows) { return Result{},err }
	registry:=routing.NewDurableRegistry(db,store)
	id,err:=registry.RegisterOperatorFixture(ctx,prepared.Bundle)
	if err!=nil { return Result{},err }
	if err:=registry.BindFixed(ctx,input.Host,id); err!=nil { return Result{},err }
	if _,err:=db.Exec(ctx,`INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'operator.fixture_imported',$2)`,input.TeamID,id); err!=nil { return Result{},err }
	return Result{DeploymentID:id,Host:input.Host,SourceSHA256:input.SourceSHA256,BundleDigest:prepared.Bundle.Manifest.Digest,Framework:prepared.Framework,Status:"operator_fixture_candidate"},nil
}
