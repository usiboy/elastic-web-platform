package integration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestLiveMeasuredProjectActivityAndTenantIsolation(t *testing.T) {
	ip := os.Getenv("EWP_LIVE_ECS_IP")
	if ip == "" {
		t.Skip("set the explicit ECS IP and protected A/B Owner identities")
	}
	if ip != "114.55.237.62" {
		t.Fatal("unexpected test ECS destination")
	}
	ca, err := os.ReadFile(os.Getenv("EWP_LIVE_ECS_CA_FILE"))
	if err != nil {
		t.Fatal("test CA unavailable")
	}
	trust := x509.NewCertPool()
	if !trust.AppendCertsFromPEM(ca) {
		t.Fatal("test CA invalid")
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil,
		TLSClientConfig: &tls.Config{RootCAs: trust, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip, "443"))
		}}}
	teamA, projectA := os.Getenv("EWP_LIVE_ECS_TEAM_A"), os.Getenv("EWP_LIVE_ECS_PROJECT_A")
	teamB, projectB := os.Getenv("EWP_LIVE_ECS_TEAM_B"), os.Getenv("EWP_LIVE_ECS_PROJECT_B")
	if teamA == "" || teamB == "" || projectA == "" || projectB == "" {
		t.Fatal("A/B Team and Project identities required")
	}
	base := "https://console.ecp.lingtong.com/v1/teams/"
	read := func(path, token string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return response, body
	}
	for _, entry := range []struct{ team, project, owner, otherTeam, otherProject string }{
		{teamA, projectA, os.Getenv("EWP_LIVE_ECS_OWNER_A_FILE"), teamB, projectB},
		{teamB, projectB, os.Getenv("EWP_LIVE_ECS_OWNER_B_FILE"), teamA, projectA},
	} {
		token := protectedToken(t, entry.owner)
		root := base + entry.team + "/projects/" + entry.project
		response, body := read(root+"/usage", token)
		var usage struct {
			VerifiedSources    int64  `json:"verifiedSources"`
			SourceArchiveBytes int64  `json:"sourceArchiveBytes"`
			AcceptedBuilds     int64  `json:"acceptedBuilds"`
			CPUTimeMS          *int64 `json:"cpuTimeMs"`
		}
		if response.StatusCode != 200 || json.Unmarshal(body, &usage) != nil || usage.VerifiedSources <= 0 || usage.SourceArchiveBytes <= 0 || usage.CPUTimeMS != nil {
			t.Fatalf("measured project data invalid for %s (HTTP %d)", entry.team, response.StatusCode)
		}
		response, body = read(root+"/activity", token)
		var history struct {
			Events []struct {
				Action string `json:"action"`
			} `json:"events"`
		}
		if response.StatusCode != 200 || json.Unmarshal(body, &history) != nil || len(history.Events) == 0 {
			t.Fatalf("project events unavailable for %s (HTTP %d)", entry.team, response.StatusCode)
		}
		response, body = read(root+"/github-pushes", token)
		var pushes struct {
			Pushes []struct {
				Status          string    `json:"status"`
				BuildStatus     string    `json:"buildStatus"`
				SourceBytes     int64     `json:"sourceBytes"`
				BuildAcceptedAt time.Time `json:"buildAcceptedAt"`
			} `json:"pushes"`
		}
		if response.StatusCode != 200 || json.Unmarshal(body, &pushes) != nil || len(pushes.Pushes) == 0 {
			t.Fatalf("signed build facts unavailable for %s (HTTP %d)", entry.team, response.StatusCode)
		}
		var observedBytes, accepted int64
		for _, push := range pushes.Pushes {
			observedBytes += push.SourceBytes
			if push.BuildStatus == "completed" && !push.BuildAcceptedAt.IsZero() {
				accepted++
			}
		}
		if observedBytes != usage.SourceArchiveBytes || accepted != usage.AcceptedBuilds {
			t.Fatalf("measured source/build totals differ for %s: bytes=%d/%d accepted=%d/%d", entry.team,
				observedBytes, usage.SourceArchiveBytes, accepted, usage.AcceptedBuilds)
		}
		for _, suffix := range []string{"usage", "activity"} {
			response, _ = read(base+entry.otherTeam+"/projects/"+entry.otherProject+"/"+suffix, token)
			if response.StatusCode != 404 {
				t.Fatalf("cross-Team %s was exposed to %s: HTTP %d", suffix, entry.team, response.StatusCode)
			}
		}
		t.Logf("team=%s verified_sources=%d source_bytes=%d accepted_builds=%d audit_events=%d actual_cpu=unmeasured",
			entry.team, usage.VerifiedSources, usage.SourceArchiveBytes, usage.AcceptedBuilds, len(history.Events))
	}
}
