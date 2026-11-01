// UNTESTED ON REAL APIGEE. Exercised only by gateway/policy.test.js, which runs this exact file
// with a stub `context` and compares it with the POC gateway. Written in ES5 for the Rhino runtime.
//
// Parses the incoming W3C `traceparent` and exposes its parts as flow variables for
// AM-SetB3FromTraceparent. Sets nothing when the header is missing or not version 00,
// so the AssignMessage step (conditioned on w3c2b3.valid) is skipped.
var tp = context.getVariable('request.header.traceparent');
var m = tp ? /^00-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$/.exec(String(tp)) : null;

if (m) {
  context.setVariable('w3c2b3.trace_id', m[1]);
  context.setVariable('w3c2b3.span_id', m[2]);
  context.setVariable('w3c2b3.sampled', (parseInt(m[3], 16) & 1) ? '1' : '0');
  context.setVariable('w3c2b3.valid', 'true');
} else {
  context.setVariable('w3c2b3.valid', 'false');
}
