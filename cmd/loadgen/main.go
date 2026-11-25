// Command loadgen sends a fixed set of clients at a running rate limiter
// and compares accepted requests with capacity plus refill over the run.
package main

import (
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:8080/api/resource", "protected URL")
	label := flag.String("label", "", "name recorded with this run, such as memory or redis")
	clients := flag.Int("clients", 4, "number of distinct client addresses")
	base := flag.Int("base", 10, "first last-octet of the client addresses; use a new base for each run")
	requests := flag.Int("requests", 100, "requests per client")
	concurrency := flag.Int("concurrency", 32, "number of workers")
	capacity := flag.Float64("capacity", 10, "bucket capacity configured on the server")
	rate := flag.Float64("rate", 20, "refill rate configured on the server")
	interval := flag.Duration("interval", time.Second, "refill interval configured on the server")
	slackFraction := flag.Float64("slack", 0.15, "allowed relative error, with a floor of 3 tokens")
	flag.Parse()

	if *clients < 1 || *requests < 1 || *concurrency < 1 {
		fmt.Fprintln(os.Stderr, "clients, requests, and concurrency must be at least 1")
		os.Exit(2)
	}
	if *clients > 200 {
		fmt.Fprintln(os.Stderr, "clients must be at most 200")
		os.Exit(2)
	}

	ips := make([]string, *clients)
	if *base < 1 || *base+*clients-1 > 254 {
		fmt.Fprintln(os.Stderr, "base and clients must stay inside 203.0.113.1–254")
		os.Exit(2)
	}
	for i := range ips {
		ips[i] = fmt.Sprintf("203.0.113.%d", *base+i)
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        *concurrency,
			MaxIdleConnsPerHost: *concurrency,
		},
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	records := make([][]sample, *clients)
	var mu sync.Mutex

	wg.Add(*concurrency)
	for range *concurrency {
		go func() {
			defer wg.Done()
			for clientID := range jobs {
				started := time.Now()
				status, err := send(client, *url, ips[clientID])
				finished := time.Now()
				mu.Lock()
				records[clientID] = append(records[clientID], sample{
					status:   status,
					err:      err,
					started:  started,
					finished: finished,
				})
				mu.Unlock()
			}
		}()
	}

	for range *requests {
		for clientID := range ips {
			jobs <- clientID
		}
	}
	close(jobs)
	wg.Wait()

	report(*label, *url, ips, records, *capacity, *rate, *interval, *slackFraction)
}

type sample struct {
	status   int
	err      error
	started  time.Time
	finished time.Time
}

func send(client *http.Client, url, ip string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-Forwarded-For", ip)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func report(label, url string, ips []string, records [][]sample, capacity, rate float64, interval time.Duration, slackFraction float64) {
	var accepted, rejected, failed int
	var latencies []time.Duration
	within := true

	fmt.Printf("label: %s\n", label)
	fmt.Printf("url: %s\n", url)
	fmt.Printf("clients: %d\n", len(ips))
	for i, ip := range ips {
		var clientAccepted int
		var first, last time.Time
		for _, sample := range records[i] {
			if sample.err != nil {
				failed++
				continue
			}
			latencies = append(latencies, sample.finished.Sub(sample.started))
			if first.IsZero() || sample.started.Before(first) {
				first = sample.started
			}
			if sample.finished.After(last) {
				last = sample.finished
			}
			switch sample.status {
			case http.StatusOK:
				accepted++
				clientAccepted++
			case http.StatusTooManyRequests:
				rejected++
			default:
				failed++
			}
		}
		span := last.Sub(first)
		want := expectedAccepts(capacity, rate, interval, span)
		slack := math.Max(3, want*slackFraction)
		ok := math.Abs(float64(clientAccepted)-want) <= slack
		if !ok {
			within = false
		}
		fmt.Printf("client %s: accepted %d expected %.1f span %s tolerance %.1f ok %v\n",
			ip, clientAccepted, want, span.Round(time.Millisecond), slack, ok)
	}

	total := accepted + rejected + failed
	fmt.Printf("accepted: %d\n", accepted)
	fmt.Printf("rejected: %d\n", rejected)
	fmt.Printf("errors: %d\n", failed)
	if total == 0 {
		fmt.Println("error rate: 0")
	} else {
		fmt.Printf("error rate: %.4f\n", float64(failed)/float64(total))
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	fmt.Printf("latency avg: %s p50: %s p95: %s p99: %s\n",
		average(latencies).Round(time.Microsecond),
		percentile(latencies, 0.50).Round(time.Microsecond),
		percentile(latencies, 0.95).Round(time.Microsecond),
		percentile(latencies, 0.99).Round(time.Microsecond))
	fmt.Printf("within tolerance: %v\n", within)
	if !within || failed > 0 {
		os.Exit(1)
	}
}

func expectedAccepts(capacity, rate float64, interval, span time.Duration) float64 {
	if interval <= 0 || span <= 0 {
		return capacity
	}
	return capacity + rate*float64(span)/float64(interval)
}

func average(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	var sum time.Duration
	for _, value := range values {
		sum += value
	}
	return sum / time.Duration(len(values))
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(p*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
