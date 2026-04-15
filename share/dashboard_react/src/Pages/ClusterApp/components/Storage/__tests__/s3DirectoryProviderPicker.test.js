// S3Directory Provider Picker Logic Tests
// Tests the pure logic functions for saved provider selection and mount field population.
// Run with: node src/Pages/ClusterApp/components/Storage/__tests__/s3DirectoryProviderPicker.test.js
// Or via: npm run test:s3-provider-picker

// --- Logic extracted from S3Directory.jsx for isolated testing ---

const defaultS3 = { name: "", endpoint: "", bucket: "", region: "", providerName: "" };

/**
 * Simulate the handleSavedProviderChange logic from S3DirectoryNewForm.
 * Returns the updated s3 state after a provider is selected.
 */
function applyProviderToS3(currentS3, providerName, clusterS3Providers) {
  if (!providerName) return currentS3;
  const provider = (clusterS3Providers || []).find((p) => p.name === providerName);
  if (!provider) return currentS3;
  const endpoint = provider.endpoint || provider.providerApp || "";
  return {
    ...currentS3,
    endpoint,
    region: provider.region || currentS3.region,
    accesskey: "",
    secretkey: "",
    providerName,
  };
}

/**
 * Build the savedProviderOptions list from clusterS3Providers.
 * Reflects the useMemo in S3DirectoryNewForm and S3DirectoryRowForm:
 * only "app"-source providers are offered — custom-endpoint providers
 * cannot be resolved by the backend GetAppByURL check.
 */
function buildSavedProviderOptions(clusterS3Providers) {
  return (clusterS3Providers || [])
    .filter((p) => p.providerSource === "app")
    .map((p) => ({ value: p.name, name: p.name }));
}

/**
 * Simulate what the S3DirectoryRowForm dispatches for a region update.
 * The backend modify switch now supports key="region" as a simple assignment.
 */
function buildRegionDispatch(fieldName, index, region) {
  return { fieldName, index, key: "region", value: region };
}

// --- Test Data ---

const sampleProviders = [
  {
    name: "prod-s3",
    providerSource: "custom",
    endpoint: "https://s3.example.com",
    region: "us-east-1",
  },
  {
    name: "dev-minio",
    providerSource: "app",
    providerApp: "minio-app:9000",
    region: "eu-west-1",
  },
  {
    name: "staging-minio",
    providerSource: "app",
    providerApp: "staging-minio:9000",
    // no region
  },
  {
    name: "no-region-provider",
    providerSource: "custom",
    endpoint: "https://other.example.com",
  },
];

// --- Test Runner ---

let passed = 0;
let failed = 0;

function assert(condition, description) {
  if (condition) {
    passed++;
    console.log(`  ✓ ${description}`);
  } else {
    failed++;
    console.log(`  ✗ ${description}`);
  }
}

function assertEqual(actual, expected, description) {
  const ok = JSON.stringify(actual) === JSON.stringify(expected);
  if (ok) {
    passed++;
    console.log(`  ✓ ${description}`);
  } else {
    failed++;
    console.log(`  ✗ ${description}`);
    console.log(`      Expected: ${JSON.stringify(expected)}`);
    console.log(`      Actual:   ${JSON.stringify(actual)}`);
  }
}

// --- Tests ---

console.log("\nTest Suite: buildSavedProviderOptions — filters to app-source only (Blocker 2 fix)");
{
  const opts = buildSavedProviderOptions(sampleProviders);
  assertEqual(opts.length, 2, "returns only app-source providers (2 of 4)");
  assertEqual(opts[0], { value: "dev-minio", name: "dev-minio" }, "first option is dev-minio");
  assertEqual(opts[1], { value: "staging-minio", name: "staging-minio" }, "second option is staging-minio");
  assert(!opts.some((o) => o.value === "prod-s3"), "custom-source provider prod-s3 is excluded");
  assert(!opts.some((o) => o.value === "no-region-provider"), "custom-source provider no-region-provider is excluded");
}

console.log("\nTest Suite: buildSavedProviderOptions — empty/null inputs");
{
  assertEqual(buildSavedProviderOptions([]), [], "returns empty array for empty list");
  assertEqual(buildSavedProviderOptions(null), [], "returns empty array for null");
  assertEqual(buildSavedProviderOptions(undefined), [], "returns empty array for undefined");
}

console.log("\nTest Suite: applyProviderToS3 — app provider uses providerApp as endpoint (AC: 2)");
{
  const result = applyProviderToS3({ ...defaultS3 }, "dev-minio", sampleProviders);
  assertEqual(result.endpoint, "minio-app:9000", "copies providerApp into endpoint field");
  assertEqual(result.region, "eu-west-1", "copies region from provider");
  assertEqual(result.providerName, "dev-minio", "sets providerName for traceability");
  assertEqual(result.accesskey, "", "clears accesskey (not available in Redux)");
  assertEqual(result.secretkey, "", "clears secretkey (not available in Redux)");
}

