// Package main implements a throwaway sink server for load testing.
//
// This is NOT part of the Webhook Relay product — it's a stand-in "customer
// endpoint" that the worker delivers to during k6 load tests, so that
// ingestion throughput can be measured without a real (or public) target
// becoming the bottleneck or getting hammered.
//
// Usage:
//
//	go run sink-server.go
//	go run sink-server.go -port 9999 -latency 0
//	go run sink-server.go -latency 20 -fail-rate 0.05
//
// Then point your webhook target_url at, e.g., http://localhost:9999/sink
// (or http://host.docker.internal:9999/sink if the worker is in Docker
// and the sink is running on the host).
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"
)

func main() {
	port := flag.Int("port", 9999, "port to listen on")
	latencyMs := flag.Int("latency", 0, "artificial response latency in milliseconds (simulates a real endpoint)")
	failRate := flag.Float64("fail-rate", 0, "fraction of requests to fail with 500, e.g. 0.05 for 5%")
	quiet := flag.Bool("quiet", false, "suppress per-request logging (recommended at high RPS)")
	flag.Parse()

	var received int64
	var failed int64

	mux := http.NewServeMux()

	mux.HandleFunc("/sink", func(w http.ResponseWriter, r *http.Request) {
		if *latencyMs > 0 {
			time.Sleep(time.Duration(*latencyMs) * time.Millisecond)
		}

		// Drain and discard the body so the connection is handled cleanly.
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()

		n := atomic.AddInt64(&received, 1)

		if *failRate > 0 && rand.Float64() < *failRate {
			atomic.AddInt64(&failed, 1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"status":"error"}`))
			if !*quiet {
				log.Printf("req #%d -> 500 (simulated failure)", n)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		if !*quiet {
			log.Printf("req #%d -> 200", n)
		}
	})

	// Simple counter endpoint so you can sanity-check totals after a run
	// without grepping logs.
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"received":%d,"failed":%d}`, atomic.LoadInt64(&received), atomic.LoadInt64(&failed))
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("sink server listening on %s (latency=%dms, fail-rate=%.2f)", addr, *latencyMs, *failRate)
	log.Fatal(http.ListenAndServe(addr, mux))
}
