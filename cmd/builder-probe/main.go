// builder-probe performs a read-only Docker runtime check. It does not launch
// tenant code or grant the current machine Builder admission.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"elastic-web-platform/internal/build"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	runtimes, err := build.ProbeDocker(ctx)
	if err == nil {
		err = build.RequireRunsc(runtimes)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "builder_runtime=blocked reason=isolation_unavailable")
		os.Exit(1)
	}
	fmt.Println("builder_runtime=detected builder_admission=pending_node_and_artifact_verification")
}
