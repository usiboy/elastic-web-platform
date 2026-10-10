package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"elastic-web-platform/db/migrations"
	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/fixture"

	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct {
	databaseURL,artifactDir,teamID,projectID,host,sourceFile,builtFile,sourceSHA,builtSHA,builderRef string
}

var idPattern=regexp.MustCompile(`^[a-z]+_[a-z0-9]{3,64}$`)
var hostPattern=regexp.MustCompile(`^[a-z0-9][a-z0-9.-]+[a-z0-9]$`)
var digestPattern=regexp.MustCompile(`^[0-9a-f]{64}$`)

func validateConfig(c config)error {
	if c.databaseURL=="" || c.artifactDir=="" || c.sourceFile=="" || c.builtFile=="" || !idPattern.MatchString(c.teamID) || !idPattern.MatchString(c.projectID) || !hostPattern.MatchString(c.host) || strings.Contains(c.host,"..") || !digestPattern.MatchString(c.sourceSHA) || !digestPattern.MatchString(c.builtSHA) || !strings.HasPrefix(c.builderRef,"operator-fixture:") {
		return errors.New("operator fixture requires existing Team/Project, exact Host and both actual archive digests")
	}
	return nil
}

func protectedArchive(file string,max int64)([]byte,error) {
	info,err:=os.Lstat(file)
	if err!=nil { return nil,err }
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077!=0 { return nil,errors.New("archive must be a private regular file") }
	f,err:=os.Open(file)
	if err!=nil { return nil,err }
	defer f.Close()
	data,err:=io.ReadAll(io.LimitReader(f,max+1))
	if err!=nil { return nil,err }
	if len(data)==0 || int64(len(data))>max { return nil,errors.New("operator archive exceeds declared budget") }
	return data,nil
}

func main() {
	var c config
	flag.StringVar(&c.teamID,"team-id","","existing Team ID")
	flag.StringVar(&c.projectID,"project-id","","existing Project ID")
	flag.StringVar(&c.host,"host","","exact fixed preview Host")
	flag.StringVar(&c.sourceFile,"source-file","","validated Git source tar.gz")
	flag.StringVar(&c.sourceSHA,"source-sha256","","actual source archive SHA-256")
	flag.StringVar(&c.builtFile,"built-file","","operator-reviewed static build tar.gz")
	flag.StringVar(&c.builtSHA,"built-sha256","","actual static build tar SHA-256")
	flag.StringVar(&c.builderRef,"builder-ref","","operator-fixture:... (NOT signed provenance)")
	flag.Parse()
	c.databaseURL=os.Getenv("DATABASE_URL")
	c.artifactDir=os.Getenv("ARTIFACT_STORE_DIR")
	if err:=validateConfig(c); err!=nil { log.Fatal(err) }
	info,err:=os.Lstat(c.artifactDir)
	if err!=nil || !info.IsDir() || info.Mode().Perm()&0077!=0 { log.Fatal("artifact root must be an existing private directory") }
	source,err:=protectedArchive(c.sourceFile,1<<20)
	if err!=nil { log.Fatal("source archive rejected: ",err) }
	built,err:=protectedArchive(c.builtFile,16<<20)
	if err!=nil { log.Fatal("build output rejected: ",err) }
	ctx,cancel:=context.WithTimeout(context.Background(),20*time.Second)
	defer cancel()
	db,err:=pgxpool.New(ctx,c.databaseURL)
	if err!=nil { log.Fatal("database unavailable: ",err) }
	defer db.Close()
	if err:=migrations.Apply(ctx,db); err!=nil { log.Fatal("migration failed: ",err) }
	result,err:=fixture.ImportTrusted(ctx,db,bundle.NewDiskStore(c.artifactDir),fixture.Input{TeamID:c.teamID,ProjectID:c.projectID,Host:c.host,SourceSHA256:c.sourceSHA,BuiltSHA256:c.builtSHA,BuilderRef:c.builderRef,SourceArchive:source,BuiltArchive:built})
	if err!=nil { log.Fatal("fixture import rejected: ",err) }
	if err:=json.NewEncoder(os.Stdout).Encode(result); err!=nil { log.Fatal("output failed: ",err) }
}
