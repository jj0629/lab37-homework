// Command mock-webhook replays webhook_orders.jsonl against the ingest edge
// with a bursty traffic profile.
//
//	mock-webhook --rate 20 --burst 200 --burst-every 5s          # replay the sample file
//	mock-webhook --fresh --count 5000 --rate 100 --burst 500     # generate new orders
package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	url := flag.String("url", envOr("WEBHOOK_URL", "http://localhost:8080/api/ingest/webhook"), "ingest endpoint")
	file := flag.String("file", envOr("WEBHOOK_FILE", "../sample-data/webhook_orders.jsonl"), "sample payloads")
	rate := flag.Float64("rate", 20, "steady requests per second")
	burst := flag.Int("burst", 50, "extra requests fired at once every --burst-every (0 = none)")
	every := flag.Duration("burst-every", 5*time.Second, "interval between bursts")
	count := flag.Int("count", 0, "total requests (0 = every line once)")
	fresh := flag.Bool("fresh", false, "give each payload a new order_id so it creates a new order")
	conc := flag.Int("concurrency", 16, "max in-flight requests")
	flag.Parse()

	lines, err := readLines(*file)
	if err != nil || len(lines) == 0 {
		fmt.Fprintln(os.Stderr, "read sample file:", err)
		os.Exit(1)
	}
	total := *count
	if total <= 0 {
		total = len(lines)
	}

	jobs := make(chan []byte, *conc*4)
	go func() {
		defer close(jobs)
		tick := time.NewTicker(time.Duration(float64(time.Second) / *rate))
		defer tick.Stop()
		var burstC <-chan time.Time
		if *burst > 0 {
			bt := time.NewTicker(*every)
			defer bt.Stop()
			burstC = bt.C
		}
		sent := 0
		next := func() []byte {
			var raw []byte
			if *fresh {
				n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(lines))))
				var m map[string]any
				_ = json.Unmarshal(lines[n.Int64()], &m)
				delete(m, "update")
				m["order_id"] = uuid()
				raw, _ = json.Marshal(m)
			} else {
				raw = lines[sent%len(lines)]
			}
			sent++
			return raw
		}
		for sent < total {
			select {
			case <-tick.C:
				jobs <- next()
			case <-burstC:
				for i := 0; i < *burst && sent < total; i++ {
					jobs <- next()
				}
			}
		}
	}()

	var ok, dup, fail atomic.Int64
	var mu sync.Mutex
	var lat []time.Duration
	client := &http.Client{Timeout: 10 * time.Second}
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < *conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for raw := range jobs {
				t := time.Now()
				resp, err := client.Post(*url, "application/json", bytes.NewReader(raw))
				d := time.Since(t)
				if err != nil {
					fail.Add(1)
					continue
				}
				var body struct {
					Duplicates int `json:"duplicates"`
				}
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				_ = json.Unmarshal(b, &body)
				if resp.StatusCode == http.StatusAccepted {
					ok.Add(1)
					dup.Add(int64(body.Duplicates))
				} else {
					fail.Add(1)
				}
				mu.Lock()
				lat = append(lat, d)
				mu.Unlock()
			}
		}()
	}
	progress := time.NewTicker(2 * time.Second)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-progress.C:
				fmt.Printf("sent %d/%d  accepted=%d duplicates=%d failed=%d\n", ok.Load()+fail.Load(), total, ok.Load(), dup.Load(), fail.Load())
			case <-done:
				return
			}
		}
	}()
	wg.Wait()
	close(done)
	elapsed := time.Since(start)
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	pct := func(p float64) time.Duration {
		if len(lat) == 0 {
			return 0
		}
		return lat[int(p*float64(len(lat)-1))]
	}
	fmt.Printf("\ndone: %d requests in %s (%.1f req/s)\naccepted=%d duplicates=%d failed=%d\nlatency p50=%s p95=%s p99=%s max=%s\n",
		ok.Load()+fail.Load(), elapsed.Round(time.Millisecond), float64(ok.Load()+fail.Load())/elapsed.Seconds(),
		ok.Load(), dup.Load(), fail.Load(), pct(.5), pct(.95), pct(.99), pct(1))
	if fail.Load() > 0 {
		os.Exit(1)
	}
}

func readLines(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if b := bytes.TrimSpace(sc.Bytes()); len(b) > 0 {
			out = append(out, append([]byte(nil), b...))
		}
	}
	return out, sc.Err()
}

func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
