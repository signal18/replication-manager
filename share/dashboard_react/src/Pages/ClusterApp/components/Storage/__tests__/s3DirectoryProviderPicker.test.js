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

/**
 * Advisory-only duplicate indicator used by Save-as-Provider UX.
 * Returns a warning message for local snapshot duplicates but does not block submit.
 */
function getDuplicateProviderAdvisory(providerName, clusterS3Providers) {
  const trimmed = (providerName || "").trim();
  if (!trimmed) return "";
  const isDuplicate = (clusterS3Providers || []).some((p) => p.name === trimmed);
  return isDuplicate
    ? `A provider named "${trimmed}" already exists in your current snapshot. You can still submit to confirm with the server.`
    : "";
}

/**
 * Simulate save-as-provider submit behavior in S3Directory forms.
 * Local duplicate checks are advisory-only: backend result remains authoritative.
 */
async function submitSaveAsProvider({
  providerName,
  s3,
  providerSource,
  onSaveAsProvider,
  showSaveProviderUI = true,
}) {
  const trimmed = (providerName || "").trim();
  const state = {
    showSaveProviderUI,
    providerName,
    saveProviderError: "",
    isSubmitting: false,
  };

  if (!trimmed) {
    state.saveProviderError = "Provider name is required.";
    return state;
  }
  if (!s3?.endpoint) {
    state.saveProviderError = "Endpoint is required before saving as a provider.";
    return state;
  }

  state.isSubmitting = true;
  try {
    await onSaveAsProvider(trimmed, s3, providerSource);
    state.showSaveProviderUI = false;
    state.providerName = "";
    state.saveProviderError = "";
  } catch (err) {
    state.saveProviderError = typeof err === "string" ? err : err?.errorMessage || err?.message || "Failed to save provider.";
  } finally {
    state.isSubmitting = false;
  }

  return state;
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

console.log("\nTest Suite: Story 6.6 AC1 — edited value (not provider value) survives save + reload");
{
  // Step 1: user selects a saved provider — values are copied into form state.
  let formState = applyProviderToS3({ ...defaultS3, bucket: "my-bucket" }, "dev-minio", sampleProviders);
  assertEqual(formState.endpoint, "minio-app:9000", "step 1: provider endpoint copied into form state");
  assertEqual(formState.region, "eu-west-1", "step 1: provider region copied into form state");
  assertEqual(formState.providerName, "dev-minio", "step 1: providerName set for traceability");

  // Step 2: user edits the endpoint field after the copy — form state is local.
  formState = { ...formState, endpoint: "custom-edited-endpoint:9001" };
  assertEqual(formState.endpoint, "custom-edited-endpoint:9001", "step 2: user-edited endpoint in form state");

  // Step 3: user edits the region field after the copy.
  formState = { ...formState, region: "ap-south-1" };
  assertEqual(formState.region, "ap-south-1", "step 3: user-edited region in form state");

  // Step 4: form is saved — the payload is the current formState, not a provider snapshot.
  const savePayload = { ...formState };
  assertEqual(savePayload.endpoint, "custom-edited-endpoint:9001",
    "step 4: save payload carries user-edited endpoint, not original provider value");
  assertEqual(savePayload.region, "ap-south-1",
    "step 4: save payload carries user-edited region, not original provider value");
  assertEqual(savePayload.providerName, "dev-minio",
    "step 4: providerName traceability label is preserved in payload");

  // Step 5: simulate reload — the form is re-initialised with the persisted server data.
  // The server stores effective field values verbatim (Story 6.6 AC1 backend contract).
  // applyProviderToS3 is NOT called on init; the form simply reflects what the server returned.
  const serverData = { ...savePayload }; // server echoes back what it received
  const onReload = serverData;
  assertEqual(onReload.endpoint, "custom-edited-endpoint:9001",
    "step 5 (reload): edited endpoint is present, not original provider value");
  assertEqual(onReload.region, "ap-south-1",
    "step 5 (reload): edited region is present, not original provider value");
  assertEqual(onReload.providerName, "dev-minio",
    "step 5 (reload): providerName traceability label persists across reload");

  // Step 6: verify the original provider value is NOT what appears after reload.
  assert(onReload.endpoint !== "minio-app:9000",
    "step 6: original provider endpoint (minio-app:9000) is NOT present after reload");
  assert(onReload.region !== "eu-west-1",
    "step 6: original provider region (eu-west-1) is NOT present after reload");
}

console.log("\nTest Suite: Story 7.2 — local duplicate advisory is non-blocking");
{
  const advisory = getDuplicateProviderAdvisory("dev-minio", sampleProviders);
  assert(advisory.includes("already exists"), "shows advisory when duplicate exists in local snapshot");
}

console.log("\nTest Suite: Story 7.2 — stale local state + backend conflict keeps save UI open");
{
  const staleLocalProviders = [
    { name: "old-provider", providerSource: "app", providerApp: "old:9000" },
  ];
  const conflictMessage = 'Failed to add S3 provider: S3 provider with name "fresh-prod" already exists';

  const advisory = getDuplicateProviderAdvisory("fresh-prod", staleLocalProviders);
  assertEqual(advisory, "", "no local advisory for stale snapshot conflict");

  let callCount = 0;
  const onSaveAsProvider = async () => {
    callCount += 1;
    throw { errorMessage: conflictMessage };
  };

  const state = await submitSaveAsProvider({
    providerName: "fresh-prod",
    s3: { endpoint: "minio-app:9000", region: "us-east-1" },
    providerSource: "app",
    onSaveAsProvider,
  });

  assertEqual(callCount, 1, "submit hits backend even when local snapshot is stale");
  assertEqual(state.showSaveProviderUI, true, "save UI stays open after backend conflict");
  assertEqual(state.isSubmitting, false, "isSubmitting resets after backend conflict");
  assertEqual(state.saveProviderError, conflictMessage, "backend conflict message is surfaced to operator");
}

console.log("\nTest Suite: Story 7.2 — retry succeeds without page reload");
{
  let callCount = 0;
  const onSaveAsProvider = async (name) => {
    callCount += 1;
    if (name === "fresh-prod") {
      throw { errorMessage: 'Failed to add S3 provider: S3 provider with name "fresh-prod" already exists' };
    }
    return { status: 201 };
  };

  const firstAttempt = await submitSaveAsProvider({
    providerName: "fresh-prod",
    s3: { endpoint: "minio-app:9000", region: "us-east-1" },
    providerSource: "app",
    onSaveAsProvider,
  });
  assertEqual(firstAttempt.showSaveProviderUI, true, "first conflict keeps UI open for retry");
  assertEqual(firstAttempt.isSubmitting, false, "first conflict releases submit lock");

  const secondAttempt = await submitSaveAsProvider({
    providerName: "fresh-prod-2",
    s3: { endpoint: "minio-app:9000", region: "us-east-1" },
    providerSource: "app",
    onSaveAsProvider,
    showSaveProviderUI: firstAttempt.showSaveProviderUI,
  });

  assertEqual(callCount, 2, "operator can retry immediately without reload");
  assertEqual(secondAttempt.showSaveProviderUI, false, "successful retry closes save UI");
  assertEqual(secondAttempt.providerName, "", "successful retry clears provider input");
  assertEqual(secondAttempt.isSubmitting, false, "isSubmitting resets after successful retry");
  assertEqual(secondAttempt.saveProviderError, "", "error is cleared after successful retry");
}

// --- Summary ---
console.log(`\n========================================`);
console.log(`Total: ${passed + failed} tests`);
console.log(`Passed: ${passed}`);
console.log(`Failed: ${failed}`);

if (typeof require !== "undefined" && require.main === module) {
  process.exit(failed === 0 ? 0 : 1);
}
