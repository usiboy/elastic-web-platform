package github

import (
	"context"
	"os"
	"testing"
	"time"

	"elastic-web-platform/internal/framework"
)

func TestLivePublicPilotPinnedLatestAAndBHeads(t *testing.T) {
	if os.Getenv("EWP_LIVE_PUBLIC_GIT_PILOT") != "1" {
		t.Skip("set EWP_LIVE_PUBLIC_GIT_PILOT=1 to read the approved real GitHub test repos")
	}
	client := NewClient(0, nil, "https://api.github.com", nil)
	for _, item := range []struct {
		id     int64
		preset string
	}{
		{1404606559, "vite-vue"},
		{1404606859, "vite-react"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		head, err := client.PublicPilotBranchHead(ctx, item.id, "main")
		if err != nil || !validGitSHA(head) {
			cancel()
			t.Fatalf("pilot branch changed or was unreadable: repo=%d err=%v", item.id, err)
		}
		source, err := client.FetchPublicPilotPinned(ctx, item.id, head)
		cancel()
		if err != nil {
			t.Fatalf("real public Git source unavailable for %d: %v", item.id, err)
		}
		preset, err := framework.Detect(source.Archive)
		if err != nil || preset.Name != item.preset || !preset.RequiresBuild {
			t.Fatalf("real Git source changed framework: repo=%d %+v %v", item.id, preset, err)
		}
		t.Logf("repo=%d commit=%s source_sha256=%s preset=%s", item.id, head, source.ArchiveSHA256, preset.Name)
	}
}
