import http from "k6/http";
import { check } from "k6";
import { Trend } from "k6/metrics";
import { uuidv4 } from "https://jslib.k6.io/k6-utils/1.4.0/index.js";

const ingestLatency = new Trend("ingest_latency", true);

// --- Config ---------------------------------------------------------------
const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";
const TARGET_URL = __ENV.TARGET_URL || "http://host.docker.internal:9999/sink";

export const options = {
  scenarios: {
    find_ceiling: {
      executor: "ramping-arrival-rate",
      // startRate/timeUnit define the base rate; stages scale from there.
      startRate: 1300,
      timeUnit: "1s",
      preAllocatedVUs: 300,
      maxVUs: 2500,
      stages: [
        { target: 1300, duration: "3m" }, // hold flat — looking for a stable plateau this time
        { target: 0, duration: "20s" },
      ],
    },
  },
  // No hard thresholds here — the point of this run is to WATCH where
  // latency/errors start climbing, not to pass/fail against a fixed bar.
  // Re-add thresholds once you know the real ceiling and want a regression guard.
};

export default function () {
  const payload = JSON.stringify({
    idempotency_key: uuidv4(),
    target_url: TARGET_URL,
    max_attempts: 1,
    payload: {
      event: "load_test.ceiling",
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

  // No sleep() here on purpose — arrival-rate executor controls pacing,
  // not the VU loop. Adding sleep would just waste VUs.
}
