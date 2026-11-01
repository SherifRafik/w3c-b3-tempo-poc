# traceparent → B3 for a B3-only Apigee proxy

> **Untested on real Apigee.** `gateway/policy.test.js` runs the JavaScript file as-is (with a
> stub `context`) and checks it builds the same B3 headers as the POC gateway. The AssignMessage
> XML is not executed anywhere. Validate in a non-prod proxy and an Apigee Debug session before rollout.

## What it does

When the incoming request has a valid W3C `traceparent`
(`^00-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$`), it overwrites the B3 headers sent to the target:

| Header | Value |
|---|---|
| `X-B3-TraceId` | traceparent trace-id (32 hex) |
| `X-B3-SpanId` | traceparent parent-id (16 hex) |
| `X-B3-Sampled` | bit 0 of the flags byte, `1` or `0` |
| `X-B3-ParentSpanId` | removed |
| `b3` (single header) | removed. B3 extractors prefer it over `X-B3-*` |

A missing or malformed `traceparent` leaves the request untouched. Both policies use
`continueOnError="true"`, so a failure here never fails the API call.

## Files

| File | Role |
|---|---|
| `apiproxy/resources/jsc/traceparent-to-b3.js` | parses `traceparent` into `w3c2b3.*` flow variables (ES5, for Rhino) |
| `apiproxy/policies/JS-TraceparentToB3.xml` | JavaScript policy wrapper |
| `apiproxy/policies/AM-SetB3FromTraceparent.xml` | sets / removes the headers |
| `apiproxy/targets/default.xml` | snippet: where to attach the two steps |

## Where to attach it

As the **last** request steps before the call to the backend (TargetEndpoint PostFlow, Request).
If anything writes `X-B3-*` or `b3` after these steps, it overwrites the translation and the
ids no longer match.

## What to check in the Apigee Debug session

1. Send a request with `traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01`.
2. In the request **sent to the target**, confirm:
   - `X-B3-TraceId: 4bf92f3577b34da6a3ce929d0e0e4736`
   - `X-B3-SpanId: 00f067aa0ba902b7`
   - `X-B3-Sampled: 1`
   - no `b3` and no `X-B3-ParentSpanId`.
3. Not known: whether Apigee's own distributed tracing (OpenCensus / Cloud Trace) writes its
   B3 headers at the target connection, i.e. after every policy. If the target still receives
   Apigee's ids, this policy cannot win. The remaining option is a backend that reads
   `traceparent` itself and prefers it over B3, which needs Apigee to forward `traceparent`.
