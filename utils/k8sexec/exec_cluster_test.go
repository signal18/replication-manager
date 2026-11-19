package k8sexec

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd"
)

// TestRunAgainstRealCluster runs the helper against a real API server. It is skipped unless
// K8SEXEC_KUBECONFIG, K8SEXEC_NAMESPACE, K8SEXEC_POD and K8SEXEC_CONTAINER name a running container of a
// disposable workload; it runs only harmless commands (echo, a failing sh, sleep) and changes nothing.
func TestRunAgainstRealCluster(t *testing.T) {
	kc, ns, pod, ctr := os.Getenv("K8SEXEC_KUBECONFIG"), os.Getenv("K8SEXEC_NAMESPACE"), os.Getenv("K8SEXEC_POD"), os.Getenv("K8SEXEC_CONTAINER")
	if kc == "" || ns == "" || pod == "" || ctr == "" {
		t.Skip("K8SEXEC_KUBECONFIG, K8SEXEC_NAMESPACE, K8SEXEC_POD and K8SEXEC_CONTAINER are not set")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kc)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Namespace: ns, Pod: pod, Container: ctr}
	ctx := context.Background()

	res, err := Run(ctx, cfg, target, []string{"sh", "-c", "echo out; echo err >&2"}, Options{})
	if err != nil || strings.TrimSpace(res.Stdout) != "out" || strings.TrimSpace(res.Stderr) != "err" {
		t.Fatalf("stdout/stderr: %+v %v", res, err)
	}
	res, err = Run(ctx, cfg, target, []string{"sh", "-c", "echo partial; exit 7"}, Options{})
	if KindOf(err) != KindExit || res.ExitCode != 7 || strings.TrimSpace(res.Stdout) != "partial" {
		t.Fatalf("exit status: %+v %v", res, err)
	}
	if _, err = Run(ctx, cfg, Target{Namespace: ns, Pod: pod, Container: "nosuchcontainer"}, []string{"true"}, Options{}); KindOf(err) != KindUnavailable {
		t.Fatalf("a missing container: kind %q: %v", KindOf(err), err)
	}
	if _, err = Run(ctx, cfg, Target{Namespace: ns, Pod: "nosuchpod", Container: ctr}, []string{"true"}, Options{}); KindOf(err) != KindNotFound {
		t.Fatalf("a missing Pod: kind %q: %v", KindOf(err), err)
	}
	start := time.Now()
	if _, err = Run(ctx, cfg, target, []string{"sleep", "30"}, Options{Timeout: 2 * time.Second}); KindOf(err) != KindTimeout || time.Since(start) > 10*time.Second {
		t.Fatalf("timeout: kind %q after %s: %v", KindOf(err), time.Since(start), err)
	}
	res, err = Run(ctx, cfg, target, []string{"sh", "-c", "i=0; while [ $i -lt 4000 ]; do echo 0123456789012345678901234567890123456789; i=$((i+1)); done"}, Options{MaxOutput: 10000})
	if err != nil || !res.StdoutTruncated || len(res.Stdout) != 10000 {
		t.Fatalf("bounded output: %d bytes truncated=%v %v", len(res.Stdout), res.StdoutTruncated, err)
	}
}
