// Stand-in for a B3-only API gateway (like Apigee). For each request it:
//   1. receives it and logs its trace headers,
//   2. removes `traceparent`,
//   3. builds B3 multi headers (X-B3-TraceId / X-B3-SpanId / X-B3-Sampled) from it,
//   4. forwards it to the backend and logs what it sent and what came back.
const http = require('node:http'); // HTTP server (from the client) and HTTP client (to the backend)

const TARGET = new URL(process.env.TARGET_URL || 'http://localhost:8081'); // where the backend listens
const PORT = Number(process.env.PORT || 8080);                             // where this gateway listens
// A version-00 traceparent: captures trace-id (32 hex), parent-id (16 hex) and flags (2 hex).
const TRACEPARENT = /^00-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$/;
// The trace headers the gateway logs, in display order.
const TRACE_KEYS = ['traceparent', 'b3', 'x-b3-traceid', 'x-b3-spanid', 'x-b3-parentspanid', 'x-b3-sampled'];

// toB3Multi returns the B3 multi headers for a traceparent, or null when it is missing or invalid.
// Same rule as the Apigee policy in apigee-policy/ (gateway/policy.test.js checks they agree).
function toB3Multi(traceparent) {
  console.log("traceparent: ", traceparent);
  const m = TRACEPARENT.exec(traceparent || ''); // parse it
  console.log("Parsed traceparent: ", m);
  if (!m) return null;                           // not a valid version-00 traceparent: nothing to build
  return {
    'x-b3-traceid': m[1],                                   // trace-id → X-B3-TraceId
    'x-b3-spanid': m[2],                                    // parent-id → X-B3-SpanId
    'x-b3-sampled': (parseInt(m[3], 16) & 1) ? '1' : '0',   // bit 0 of the flags → X-B3-Sampled
  };
}

// Local wall-clock time as HH:MM:SS.mmm, to line up with the client and backend logs.
const time = (d = new Date()) => `${d.toTimeString().slice(0, 8)}.${String(d.getMilliseconds()).padStart(3, '0')}`;
// Prints one gateway log line to stderr, tagged with the request id.
const log = (rid, msg) => console.error(`${time()} [gateway] req=${rid} ${msg}`);
// Prints each trace header present in h, one per line.
const logHeaders = (rid, h) => {
  const keys = TRACE_KEYS.filter((k) => h[k] !== undefined); // the trace headers this request has
  if (!keys.length) log(rid, '    (no trace headers)');        // say so if there are none
  for (const k of keys) log(rid, `    ${k}: ${h[k]}`);         // "name: value" for each one
};

// Exported so gateway/policy.test.js can test toB3Multi without starting a server.
module.exports = { toB3Multi };

// Everything below runs only when started as `node gateway/gateway.js`, not when the test imports it.
if (require.main === module) {
  // Called once per incoming request: req is what the client sent, res is our reply to it.
  http.createServer((req, res) => {
    // 1. Receive the request and log it.
    const h = { ...req.headers };         // copy the client's headers; this copy is what goes to the backend
    delete h.host;                        // the backend gets its own Host header
    delete h.connection;                  // hop-by-hop header, not forwarded
    const rid = h['x-request-id'] || '-'; // the client's request id, to follow one request across the logs
    log(rid, `← ${req.method} ${req.url}  from client`);
    logHeaders(rid, h);                   // the trace headers as received

    // 2. Remove traceparent (keeping its value to build B3 from).
    const traceparent = h.traceparent;    // the app's W3C header
    delete h.traceparent;                 // a B3-only gateway does not pass it on
    log(rid, `  removed traceparent: ${traceparent ?? '(none)'}`);

    // 3. Build B3 multi headers from it.
    const b3 = toB3Multi(traceparent);    // null when there is no valid traceparent
    if (b3) {
      delete h.b3;                        // a single b3 header would win over X-B3-* at the backend
      delete h['x-b3-parentspanid'];      // traceparent has no equivalent; never send one
      Object.assign(h, b3);               // set (overwrite) X-B3-TraceId, X-B3-SpanId, X-B3-Sampled
      log(rid, `  built B3 multi: X-B3-TraceId=${b3['x-b3-traceid']} X-B3-SpanId=${b3['x-b3-spanid']} X-B3-Sampled=${b3['x-b3-sampled']}`);
    } else {
      log(rid, '  no valid traceparent: no B3 built, request forwarded as is'); // leave any existing B3 alone
    }

    // 4. Forward to the backend and log.
    log(rid, `→ ${TARGET.origin}${req.url}  to backend`);
    logHeaders(rid, h);                   // the trace headers the backend will see
    const up = http.request({ hostname: TARGET.hostname, port: TARGET.port, path: req.url, method: req.method, headers: h }, (r) => {
      log(rid, `← ${r.statusCode} from backend, relayed to client`); // the backend's reply status
      res.writeHead(r.statusCode, r.headers);                       // reply to the client with the backend's status and headers
      r.pipe(res);                                                    // stream the backend's body to the client
    });
    up.on('error', (e) => { res.writeHead(502); res.end(String(e)); }); // backend unreachable: answer 502 with the error
    req.pipe(up);                                                        // stream the client's body (if any) to the backend
  }).listen(PORT, () => console.error(`gateway :${PORT} -> ${TARGET.origin}`)); // start listening, print a startup line
}
