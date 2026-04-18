import { extractApiErrorMessage, redactSensitiveInfo } from "../apiError.js";

let passed = 0;
let failed = 0;

function assertEqual(actual, expected, description) {
  if (actual === expected) {
    passed += 1;
    console.log(`  ✓ ${description}`);
    return;
  }
  failed += 1;
  console.log(`  ✗ ${description}`);
  console.log(`      Expected: ${expected}`);
  console.log(`      Actual:   ${actual}`);
}

console.log("\nTest Suite: extractApiErrorMessage");

{
  const err = { response: { data: { errorMessage: "Invalid bucket name" } } };
  assertEqual(
    extractApiErrorMessage(err, "Fallback"),
    "Invalid bucket name",
    "uses structured JSON backend errorMessage"
  );
}

{
  const err = { response: { data: "{\"message\":\"Provider is missing\"}" } };
  assertEqual(
    extractApiErrorMessage(err, "Fallback"),
    "Provider is missing",
    "parses JSON encoded string payload"
  );
}

{
  const err = { response: { data: "plain text backend failure" } };
  assertEqual(
    extractApiErrorMessage(err, "Fallback"),
    "plain text backend failure",
    "uses plain text backend payload"
  );
}

{
  const err = { request: {}, message: "Network Error", code: "ERR_NETWORK" };
  assertEqual(
    extractApiErrorMessage(err, "Fallback"),
    "Network error. Please check your connection and retry.",
    "maps network failures to stable user message"
  );
}

{
  const err = { message: "failed with token=abc123" };
  assertEqual(
    extractApiErrorMessage(err, "Fallback"),
    "failed with token=[REDACTED]",
    "redacts token-like values in direct error message"
  );
}

console.log("\nTest Suite: redactSensitiveInfo");

{
  assertEqual(
    redactSensitiveInfo("secretkey=my-secret-value"),
    "secretkey=[REDACTED]",
    "redacts key=value secret strings"
  );
}

{
  assertEqual(
    redactSensitiveInfo("Bearer super-secret-token"),
    "Bearer [REDACTED]",
    "redacts bearer tokens"
  );
}

if (failed > 0) {
  console.log(`\n❌ Failed: ${failed}, Passed: ${passed}`);
  process.exit(1);
}

console.log(`\n✅ All tests passed: ${passed}`);