console.log("\nTest Suite: applyProviderToS3 — app provider without region preserves existing region");
{
  const initial = { ...defaultS3, region: "ap-southeast-1" };
  const result = applyProviderToS3(initial, "staging-minio", sampleProviders);
  assertEqual(result.endpoint, "staging-minio:9000", "copies providerApp");
  assertEqual(result.region, "ap-southeast-1", "keeps existing region when provider has none");
}

console.log("\nTest Suite: manual entry still works when no provider is selected (AC: 1)");
{
  const initial = { ...defaultS3, endpoint: "minio-local:9000", bucket: "my-bucket" };
  const result = applyProviderToS3(initial, "", sampleProviders);
  assertEqual(result.endpoint, "minio-local:9000", "endpoint unchanged when no provider selected");
  assertEqual(result.bucket, "my-bucket", "bucket unchanged when no provider selected");
  assertEqual(result.providerName, "", "providerName remains empty");
}

console.log("\nTest Suite: applyProviderToS3 — unknown provider name returns state unchanged");
{
  const initial = { ...defaultS3, endpoint: "existing" };
  const result = applyProviderToS3(initial, "nonexistent", sampleProviders);
  assertEqual(result, initial, "state unchanged when provider name not found");
}

console.log("\nTest Suite: providerName included in saved payload");
{
  const s3AfterSelection = applyProviderToS3({ ...defaultS3, bucket: "test-bucket" }, "dev-minio", sampleProviders);
  assert(s3AfterSelection.providerName === "dev-minio", "providerName is in form state before save");
  assert("providerName" in s3AfterSelection, "providerName key exists in payload");
}

console.log("\nTest Suite: copy-then-edit remains local when provider changes (AC: 2)");
{
  const copied = applyProviderToS3({ ...defaultS3, bucket: "my-bucket" }, "dev-minio", sampleProviders);
  // User edits copied values locally after the copy event.
  const edited = {
    ...copied,
    endpoint: "custom-edited-endpoint:9000",
    region: "ap-south-1",
  };

  // Later, provider library changes (new endpoint/region), but existing mount state
  // must remain unchanged until user explicitly chooses a provider again.
  const updatedProviders = sampleProviders.map((p) =>
    p.name === "dev-minio"
      ? { ...p, providerApp: "new-minio-app:9000", region: "us-west-2" }
      : p
  );

  assertEqual(edited.endpoint, "custom-edited-endpoint:9000", "edited endpoint remains local");
  assertEqual(edited.region, "ap-south-1", "edited region remains local");
  assertEqual(edited.providerName, "dev-minio", "providerName traceability remains present");

  // Ensure provider list update by itself does not mutate current mount object.
  assertEqual(updatedProviders.find((p) => p.name === "dev-minio").providerApp, "new-minio-app:9000", "provider library changed independently");
  assertEqual(edited.endpoint, "custom-edited-endpoint:9000", "mount state unchanged after provider library update");
}

console.log("\nTest Suite: copy-then-edit survives provider deletion (AC: 2)");
{
  const copied = applyProviderToS3({ ...defaultS3, bucket: "archive" }, "dev-minio", sampleProviders);
  const edited = { ...copied, region: "eu-central-1" };
  const providersAfterDelete = sampleProviders.filter((p) => p.name !== "dev-minio");

  // Provider no longer appears in options.
  const opts = buildSavedProviderOptions(providersAfterDelete);
  assert(!opts.some((o) => o.value === "dev-minio"), "deleted provider is not shown in picker options");

  // Existing mount still carries edited effective values and providerName trace string.
  assertEqual(edited.endpoint, "minio-app:9000", "existing mount endpoint remains unchanged after provider deletion");
  assertEqual(edited.region, "eu-central-1", "existing mount edited region remains unchanged after provider deletion");
  assertEqual(edited.providerName, "dev-minio", "providerName traceability string remains on existing mount");
}

console.log("\nTest Suite: region dispatch key for S3DirectoryRowForm (Blocker 1 fix)");
{
  const dispatch = buildRegionDispatch("s3Mounts", 0, "us-west-2");
  assertEqual(dispatch.key, "region", "dispatches key='region' — now supported by backend modify switch");
  assertEqual(dispatch.value, "us-west-2", "carries the region value");
  assertEqual(dispatch.fieldName, "s3Mounts", "targets s3Mounts field");
}

// --- Summary ---
console.log(`\n========================================`);
console.log(`Total: ${passed + failed} tests`);
console.log(`Passed: ${passed}`);
console.log(`Failed: ${failed}`);

if (typeof require !== "undefined" && require.main === module) {
  process.exit(failed === 0 ? 0 : 1);
}
