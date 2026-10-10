package fixture

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"elastic-web-platform/db/migrations"
	"elastic-web-platform/internal/bundle"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testArchive(t *testing.T,files map[string]string)[]byte {
	t.Helper()
	var out bytes.Buffer
	gz:=gzip.NewWriter(&out)
	tw:=tar.NewWriter(gz)
	for name,body:=range files {
		if err:=tw.WriteHeader(&tar.Header{Name:name,Mode:0644,Size:int64(len(body))}); err!=nil { t.Fatal(err) }
		if _,err:=tw.Write([]byte(body)); err!=nil { t.Fatal(err) }
	}
	if err:=tw.Close(); err!=nil { t.Fatal(err) }
	if err:=gz.Close(); err!=nil { t.Fatal(err) }
	return out.Bytes()
}

func hashBytes(data []byte)string { h:=sha256.Sum256(data); return hex.EncodeToString(h[:]) }

func TestPrepareTrustedFixtureChecksBothActualArchiveHashes(t *testing.T) {
	source:=testArchive(t,map[string]string{
		"index.html":"<div id=app></div>",
		"package.json":`{"scripts":{"build":"vite build"},"dependencies":{"vue":"3.5.18","vite":"7.1.2"}}`,
		"package-lock.json":`{"lockfileVersion":3,"packages":{}}`,
	})
	built:=testArchive(t,map[string]string{"./index.html":"<script src=\"/assets/site.js\"></script>","./assets/site.js":"console.log(42)"})
	input:=Input{TeamID:"t_alpha",ProjectID:"p_alpha",Host:"a.preview.ecp.lingtong.com",SourceSHA256:hashBytes(source),BuiltSHA256:hashBytes(built),BuilderRef:"operator-fixture:node22-vite7",SourceArchive:source,BuiltArchive:built}
	prepared,err:=Prepare(input)
	if err!=nil || prepared.Framework!="vite-vue" || prepared.Bundle.Manifest.SourceSHA256!=input.SourceSHA256 || prepared.Bundle.Verify()!=nil { t.Fatalf("operator fixture: %+v %v",prepared,err) }
	for _,mutate:=range []func(*Input){
		func(i *Input){i.SourceSHA256="other"},
		func(i *Input){i.BuiltSHA256="other"},
		func(i *Input){i.BuilderRef="unsigned-provenance"},
		func(i *Input){i.Host="a.preview.ecp.lingtong.com/../other"},
	} {
		copy:=input; mutate(&copy)
		if _,err:=Prepare(copy); err==nil { t.Fatalf("forged fixture accepted: %+v",copy) }
	}
}

func fixtureDatabase(t *testing.T)*pgxpool.Pool {
	t.Helper()
	url:=os.Getenv("TEST_DATABASE_URL")
	if url=="" { t.Skip("set TEST_DATABASE_URL for independent PostgreSQL fixture check") }
	ctx:=context.Background()
	admin,err:=pgxpool.New(ctx,url); if err!=nil { t.Fatal(err) }
	t.Cleanup(admin.Close)
	schema:=fmt.Sprintf("fixture_test_%d",time.Now().UnixNano())
	if _,err:=admin.Exec(ctx,"CREATE SCHEMA "+schema); err!=nil { t.Fatal(err) }
	t.Cleanup(func(){ if _,err:=admin.Exec(context.Background(),"DROP SCHEMA "+schema+" CASCADE"); err!=nil { t.Error(err) } })
	cfg,err:=pgxpool.ParseConfig(url); if err!=nil { t.Fatal(err) }
	cfg.ConnConfig.RuntimeParams["search_path"]=schema
	db,err:=pgxpool.NewWithConfig(ctx,cfg); if err!=nil { t.Fatal(err) }
	t.Cleanup(db.Close)
	if err:=migrations.Apply(ctx,db); err!=nil { t.Fatal(err) }
	if _,err:=db.Exec(ctx,`INSERT INTO teams(id,slug,name) VALUES('t_alpha','alpha','Alpha'),('t_beta','beta','Beta'); INSERT INTO projects(id,team_id,slug,name) VALUES('p_alpha','t_alpha','site','Site A'),('p_beta','t_beta','site','Site B')`); err!=nil { t.Fatal(err) }
	return db
}

func TestImportRealTrustedABFixturesKeepsHostAndBytesImmutable(t *testing.T) {
	root:=os.Getenv("EWP_LIVE_GITHUB_OUTPUT_DIR")
	if root=="" { t.Skip("set EWP_LIVE_GITHUB_OUTPUT_DIR to approved private A/B output") }
	db:=fixtureDatabase(t)
	store:=bundle.NewDiskStore(t.TempDir())
	ctx:=context.Background()
	inputs:=[]Input{
		{TeamID:"t_alpha",ProjectID:"p_alpha",Host:"a.preview.ecp.lingtong.com",SourceSHA256:"06eb9696ea468930721f5b4f3eff2d90819ef3553385ca2a0a342b4585c0eaa7",BuiltSHA256:"95b606560d3d47f90304dde1e830fe4e102bac5ebc022a3dd971a06ce0461dfd",BuilderRef:"operator-fixture:node22-vite7"},
		{TeamID:"t_beta",ProjectID:"p_beta",Host:"b.preview.ecp.lingtong.com",SourceSHA256:"044e7ed94cdcb02c1b1ff6e6654a12ba8e89d46d9eda7e55ce7eaf5cd30d906a",BuiltSHA256:"92d909ef69e684f09fcdd82ed88468e7181ce74a2fec5c69547db1818effe712",BuilderRef:"operator-fixture:node22-vite7-no-treeshake"},
	}
	for i:=range inputs {
		var err error
		input:=&inputs[i]
		sourceFile,builtFile:="",""
		if i==0 { sourceFile="repo-1404606559-b7dc40fd3655.tar.gz"; builtFile="testA-built-b7dc40fd3655.tar.gz" } else { sourceFile="repo-1404606859-9ea1fc0c7037.tar.gz"; builtFile="testB-built-no-treeshake-9ea1fc0c7037.tar.gz" }
		input.SourceArchive,err=os.ReadFile(filepath.Join(root,sourceFile)); if err!=nil { t.Fatal(err) }
		input.BuiltArchive,err=os.ReadFile(filepath.Join(root,builtFile)); if err!=nil { t.Fatal(err) }
		first,err:=ImportTrusted(ctx,db,store,*input)
		if err!=nil || first.Status!="operator_fixture_candidate" || first.DeploymentID=="" { t.Fatalf("fixture %d: %+v %v",i,first,err) }
		again,err:=ImportTrusted(ctx,db,store,*input)
		if err!=nil || again.DeploymentID!=first.DeploymentID { t.Fatalf("fixture replay %d: %+v %v",i,again,err) }
		t.Logf("repo=%d host=%s deployment=%s bundleDigest=%s",i,input.Host,first.DeploymentID,first.BundleDigest)
	}
	copy:=inputs[1]
	copy.Host=inputs[0].Host
	if _,err:=ImportTrusted(ctx,db,store,copy); err==nil { t.Fatal("tenant B took tenant A fixed preview Host") }
	var count int
	if err:=db.QueryRow(ctx,`SELECT count(*) FROM site_deployments`).Scan(&count); err!=nil || count!=2 { t.Fatalf("fixture DB count: %d %v",count,err) }
}
