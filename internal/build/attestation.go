package build

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

const attestationDomain = "ewp-builder-attestation:v1\x00"

var (
	buildScopeID   = regexp.MustCompile(`^[a-z]+_[a-z0-9]{3,64}$`)
	buildBuilderID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`)
	buildDelivery  = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
	buildCommit    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	buildDigest    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	buildJobID     = regexp.MustCompile(`^job_[0-9a-f]{32}$`)
)

type JobPlan struct {
	BuilderID   string `json:"builderId"`
	ImageDigest string `json:"imageDigest"`
	CPUMilli    int64  `json:"cpuMilli"`
	MemoryMiB   int64  `json:"memoryMiB"`
	StorageMiB  int64  `json:"storageMiB"`
}

func (plan JobPlan) Valid() bool {
	return buildBuilderID.MatchString(plan.BuilderID) && buildDigest.MatchString(plan.ImageDigest) &&
		plan.CPUMilli >= 100 && plan.CPUMilli <= 100000 && plan.MemoryMiB >= 128 && plan.MemoryMiB <= 1048576 &&
		plan.StorageMiB >= 128 && plan.StorageMiB <= 1048576
}

// BuildStatement binds a separate Builder's claim to a fixed Git source and
// the actual output archive. Runtime and limits are claims, not independent
// proof of kernel-enforced isolation; only an approved Builder may hold the
// private signing key. No tenant-facing API can supply the trust anchor.
type BuildStatement struct {
	Version      int       `json:"version"`
	BuilderID    string    `json:"builderId"`
	TeamID       string    `json:"teamId"`
	ProjectID    string    `json:"projectId"`
	DeliveryID   string    `json:"deliveryId"`
	JobID        string    `json:"jobId"`
	JobNonce     string    `json:"jobNonce"`
	CommitSHA    string    `json:"commitSha"`
	SourceSHA256 string    `json:"sourceSha256"`
	OutputSHA256 string    `json:"outputSha256"`
	ImageDigest  string    `json:"imageDigest"`
	Runtime      string    `json:"runtime"`
	CPUMilli     int64     `json:"cpuMilli"`
	MemoryMiB    int64     `json:"memoryMiB"`
	StorageMiB   int64     `json:"storageMiB"`
	FinishedAt   time.Time `json:"finishedAt"`
}

type SignedBuild struct {
	Statement BuildStatement `json:"statement"`
	Signature string         `json:"signature"`
}

func validStatement(item BuildStatement) bool {
	return item.Version == 1 && (JobPlan{BuilderID: item.BuilderID, ImageDigest: item.ImageDigest, CPUMilli: item.CPUMilli, MemoryMiB: item.MemoryMiB, StorageMiB: item.StorageMiB}).Valid() &&
		buildScopeID.MatchString(item.TeamID) && buildScopeID.MatchString(item.ProjectID) && buildDelivery.MatchString(item.DeliveryID) &&
		buildJobID.MatchString(item.JobID) && buildDigest.MatchString(item.JobNonce) &&
		buildCommit.MatchString(item.CommitSHA) && buildDigest.MatchString(item.SourceSHA256) && buildDigest.MatchString(item.OutputSHA256) &&
		item.Runtime == "runsc" && !item.FinishedAt.IsZero()
}

func statementMessage(statement BuildStatement) []byte {
	data, _ := json.Marshal(statement)
	return append([]byte(attestationDomain), data...)
}

// SignBuild is intended for an approved isolated Builder. Never load or ship
// this private key in the control API, repository or tenant workload.
func SignBuild(private ed25519.PrivateKey, statement BuildStatement) (SignedBuild, error) {
	if len(private) != ed25519.PrivateKeySize || !validStatement(statement) {
		return SignedBuild{}, errors.New("invalid build signing input")
	}
	signature := ed25519.Sign(private, statementMessage(statement))
	return SignedBuild{Statement: statement, Signature: base64.RawStdEncoding.EncodeToString(signature)}, nil
}

func VerifyBuild(public ed25519.PublicKey, signed SignedBuild, archive []byte) error {
	if len(public) != ed25519.PublicKeySize || !validStatement(signed.Statement) || len(archive) == 0 || len(archive) > 16<<20 {
		return errors.New("invalid build attestation or output")
	}
	signature, err := base64.RawStdEncoding.DecodeString(signed.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(public, statementMessage(signed.Statement), signature) {
		return errors.New("build signature invalid")
	}
	digest := sha256.Sum256(archive)
	if hex.EncodeToString(digest[:]) != signed.Statement.OutputSHA256 {
		return errors.New("build output digest mismatch")
	}
	return nil
}
