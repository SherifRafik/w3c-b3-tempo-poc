// node --test gateway/policy.test.js
// The gateway builds B3 in JavaScript of its own; the Apigee policy in apigee-policy/ is a separate
// file. These tests pin the gateway's output and check that the Apigee policy agrees with it.
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { toB3Multi } = require('./gateway');

const TP = '00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01'; // the W3C spec's example
const POLICY_JS = fs.readFileSync(path.join(__dirname, '../apigee-policy/apiproxy/resources/jsc/traceparent-to-b3.js'), 'utf8');

// Runs the Apigee policy file as-is with a stub `context`; returns the flow variables it set.
function runApigeePolicy(traceparent) {
  const vars = {};
  const context = {
    getVariable: (n) => (n === 'request.header.traceparent' ? traceparent ?? null : vars[n] ?? null),
    setVariable: (n, v) => { vars[n] = v; },
  };
  vm.runInNewContext(POLICY_JS, { context });
  return vars;
}

test('builds B3 multi from the W3C spec example', () => {
  assert.deepStrictEqual(toB3Multi(TP), { 'x-b3-traceid': '4bf92f3577b34da6a3ce929d0e0e4736', 'x-b3-spanid': '00f067aa0ba902b7', 'x-b3-sampled': '1' });
});

test('the Apigee policy agrees with the gateway', () => {
  const cases = [TP, TP.replace(/-01$/, '-02'), undefined, '', TP.toUpperCase(), '01' + TP.slice(2), TP + '-extra', TP.slice(0, -1)];
  for (const tp of cases) {
    const v = runApigeePolicy(tp);
    const fromPolicy = v['w3c2b3.valid'] === 'true'
      ? { 'x-b3-traceid': v['w3c2b3.trace_id'], 'x-b3-spanid': v['w3c2b3.span_id'], 'x-b3-sampled': v['w3c2b3.sampled'] }
      : null;
    assert.deepStrictEqual(toB3Multi(tp), fromPolicy, `traceparent ${tp}`);
  }
});
