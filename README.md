# W3C `traceparent` → B3 → Tempo POC

**Setup.** An app's tracing SDK adds a W3C `traceparent` to its HTTP requests, and an APM tool
deep-links to the backend's trace by that trace id. Between them sits an API gateway (here,
Apigee-like) that only speaks B3, in front of an OpenTelemetry backend that reads B3. The fix tested
here: the gateway translates `traceparent` into `X-B3-*` headers.

**Question.** Does Tempo then store the backend span under the trace id the app sent?

**Answer.** Yes. The trace id is the same in the client, the gateway's B3 headers, the backend span
and Tempo, even though the gateway removes `traceparent` (see [A real run](#a-real-run)).

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
| `apigee-policy/` | The same translation as an Apigee policy: JavaScript policy + AssignMessage + target snippet. **Untested on real Apigee.** A test checks it builds the same B3 headers as the gateway. |
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
