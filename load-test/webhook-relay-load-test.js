import http from "k6/http";
import { check, sleep } from "k6";
import { Trend } from "k6/metrics";
import { uuidv4 } from "https://jslib.k6.io/k6-utils/1.4.0/index.js";

// Custom metric so ingestion latency shows up cleanly in the summary,
// separate from k6's default http_req_duration (which would include
// any non-2xx responses too).
const ingestLatency = new Trend("ingest_latency", true);

// --- Config ---------------------------------------------------------------
const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";
// Point this at a fast local sink, NOT a public service (see note below).
const TARGET_URL = __ENV.TARGET_URL || "http://host.docker.internal:9999/sink";

export const options = {
  scenarios: {
    ramping_load: {
      executor: "ramping-vus",
      startVUs: 0,
      stages: [
        { duration: "30s", target: 50 }, // warm up
        { duration: "1m", target: 200 }, // ramp to moderate load
        { duration: "2m", target: 200 }, // hold — steady-state measurement window
        { duration: "30s", target: 500 }, // push toward peak
        { duration: "1m", target: 500 }, // hold at peak
        { duration: "30s", target: 0 }, // ramp down
      ],
    },
  },
  thresholds: {
    // These are the claims you'll want to quote in the README.
    // Tune the numbers once you see real results, then keep them
    // as regression guards for future runs.
    http_req_duration: ["p(95)<50", "p(99)<150"],
    http_req_failed: ["rate<0.01"],
    ingest_latency: ["p(95)<50", "p(99)<150"],
  },
};

export default function () {
  const payload = JSON.stringify({
    idempotency_key: uuidv4(),
    target_url: TARGET_URL,
    max_attempts: 1, // load test cares about ingestion, not the retry path
    payload: {
      event: "load_test.ping",
      vu: __VU,
      iter: __ITER,
      ts: Date.now(),
    },
  });

  const params = {
    headers: { "Content-Type": "application/json" },
  };

  const res = http.post(`${BASE_URL}/api/v1/webhooks`, payload, params);

  ingestLatency.add(res.timings.duration);

  check(res, {
    "status is 202": (r) => r.status === 202,
  });

  // Small think time so we're simulating realistic traffic rather than
  // one VU firing as fast as physically possible in a tight loop.
  sleep(0.1);
}
