# W3C `traceparent` → B3 → Tempo POC

**Setup.** An app's tracing SDK adds a W3C `traceparent` to its HTTP requests, and an APM tool
deep-links to the backend's trace by that trace id. Between them sits an API gateway (here,
Apigee-like) that only speaks B3, in front of an OpenTelemetry backend that reads B3. Two possible fixes:
1. the backend reads `traceparent` too, and prefers it over B3 (propagator order);
2. the gateway translates `traceparent` into `X-B3-*` headers.

**Question.** With fix 2, does Tempo store the backend span under the trace id the app sent?

**Answer.** Yes, as long as the translation is the last thing that writes B3 headers and any
single `b3` header is removed. It holds even though the gateway strips `traceparent`. Confirmed on
a Go backend, a JVM backend under the OpenTelemetry Java agent, and Spring Boot with Micrometer
Tracing (see [Scenario matrix](#scenario-matrix)).

Fix 1 (backend propagator order) works only if the gateway **forwards** `traceparent`.
Spring Boot also needs the **opposite** order to OpenTelemetry.

```
client ──traceparent──▶ gateway ──X-B3-* only──▶ backend ──OTLP──▶ Tempo ◀── Grafana
(W3C format)            (removes traceparent,    (Go, OpenTelemetry,
                         builds B3 multi)         reads B3)
```

| Piece | What it does |
|---|---|
| `client/` | Go. Prepares the headers, adds a W3C `traceparent` (random trace id and span id, flags `01`) as an SDK would, sends the request, logs it. Prints the trace id it sent, whether the backend recorded the same one, and its Grafana link. |
| `gateway/gateway.js` | Node, no deps. Logs the request, removes `traceparent`, builds `X-B3-TraceId` / `X-B3-SpanId` / `X-B3-Sampled` from it, forwards to the backend, logs. |
| `backend/` | Go + OpenTelemetry, reads B3. Records a span in the trace the B3 headers carry, sends it to Tempo, logs. |
| `apigee-policy/` | Fix 2 for Apigee: JavaScript policy + AssignMessage + target snippet. **Untested on real Apigee.** A test checks it builds the same B3 headers as the gateway. |
| Tempo 2.8.1 + Grafana 12.1.0 | `docker compose`. Tempo datasource and a `W3C trace lookup` dashboard are provisioned. |

## Run

Needs Docker, Go 1.24+, Node 24. Three terminals, from this folder:

```bash
docker compose up -d                       # Tempo on :3200, Grafana on :3000
go run ./backend                           # terminal A, listens on :8081
node gateway/gateway.js                    # terminal B, listens on :8080
go run ./client                            # terminal C, sends one request per run
```

`-H 'Name: value'` (repeatable) adds a header. A `traceparent` given this way replaces the generated one:

```bash
go run ./client -H 'traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01'
```

Ports: backend `PORT` (default 8081), gateway `PORT` (8080) and `TARGET_URL` (http://localhost:8081),
client `-gateway` (http://localhost:8080).

Tear down: `docker compose down` (Tempo keeps traces in memory only).

## Testing

**Unit tests:** the generated `traceparent` and trace-id parsing (Go), and the gateway's B3 headers
against the Apigee policy file (Node):

```bash
go test ./... && node --test gateway/policy.test.js
```

**End to end:**

1. Start the stack and the three processes as in [Run](#run).
2. Run `go run ./client`.
3. Follow the request by its `req=…` id through the three terminals. Check that:
   - the gateway logs `removed traceparent` and `built B3 multi` with the client's trace id;
   - the backend logs `continued trace=<that id>` and `sent to Tempo`;
   - the client ends with `same as backend: true`.
4. Confirm Tempo stored it under the trace id the client sent. This should return `200`:
   ```bash
   curl -s -o /dev/null -w '%{http_code}\n' localhost:3200/api/traces/<sent trace id>
   ```
   Use `/api/traces/`, not `/api/v2/traces/`: v2 returns 200 with an empty trace for ids Tempo doesn't have.
5. Open the client's `View on Grafana` link, a deep link of the form
   `…/d/w3c-trace/w3c-trace-lookup?var-traceId=<id>&from=…&to=…`. The panel shows the
   `b3-backend` span; its attributes list the B3 headers it received.

Negative checks:
- `go run ./client -H 'traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00'`: the
  gateway builds `X-B3-Sampled=0`, and the backend logs `NOT sampled, not sent to Tempo`.
- `curl localhost:8081/api/accounts -H 'traceparent: 00-…-01'` straight to the backend: it logs
  `no B3 trace received: started a new trace`, because the backend reads only B3.

## A real run

One request, captured on 2026-11-01 (on ports 8090/8091, so the URLs show those). The trace id
`b1d5f134fb10474a7cab7bbf52c5d03b` is the same in the client, the gateway's B3 headers, the backend span and Tempo.

Client:

```
17:41:52.902299 [client]  req=3dfaeea10eb0049a → GET http://localhost:8090/api/accounts
17:41:52.902803 [client]  req=3dfaeea10eb0049a     Traceparent: 00-b1d5f134fb10474a7cab7bbf52c5d03b-e477d8bff270dc8b-01
17:41:52.902810 [client]  req=3dfaeea10eb0049a     X-Request-Id: 3dfaeea10eb0049a
17:41:52.933636 [client]  req=3dfaeea10eb0049a ← 200 OK  backend recorded trace=b1d5f134fb10474a7cab7bbf52c5d03b
17:41:52.933665 [client]  req=3dfaeea10eb0049a   sent trace id b1d5f134fb10474a7cab7bbf52c5d03b  same as backend: true
17:41:52.933675 [client]  req=3dfaeea10eb0049a   View on Grafana: http://localhost:3000/d/w3c-trace/w3c-trace-lookup?orgId=1&var-traceId=b1d5f134fb10474a7cab7bbf52c5d03b&from=1785595312933&to=1793547712933
```

Gateway (the `traceparent:` / `Parsed traceparent:` lines are `console.log` debug output in `toB3Multi`):

```
gateway :8090 -> http://localhost:8091
17:41:52.910 [gateway] req=3dfaeea10eb0049a ← GET /api/accounts  from client
17:41:52.914 [gateway] req=3dfaeea10eb0049a     traceparent: 00-b1d5f134fb10474a7cab7bbf52c5d03b-e477d8bff270dc8b-01
17:41:52.914 [gateway] req=3dfaeea10eb0049a   removed traceparent: 00-b1d5f134fb10474a7cab7bbf52c5d03b-e477d8bff270dc8b-01
traceparent:  00-b1d5f134fb10474a7cab7bbf52c5d03b-e477d8bff270dc8b-01
Parsed traceparent:  [
  '00-b1d5f134fb10474a7cab7bbf52c5d03b-e477d8bff270dc8b-01',
  'b1d5f134fb10474a7cab7bbf52c5d03b',
  'e477d8bff270dc8b',
  '01',
  index: 0,
  input: '00-b1d5f134fb10474a7cab7bbf52c5d03b-e477d8bff270dc8b-01',
  groups: undefined
]
17:41:52.914 [gateway] req=3dfaeea10eb0049a   built B3 multi: X-B3-TraceId=b1d5f134fb10474a7cab7bbf52c5d03b X-B3-SpanId=e477d8bff270dc8b X-B3-Sampled=1
17:41:52.914 [gateway] req=3dfaeea10eb0049a → http://localhost:8091/api/accounts  to backend
17:41:52.914 [gateway] req=3dfaeea10eb0049a     x-b3-traceid: b1d5f134fb10474a7cab7bbf52c5d03b
17:41:52.914 [gateway] req=3dfaeea10eb0049a     x-b3-spanid: e477d8bff270dc8b
17:41:52.914 [gateway] req=3dfaeea10eb0049a     x-b3-sampled: 1
17:41:52.932 [gateway] req=3dfaeea10eb0049a ← 200 from backend, relayed to client
```

Backend:

```
17:41:52.074448 backend :8091 (reads B3)
17:41:52.932661 [backend] req=3dfaeea10eb0049a ← GET /api/accounts
17:41:52.932681 [backend] req=3dfaeea10eb0049a     x-b3-traceid: b1d5f134fb10474a7cab7bbf52c5d03b
17:41:52.932687 [backend] req=3dfaeea10eb0049a     x-b3-spanid: e477d8bff270dc8b
17:41:52.932690 [backend] req=3dfaeea10eb0049a     x-b3-sampled: 1
17:41:52.932693 [backend] req=3dfaeea10eb0049a   continued trace=b1d5f134fb10474a7cab7bbf52c5d03b from span=e477d8bff270dc8b
17:41:52.932739 [backend] req=3dfaeea10eb0049a   span trace=b1d5f134fb10474a7cab7bbf52c5d03b span=ba11f55966f44119 sent to Tempo
```

Tempo and Grafana:

```
GET localhost:3200/api/traces/b1d5f134fb10474a7cab7bbf52c5d03b -> 200
GET Grafana Tempo datasource /api/traces/b1d5f134fb10474a7cab7bbf52c5d03b -> 200
```

## Scenario matrix

An earlier version of this POC ran 10 scenarios against three backends: a gateway with
configurable steps (its own B3 injection, policy order, single `b3`, 64-bit truncation), the Go
backend, a JVM backend under the OpenTelemetry Java agent, and a Spring Boot backend. That code is
not in this repository; these are its results.

Whether Tempo stored the span under the trace id the app sent, per backend.
Every cell matches the prediction; no run produced an unexpected result.

| Scenario | Gateway steps | `traceparent` reaches backend | Go OTel SDK (`OTEL_PROPAGATORS`) | OTel Java agent 2.31.0 (same values) | Spring Boot 3.5 (`…propagation.consume`) |
|---|---|---|---|---|---|
| 1 control | passthrough | yes | `tracecontext`: **match** | **match** | `w3c`: **match** |
| 2 no fix (gateway's own B3) | `own_b3` | no (stripped) | `b3multi`: no, gateway's id | no, gateway's id | `b3multi`: no, gateway's id |
| 3 fix 2 | `own_b3,translate` | no (stripped) | `b3multi`: **match** | **match** | `b3multi`: **match** |
| 4 wrong order | `translate,own_b3` | no (stripped) | `b3multi`: no, gateway's id | no, gateway's id | `b3multi`: no, gateway's id |
| 5a fix 1, B3 listed first | `own_b3` | yes | `b3multi,tracecontext`: **match** | **match** | `b3multi,w3c`: no, gateway's id |
| 5b fix 1, W3C listed first | `own_b3` | yes | `tracecontext,b3multi`: no, gateway's id | no, gateway's id | `w3c,b3multi`: **match** |
| 5c fix 1, gateway strips `traceparent` | `own_b3` | no (stripped) | `b3multi,tracecontext`: no, gateway's id | no, gateway's id | `b3multi,w3c`: no, gateway's id |
| 6 64-bit truncation | `own_b3,translate64` | no (stripped) | `b3multi`: no, zero-padded low 16 hex | no, same | no, same |
| 7a single `b3` left in | `own_b3_single,translate_keep_b3` | no (stripped) | `b3multi`: no, single `b3` wins | no, single `b3` wins | no, single `b3` wins |
| 7b single `b3` removed by policy | `own_b3_single,translate` | no (stripped) | `b3multi`: **match** | **match** | **match** |

`own_b3` is the gateway starting its own B3 trace (like Apigee's own tracing); `translate` is fix 2,
the policy in `apigee-policy/`.

## What the scenario matrix found

Nothing contradicted the predicted trace ids: every scenario stored the predicted id, on all three
backends. Item 1 confirms the expected first-wins vs last-wins difference. Items 2 to 4 are
surprises, or things that could mislead someone repeating the test.

1. **Spring Boot / Micrometer Tracing is first-wins, so fix 1 flips.** With the gateway's
   own B3 plus the app's `traceparent` on the same request:
   - OTel SDK and OTel Java agent: `b3multi,tracecontext` matches; `tracecontext,b3multi` does not
     (the composite propagator lets the last valid extractor win).
   - Spring Boot: `management.tracing.propagation.consume=b3_multi,w3c` does **not** match;
     `w3c,b3_multi` matches (the first extractor that finds a context wins).

   So "put `tracecontext` last" is right for the OTel agent and wrong for Spring Boot. Check which
   one the backend uses before choosing an order.
2. **Tempo `GET /api/v2/traces/<id>` returns `200 {"trace":{},"metrics":{}}` for an id it does not
   have.** Only `/api/traces/<id>` (v1) returns 404. An early run used v2 and showed 200 for
   every mismatch. Check v2 for an empty `trace`, or use v1.
3. **64-bit truncation (scenario 6), what Tempo does.** B3 extractors in all three stacks left-pad
   a 16-hex `X-B3-TraceId` with zeros, so Tempo stores `0000000000000000` + the low 16 hex:
   - its search API prints it without the leading zeros (just the 16 hex). Search does this for
     every id: a random gateway id starting with `0` shows up as 31 hex in the tables;
   - `/api/traces/<16 hex>` and `/api/traces/<32 hex zero-padded>` both find it;
   - the full 32-hex trace id the app sent returns 404, so the "View on Grafana" link shows nothing.

   No error anywhere; the link is just empty.
4. **A single `b3` header beats `X-B3-*` in all three stacks** (scenario 7a). The policy's
   "remove `b3`" line is needed if the gateway ever emits the single form.
