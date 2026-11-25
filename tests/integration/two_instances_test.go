package integration

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Two server processes must enforce one Redis bucket for the same client.
// The test is skipped unless REDIS_ADDR is set:
//
//	REDIS_ADDR=localhost:6379 go test ./tests/integration/ -count=1

func TestTwoInstancesShareOneKey(t *testing.T) {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		t.Skip("REDIS_ADDR is unset; run: REDIS_ADDR=localhost:6379 go test ./tests/integration/ -count=1")
	}

	clientIP := "203.0.113.50"
	otherIP := "203.0.113.51"
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis: %v", err)
	}
	if err := rdb.Del(ctx, "rl:"+clientIP, "rl:"+otherIP).Err(); err != nil {
		t.Fatalf("del: %v", err)
	}

	root := moduleRoot(t)
	bin := filepath.Join(t.TempDir(), "server")
	build := exec.Command("go", "build", "-o", bin, "./cmd/server")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v\n%s", err, out)
	}

	port1 := freePort(t)
	port2 := freePort(t)
	stop1 := startServer(t, bin, port1, redisAddr)
	stop2 := startServer(t, bin, port2, redisAddr)
	defer stop1()
	defer stop2()
	waitHealthy(t, port1)
	waitHealthy(t, port2)

	for i := range 10 {
		port := port1
		if i%2 == 1 {
			port = port2
		}
		status := get(t, port, clientIP)
		if status != http.StatusOK {
			t.Fatalf("request %d via port %d status = %d, want 200", i+1, port, status)
		}
	}
	if status := get(t, port2, clientIP); status != http.StatusTooManyRequests {
		t.Fatalf("11th request on the second instance status = %d, want 429", status)
	}
	if status := get(t, port1, otherIP); status != http.StatusOK {
		t.Fatalf("other client status = %d, want 200", status)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	return filepath.Dir(string(trimNewline(out)))
}

func trimNewline(b []byte) string {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return string(b)
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return port
}

func startServer(t *testing.T, bin string, port int, redisAddr string) func() {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"SERVER_PORT="+strconv.Itoa(port),
		"STORAGE_BACKEND=redis",
		"REDIS_ADDR="+redisAddr,
		"RATE_LIMIT_CAPACITY=10",
		"RATE_LIMIT_REFILL_RATE=1",
		"RATE_LIMIT_REFILL_INTERVAL=1h",
		"TRUSTED_PROXIES=127.0.0.1,::1",
		"LIMITER_ON_ERROR=fail_closed",
	)
	logs, err := os.CreateTemp(t.TempDir(), "server-*.log")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	return func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		_, _ = logs.Seek(0, io.SeekStart)
		text, _ := io.ReadAll(logs)
		if t.Failed() && len(text) > 0 {
			t.Logf("server :%d\n%s", port, text)
		}
		_ = logs.Close()
	}
}

func waitHealthy(t *testing.T, port int) {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("server on port %d did not become healthy", port)
}

func get(t *testing.T, port int, clientIP string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/resource", port), nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("X-Forwarded-For", clientIP)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}
