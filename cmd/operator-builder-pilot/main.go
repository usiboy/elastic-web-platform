// operator-builder-pilot 仅编排经授权的两个固定 Git 测试仓库的离线 Builder 试点。
// issue/accept 不加载签名私钥；sign 不连接 DB，私钥仅从受限 Builder 目录读取。
// 同机 root 操作者仍是信任边界，不把该声明冒充独立节点签名自动准入。
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"elastic-web-platform/db/migrations"
	"elastic-web-platform/internal/build"
	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/pipeline"
	"elastic-web-platform/internal/source/uploads"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	root        = "/data/ewp-builder-20261007"
	imageDigest = "e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3"
	builderID   = "ecs-samehost-netns-20261007"
)

type fixedProject struct {
	name, team, project, commit, source, output, html, css, js, cssPath, jsPath string
	plan                                                                        build.JobPlan
}

func projectFor(name string) (fixedProject, error) {
	base := build.JobPlan{BuilderID: builderID, ImageDigest: imageDigest, StorageMiB: 2048}
	switch name {
	case "testA":
		base.CPUMilli, base.MemoryMiB = 1000, 2048
		return fixedProject{name, "t_f9cf963718a72612c48940af8a8296e4", "p_34401f5c05af1079010cb95ee3ec1275",
			"e6062c7469d13712515dbf19474c851c044b753f", "e705152a12b9cd06d3881c97240b53fb345267e4aa7ea9a0ed531a63a59a3b8d",
			root + "/docker/volumes/ewp-builder-a-output-bounded-20261007/_data/bundle.tar.gz",
			"e1026657cf7807381ea12f21325a8638decf0b1989eb42d4e549c3fb98a32ff6", "e1e8f6dd498f7017c7033bf78947a1fa907ede06cedbe10de3de842cf0be1531",
			"78d59d5fb71dc694944b2f47c49b2190cb4b2a5116827295ccce49093dd5272a", "/assets/index-NsIWs94z.css", "/assets/index-DXuZjoRV.js", base}, nil
	case "testB":
		base.CPUMilli, base.MemoryMiB = 2000, 3072
		return fixedProject{name, "t_eee8046ca49c271f5bea06acf285be66", "p_e5123bae12388454bb82fc1590750ab9",
			"cfaa2f283f5ced67b85899919fe10a4a9f99d014", "4ba10abcb742a445aea1d55b622fac1e4a0fcf4b7eee6928a3789fc819bde9c3",
			root + "/docker/volumes/ewp-builder-b-output-bounded-20261007/_data/bundle.tar.gz",
			"b0bf964b1808620fe848549c939140411499dc472e7540ebe5e0f4a278d0348a", "cee9771c708e34a72a0e3dde37cb41e83b0641928604a8f0151d59fbfc6f4e3c",
			"0d4f51df202ba1f460cfc674d5c3212ef7856cc9f1c7a3eb5f4e6c7a38f5f07b", "/assets/index-DMyhoa2j.css", "/assets/index-DRp-iAjd.js", base}, nil
	default:
		return fixedProject{}, errors.New("only the two approved fixed Git projects are supported")
	}
}

type jobEnvelope struct {
	TeamID     string            `json:"teamId"`
	ProjectID  string            `json:"projectId"`
	DeliveryID string            `json:"deliveryId"`
	Job        pipeline.BuildJob `json:"job"`
}

func privateFile(name string, limit int64) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() <= 0 || info.Size() > limit {
		return nil, errors.New("expected bounded private regular file")
	}
	data, err := os.ReadFile(name)
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("private file unavailable")
	}
	return data, nil
}

func privateJSON(name string, value any) error {
	data, err := privateFile(name, 8192)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("private job document malformed")
	}
	return nil
}

func writePrivateJSON(name string, value any) error {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("operator job output already exists or unavailable")
	}
	if err := json.NewEncoder(f).Encode(value); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// 签名时独立核对输出卷的真实宿主挂载；仅靠卷名称或声明的 StorageMiB 不证明硬限。
func boundedOutputMount(mountinfo []byte, path string) bool {
	for _, line := range strings.Split(string(mountinfo), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[4] != path {
			continue
		}
		separator := -1
		for i, field := range fields {
			if field == "-" {
				separator = i
				break
			}
		}
		if separator < 0 || len(fields) <= separator+3 || fields[separator+1] != "tmpfs" {
			return false
		}
		mountOptions := "," + fields[5] + ","
		for _, option := range []string{"rw", "nosuid", "nodev", "noexec"} {
			if !strings.Contains(mountOptions, ","+option+",") {
				return false
			}
		}
		superOptions := "," + fields[separator+3] + ","
		return strings.Contains(superOptions, ",size=65536k,") && strings.Contains(superOptions, ",uid=1000,") && strings.Contains(superOptions, ",gid=1000,")
	}
	return false
}

