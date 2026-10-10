package build

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestSignedBuildAttestationBindsTenantSourceAndActualOutput(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	output := []byte("built archive bytes")
	sum := sha256.Sum256(output)
	statement := BuildStatement{
		Version: 1, BuilderID: "approved-builder-1", TeamID: "t_alpha", ProjectID: "p_alpha", DeliveryID: "poll-a",
		JobID: "job_11111111111111111111111111111111", JobNonce: "2222222222222222222222222222222222222222222222222222222222222222",
		CommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SourceSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		OutputSHA256: hex.EncodeToString(sum[:]), ImageDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Runtime: "runsc", CPUMilli: 1000, MemoryMiB: 2048, StorageMiB: 2048, FinishedAt: time.Now().UTC(),
	}
	signed, err := SignBuild(private, statement)
	if err != nil || VerifyBuild(public, signed, output) != nil {
		t.Fatalf("valid signed output rejected: %v", err)
	}
	if VerifyBuild(public, signed, []byte("different output")) == nil {
		t.Fatal("changed built bytes accepted")
	}
	mutated := signed
	mutated.Statement.TeamID = "t_beta"
	if VerifyBuild(public, mutated, output) == nil {
		t.Fatal("cross-Team statement accepted")
	}
	mutated = signed
	mutated.Statement.CommitSHA = "dddddddddddddddddddddddddddddddddddddddd"
	if VerifyBuild(public, mutated, output) == nil {
		t.Fatal("different Git commit accepted")
	}
	mutated = signed
	if signed.Signature[0] == 'A' {
		mutated.Signature = "B" + signed.Signature[1:]
	} else {
		mutated.Signature = "A" + signed.Signature[1:]
	}
	if VerifyBuild(public, mutated, output) == nil {
		t.Fatal("forged signature accepted")
	}
	mutated = signed
	mutated.Statement.Runtime = "runc"
	if VerifyBuild(public, mutated, output) == nil {
		t.Fatal("unisolated runtime admitted")
	}
}
