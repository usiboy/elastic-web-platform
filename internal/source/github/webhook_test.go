package github

import "testing"

func TestVerifySignatureMatchesGitHubReference(t *testing.T) {
	const signature = "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	if !VerifySignature("It's a Secret to Everybody", []byte("Hello, World!"), signature) {
		t.Fatal("official SHA-256 HMAC vector rejected")
	}
	for _, invalid := range []string{"", "sha1=" + signature[7:], "sha256=" + signature[7:len(signature)-1], "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e18"} {
		if VerifySignature("It's a Secret to Everybody", []byte("Hello, World!"), invalid) {
			t.Fatalf("accepted invalid signature %q", invalid)
		}
	}
	if VerifySignature("It's a Secret to Everybody", []byte("Hello, World?"), signature) {
		t.Fatal("tampered payload accepted")
	}
}