func verifiedOutput(project fixedProject, sourceSHA string) ([]byte, string, time.Time, error) {
	mountinfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil || !boundedOutputMount(mountinfo, filepath.Dir(project.output)) {
		return nil, "", time.Time{}, errors.New("bounded Builder output mount unavailable")
	}
	info, err := os.Lstat(project.output)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 16<<20 {
		return nil, "", time.Time{}, errors.New("bounded Builder archive unavailable")
	}
	data, err := os.ReadFile(project.output)
	if err != nil || len(data) > 16<<20 {
		return nil, "", time.Time{}, errors.New("Builder output read failed")
	}
	preview, err := bundle.CaptureBuiltArchive(project.team, project.project, sourceSHA, "operator-pilot:validate-only", data)
	if err != nil {
		return nil, "", time.Time{}, errors.New("unsafe Builder output")
	}
	for name, expected := range map[string]string{"/index.html": project.html, project.cssPath: project.css, project.jsPath: project.js} {
		content, ok := preview.Read(name)
		sha := sha256.Sum256(content)
		if !ok || hex.EncodeToString(sha[:]) != expected {
			return nil, "", time.Time{}, errors.New("Builder output differs from pinned CI bytes")
		}
	}
	hash := sha256.Sum256(data)
	return data, hex.EncodeToString(hash[:]), info.ModTime(), nil
}

func processorFor(ctx context.Context) (*pipeline.Processor, *pgxpool.Pool, error) {
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("SOURCE_STORE_DIR") == "" || os.Getenv("ARTIFACT_STORE_DIR") == "" || os.Getenv("SITE_PREVIEW_SUFFIX") == "" {
		return nil, nil, errors.New("protected new-platform DB/source/artifact/Preview configuration required")
	}
	db, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return nil, nil, err
	}
	if err := migrations.Apply(ctx, db); err != nil {
		db.Close()
		return nil, nil, err
	}
	p := pipeline.NewProcessor(db, uploads.NewDiskStore(os.Getenv("SOURCE_STORE_DIR")), bundle.NewDiskStore(os.Getenv("ARTIFACT_STORE_DIR")), nil, os.Getenv("SITE_PREVIEW_SUFFIX"))
	return p, db, nil
}

