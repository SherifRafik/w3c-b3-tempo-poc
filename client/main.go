// The "mobile app": prepares the request headers, adds a W3C traceparent (as an SDK would),
// sends the request to the gateway and logs what it sent and what came back.
package main

import (
	"crypto/rand"  // random trace id, span id and request id
	"encoding/hex" // turns bytes into a hex string
	"flag"         // command-line flags
	"fmt"          // string formatting
	"log"          // timestamped log lines on stderr
	"maps"         // map key helpers (for sorting headers)
	"net/http"     // HTTP client
	"slices"       // sorting helpers
	"strings"      // string helpers
	"time"         // current time, for the Grafana link's range
)

// grafanaURL is a Grafana deep link to the trace-lookup dashboard; the trace id is appended to it.
const grafanaURL = "http://localhost:3000/d/w3c-trace/w3c-trace-lookup?orgId=1&var-traceId="

func main() {
	gateway := flag.String("gateway", "http://localhost:8080", "gateway base URL") // where to send the request
	var extra headerFlags                                                          // collects the -H flags
	flag.Var(&extra, "H", "extra request header 'Name: value', repeatable (a traceparent here replaces the generated one)")
	flag.Parse()                                // read the command line
	log.SetFlags(log.Ltime | log.Lmicroseconds) // prefix every log line with HH:MM:SS.micros

	// 1. Prepare headers.
	req, _ := http.NewRequest("GET", *gateway+"/api/accounts", nil) // the app's API call, sent to the gateway
	reqID := randomHex(8)                                           // 16 hex chars that follow this request through every log
	req.Header.Set("X-Request-Id", reqID)                           // our correlation id (the backend tags its span with it)
	for _, kv := range extra {                                      // each -H flag, in order
		k, v, _ := strings.Cut(kv, ":")                            // split "Name: value" at the first colon
		req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v)) // add it, replacing any existing value
	}

	// 2. Prepare the traceparent, unless -H already supplied one.
	if req.Header.Get("traceparent") == "" {
		req.Header.Set("traceparent", newTraceparent()) // a fresh W3C traceparent, as an SDK injects
	}
	sentID := traceIDOf(req.Header.Get("traceparent")) // the trace id we sent; an APM tool would link to this id

	// 3. Fire the request and log it.
	log.Printf("[client]  req=%s → %s %s", reqID, req.Method, req.URL) // the request line
	for _, k := range slices.Sorted(maps.Keys(req.Header)) {           // every header we send, alphabetically
		log.Printf("[client]  req=%s     %s: %s", reqID, k, req.Header.Get(k))
	}
	resp, err := http.DefaultClient.Do(req) // send it and wait for the reply
	if err != nil {                         // gateway unreachable: stop
		log.Fatal(err)
	}
	resp.Body.Close()                                                                             // the body is not needed
	backendID := resp.Header.Get("X-Trace-Id")                                                    // the trace the backend recorded the request under
	log.Printf("[client]  req=%s ← %s  backend recorded trace=%s", reqID, resp.Status, backendID) // the reply

	if sentID != "" { // a valid traceparent was sent: compare with what the backend recorded
		log.Printf("[client]  req=%s   sent trace id %s  same as backend: %v", reqID, sentID, sentID == backendID)
		now := time.Now() // the link covers the last 3 months
		log.Printf("[client]  req=%s   View on Grafana: %s%s&from=%d&to=%d", reqID, grafanaURL, sentID, now.AddDate(0, -3, 0).UnixMilli(), now.UnixMilli())
	}
}

// newTraceparent returns a W3C traceparent: version 00, a random 128-bit trace id,
// a random 64-bit parent (span) id, and flags 01 (sampled).
func newTraceparent() string {
	return fmt.Sprintf("00-%s-%s-01", randomHex(16), randomHex(8)) // 32 hex, 16 hex
}

// traceIDOf returns the trace-id field of a version-00 traceparent, or "" if it is not one.
func traceIDOf(traceparent string) string {
	parts := strings.Split(traceparent, "-")                        // version-traceid-parentid-flags
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 { // not a version-00 traceparent
		return ""
	}
	return parts[1] // the 32-hex trace id
}

// randomHex returns n random bytes as 2n lowercase hex characters.
func randomHex(n int) string {
	b := make([]byte, n) // n bytes of buffer
	rand.Read(b)         // fill with randomness
	return hex.EncodeToString(b)
}

// headerFlags collects repeated -H 'Name: value' flags.
type headerFlags []string

// String shows the collected flags (required by the flag package).
func (h *headerFlags) String() string { return strings.Join(*h, ", ") }

// Set is called once per -H flag: it checks the format and stores the value.
func (h *headerFlags) Set(v string) error {
	if !strings.Contains(v, ":") { // must look like "Name: value"
		return fmt.Errorf("want 'Name: value', got %q", v)
	}
	*h = append(*h, v) // keep it, in command-line order
	return nil
}