func run(ctx context.Context, stage string, project fixedProject, failureCode string) error {
	jobFile := filepath.Join(root, "state", "job-"+project.name+".json")
	signedFile := filepath.Join(root, "state", "signed-"+project.name+".json")
	if stage == "issue" {
		if _, err := os.Lstat(jobFile); !errors.Is(err, os.ErrNotExist) {
			return errors.New("job file exists; refusing duplicate issuance")
		}
		p, db, err := processorFor(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		var delivery, commit, sourceSHA, status string
		err = db.QueryRow(ctx, `SELECT p.delivery_id,p.commit_sha,s.archive_sha256,p.status FROM github_pushes p JOIN github_verified_sources s ON s.delivery_id=p.delivery_id AND s.team_id=p.team_id AND s.project_id=p.project_id
			WHERE p.team_id=$1 AND p.project_id=$2`, project.team, project.project).Scan(&delivery, &commit, &sourceSHA, &status)
		if err != nil || commit != project.commit || sourceSHA != project.source || status != "awaiting_isolated_build" {
			return errors.New("fixed, verified Git source is not awaiting build")
		}
		job, err := p.IssueBuildJob(ctx, project.team, project.project, delivery, project.plan, 0)
		if err != nil {
			return err
		}
		if err := writePrivateJSON(jobFile, jobEnvelope{TeamID: project.team, ProjectID: project.project, DeliveryID: delivery, Job: job}); err != nil {
			return err
		}
		fmt.Printf("builder_job_issued=%s expires_at=%s\n", project.name, job.ExpiresAt.UTC().Format(time.RFC3339))
		return nil
	}
	var order jobEnvelope
	if err := privateJSON(jobFile, &order); err != nil {
		return err
	}
	if order.TeamID != project.team || order.ProjectID != project.project || order.Job.CommitSHA != project.commit || order.Job.SourceSHA256 != project.source || order.Job.Plan != project.plan || order.DeliveryID == "" || time.Now().After(order.Job.ExpiresAt) {
		return errors.New("operator work order no longer matches fixed Git source")
	}
	if stage == "fail" {
		// Failure reports require the exact one-time job and never load the
		// signing key, a build archive or an untrusted container log.
		p, db, err := processorFor(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		changed, err := p.FailBuildJob(ctx, project.team, project.project, order.DeliveryID, order.Job.ID, order.Job.Nonce, failureCode)
		if err != nil {
			return err
		}
		fmt.Printf("builder_job_failed=%s changed=%t code=%s\n", project.name, changed, failureCode)
		return nil
	}
	output, digest, builtAt, err := verifiedOutput(project, order.Job.SourceSHA256)
	if err != nil {
		return err
	}
	if builtAt.Before(order.Job.IssuedAt) || builtAt.After(order.Job.ExpiresAt) {
		return errors.New("Builder archive was not written during the issued job")
	}
	if stage == "sign" {
		keyPEM, err := privateFile(filepath.Join(root, "private", "builder-signer.pem"), 4096)
		if err != nil {
			return errors.New("protected Builder signing key unavailable")
		}
		block, _ := pem.Decode(keyPEM)
		if block == nil {
			return errors.New("invalid Builder signing key")
		}
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		private, ok := parsed.(ed25519.PrivateKey)
		if err != nil || !ok {
			return errors.New("Builder key is not Ed25519")
		}
		statement := build.BuildStatement{Version: 1, BuilderID: builderID, TeamID: project.team, ProjectID: project.project, DeliveryID: order.DeliveryID, JobID: order.Job.ID, JobNonce: order.Job.Nonce,
			CommitSHA: project.commit, SourceSHA256: project.source, OutputSHA256: digest, ImageDigest: imageDigest, Runtime: "runsc", CPUMilli: project.plan.CPUMilli, MemoryMiB: project.plan.MemoryMiB, StorageMiB: project.plan.StorageMiB, FinishedAt: time.Now().UTC()}
		if statement.FinishedAt.After(order.Job.ExpiresAt) {
			return errors.New("build job expired before signing")
		}
		signed, err := build.SignBuild(private, statement)
		if err != nil {
			return err
		}
		if err := writePrivateJSON(signedFile, signed); err != nil {
			return err
		}
		fmt.Printf("builder_output_signed=%s archive_sha256=%s\n", project.name, digest)
		return nil
	}
	if stage != "accept" {
		return errors.New("only issue/sign/accept/fail stages are allowed")
	}
	pubPEM, err := privateFile("/data/ewp-preview-20261006/private/builder-pilot-public.pem", 4096)
	if err != nil {
		return errors.New("control-plane Builder trust anchor unavailable")
	}
	block, _ := pem.Decode(pubPEM)
	if block == nil {
		return errors.New("invalid Builder trust anchor")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	public, ok := parsed.(ed25519.PublicKey)
	if err != nil || !ok {
		return errors.New("Builder trust anchor is not Ed25519")
	}
	var signed build.SignedBuild
	if err := privateJSON(signedFile, &signed); err != nil {
		return err
	}
	if signed.Statement.TeamID != project.team || signed.Statement.ProjectID != project.project || signed.Statement.DeliveryID != order.DeliveryID || signed.Statement.JobID != order.Job.ID || signed.Statement.OutputSHA256 != digest {
		return errors.New("signed output differs from current fixed job")
	}
	p, db, err := processorFor(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	result, err := p.AcceptSignedBuild(ctx, signed, output, public)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func main() {
	stage := flag.String("stage", "", "issue, sign, accept or fail")
	name := flag.String("project", "", "testA or testB")
	failureCode := flag.String("failure-code", "", "allowlisted failure code when reporting a failed build")
	flag.Parse()
	project, err := projectFor(*name)
	if err != nil {
		log.Fatal(err)
	}
	if *stage != "issue" && *stage != "sign" && *stage != "accept" && *stage != "fail" {
		log.Fatal("invalid operator Builder pilot stage")
	}
	if (*stage != "fail" && *failureCode != "") || (*stage == "fail" && *failureCode == "") {
		log.Fatal("failure-code required only for fail stage")
	}
	ctx, done := context.WithTimeout(context.Background(), 30*time.Second)
	defer done()
	if err := run(ctx, *stage, project, *failureCode); err != nil {
		log.Fatal("operator Builder pilot rejected: ", err)
	}
}
