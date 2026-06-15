// S3 Mount Volume-Dir Placement Tests
// Verifies the S3Directory.jsx explicit V2 S3 mount placement logic added in
// Phase 14 (V2 explicit S3 mount placement): selecting a saved volume row, a
// directory token within that row (mnt-biased default suggestion only), and
// a relative subdirectory beneath that token, with a Fullpath preview that
// always matches the persisted volumedir.
//
// Run with: node src/Pages/ClusterApp/components/Storage/__tests__/s3VolumeDir.test.js
// Or via: npm run test:s3-volume-dir

const {
  buildVolumeDir,
  defaultS3Subdir,
  extractSubDir,
  getVolumeDirTokens,
  matchVolumeDirToken,
} = await import('../volumeDirUtils.js');

// --- S3DirectoryRowForm (existing-mount editor) state derivation/handlers ---

function rowView(s3, volumeOptions) {
  const { volumename, volumedir } = s3;
  const vol = volumeOptions.find((opt) => opt.value === volumename);
  const availableDirs = getVolumeDirTokens(vol?.volumedir);
  const srcbasepath = matchVolumeDirToken(volumedir, vol?.volumedir, defaultS3Subdir);
  const subpath = extractSubDir(volumedir, srcbasepath);
  return { vol, availableDirs, srcbasepath, subpath };
}

function rowHandleVolume(s3, volumeOptions, newVolumeValue) {
  const { name, volumedir } = s3;
  const { srcbasepath, subpath } = rowView(s3, volumeOptions);
  const newVol = volumeOptions.find((opt) => opt.value === newVolumeValue);
  const newDirs = getVolumeDirTokens(newVol?.volumedir);
  const newBase = newDirs.includes(srcbasepath) ? srcbasepath : defaultS3Subdir(newVol?.volumedir);
  const newValue = newVol ? buildVolumeDir(newBase, subpath, name) : volumedir;
  return { ...s3, volumename: newVol ? newVol.value : "", volumedir: newValue };
}

function rowHandleBaseDir(s3, volumeOptions, baseDirToken) {
  const { name } = s3;
  const { subpath } = rowView(s3, volumeOptions);
  return { ...s3, volumedir: buildVolumeDir(baseDirToken, subpath, name) };
}

function rowHandleSubPath(s3, volumeOptions, value) {
  const { name } = s3;
  const { srcbasepath } = rowView(s3, volumeOptions);
  return { ...s3, volumedir: buildVolumeDir(srcbasepath, value, name) };
}

// --- S3DirectoryNewForm (add-mount form) state derivation/handlers ---
// Unlike S3DirectoryRowForm, the new-mount form has no `name` field yet, so a
// blank/"/" Sub Dir persists volumedir as the bare selected directory token
// (Phase 16; resolved server-side by appending the generated mount name to
// that token). When no directory token is selected at all, volumedir stays
// "" (fully unspecified, resolved server-side via
// Volume.S3MountSubdir() + the generated mount name).

function newView(s3, volumeOptions) {
  const { volumename, volumedir } = s3;
  const vol = volumeOptions.find((opt) => opt.value === volumename);
  const availableDirs = getVolumeDirTokens(vol?.volumedir);
  const srcbasepath = matchVolumeDirToken(volumedir, vol?.volumedir, defaultS3Subdir);
  return { vol, availableDirs, srcbasepath };
}

function newHandleVolume(s3, volumeOptions, newVolumeValue) {
  const { subpath } = s3;
  const { srcbasepath } = newView(s3, volumeOptions);
  const newVol = volumeOptions.find((opt) => opt.value === newVolumeValue);
  const newDirs = getVolumeDirTokens(newVol?.volumedir);
  const newBase = newDirs.includes(srcbasepath) ? srcbasepath : defaultS3Subdir(newVol?.volumedir);
  return { ...s3, volumename: newVol ? newVol.value : "", volumedir: buildVolumeDir(newBase, subpath, "", { preserveBareToken: true }) };
}

function newHandleBaseDir(s3, baseDirToken) {
  const { subpath } = s3;
  return { ...s3, volumedir: buildVolumeDir(baseDirToken, subpath, "", { preserveBareToken: true }) };
}

function newHandleSubPath(s3, volumeOptions, value) {
  const { srcbasepath } = newView(s3, volumeOptions);
  return { ...s3, subpath: value, volumedir: buildVolumeDir(srcbasepath, value, "", { preserveBareToken: true }) };
}

// --- Test fixtures ---
// Mirrors the `volumeOptions` shape built by Storage/index.jsx:
// volumes.map((vol) => ({ value: vol.name, name: vol.name, volumedir: vol.volumedir }))

const volumeOptions = [
  { value: "app-data", name: "app-data", volumedir: "data" },
  { value: "app-shared", name: "app-shared", volumedir: "etc log" },
  { value: "app-multi", name: "app-multi", volumedir: "data mnt" },
  { value: "app-onlymnt", name: "app-onlymnt", volumedir: "mnt" },
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

console.log("\nTest Suite: defaultS3Subdir — mnt is the default suggestion, not a hard rule");
{
  assertEqual(defaultS3Subdir("data mnt"), "mnt", "a volume exposing mnt suggests mnt, regardless of token order");
  assertEqual(defaultS3Subdir("mnt log"), "mnt", "mnt is preferred even when it is not the first token");
  assertEqual(defaultS3Subdir("data log"), "data", "a volume without mnt falls back to its first token");
  assertEqual(defaultS3Subdir(""), "", "an empty volumedir yields no suggestion");
  assertEqual(defaultS3Subdir(undefined), "", "an undefined volumedir (no volume selected) yields no suggestion");
}

console.log("\nTest Suite: matchVolumeDirToken — exact token match wins, mnt-biased default is only the fallback");
{
  assertEqual(matchVolumeDirToken("data/uploads", "data mnt", defaultS3Subdir), "data", "a path rooted under 'data' resolves to 'data', not the mnt suggestion");
  assertEqual(matchVolumeDirToken("mnt/uploads", "data mnt", defaultS3Subdir), "mnt", "a path rooted under 'mnt' resolves to 'mnt'");
  assertEqual(matchVolumeDirToken("", "data mnt", defaultS3Subdir), "mnt", "an unplaced mount (blank path) on a volume with mnt suggests mnt");
  assertEqual(matchVolumeDirToken("", "data log", defaultS3Subdir), "data", "an unplaced mount on a volume without mnt suggests the first token");
  assertEqual(matchVolumeDirToken("legacy-bucket-name", "data mnt", defaultS3Subdir), "mnt", "a legacy path with no '/' (predating multi-dir merge) falls back to the mnt suggestion");
}

console.log("\nTest Suite: extractSubDir — splits a persisted volumedir into token + subdirectory");
{
  assertEqual(extractSubDir("mnt/my-bucket", "mnt"), "my-bucket", "subdirectory beneath the mnt token");
  assertEqual(extractSubDir("mnt", "mnt"), "", "a volumedir equal to the token has an empty subdirectory");
  assertEqual(extractSubDir("legacy-bucket-name", ""), "legacy-bucket-name", "a falsy base token returns the full legacy value unchanged");
}

console.log("\nTest Suite: buildVolumeDir — Task 1/Phase 16, blank placement persists the selected token, deferring only when none is selected");
{
  assertEqual(buildVolumeDir("mnt", "", "", { preserveBareToken: true }), "mnt", "blank Sub Dir with no mount-name fallback (Add form) persists the bare selected token");
  assertEqual(buildVolumeDir("mnt", "/", "", { preserveBareToken: true }), "mnt", "a bare '/' Sub Dir is treated the same as blank");
  assertEqual(buildVolumeDir("", "", "", { preserveBareToken: true }), "", "no token selected and no mount-name fallback fully defers volumedir to the backend");
  assertEqual(buildVolumeDir("mnt", "", "my-mount"), "mnt/my-mount", "blank Sub Dir on an existing row falls back to the mount's own name");
  assertEqual(buildVolumeDir("mnt", "uploads", ""), "mnt/uploads", "an explicit Sub Dir always wins, even with no mount-name fallback");
  assertEqual(buildVolumeDir("", "uploads", ""), "uploads", "no base token still persists the explicit subdirectory");
}

console.log("\nTest Suite: S3DirectoryRowForm — single-token volume (Basepath text, no dropdown)");
{
  const s3 = { name: "my-bucket-mount", volumename: "app-onlymnt", volumedir: "mnt/my-bucket-mount" };
  const { availableDirs, srcbasepath, subpath } = rowView(s3, volumeOptions);
  assertEqual(availableDirs, ["mnt"], "app-onlymnt exposes exactly one token");
  assert(!(availableDirs.length > 1), "Volume Dir dropdown is not shown for a single-token volume");
  assertEqual(srcbasepath, "mnt", "srcbasepath resolves to the lone mnt token");
  assertEqual(subpath, "my-bucket-mount", "subpath is the path beneath the lone token");

  const edited = rowHandleSubPath(s3, volumeOptions, "renamed-mount");
  assertEqual(edited.volumedir, "mnt/renamed-mount", "editing Sub Dir rewrites under the same single token");
  assertEqual(rowView(edited, volumeOptions).srcbasepath, "mnt", "single token remains selected after Sub Dir edit");
}

console.log("\nTest Suite: S3DirectoryRowForm — multi-token volume allows explicit, non-mnt placement (Task 4)");
{
  // app-multi = "data mnt"; the mount currently lives under "mnt" (the default suggestion).
  const s3 = { name: "uploads", volumename: "app-multi", volumedir: "mnt/uploads" };
  const view = rowView(s3, volumeOptions);
  assertEqual(view.availableDirs, ["data", "mnt"], "app-multi exposes both data and mnt tokens");
  assert(view.availableDirs.length > 1, "Volume Dir dropdown is shown for a multi-token volume");
  assertEqual(view.srcbasepath, "mnt", "initially placed under the mnt suggestion");

  // The user explicitly overrides the suggestion to "data" - mnt is only a default, not a hard rule.
  const toData = rowHandleBaseDir(s3, volumeOptions, "data");
  assertEqual(toData.volumedir, "data/uploads", "explicitly selecting 'data' persists data/uploads, overriding the mnt suggestion");
  assertEqual(rowView(toData, volumeOptions).srcbasepath, "data", "selected token updates to data and is not snapped back to mnt");

  const backToMnt = rowHandleBaseDir(toData, volumeOptions, "mnt");
  assertEqual(backToMnt.volumedir, "mnt/uploads", "switching back to mnt restores mnt/uploads");
}

console.log("\nTest Suite: S3DirectoryRowForm — switching volumes preserves subdir, mnt-biased fallback when unavailable");
{
  // app-multi = "data mnt"; mount lives under "data".
  const s3 = { name: "files", volumename: "app-multi", volumedir: "data/files" };
  assertEqual(rowView(s3, volumeOptions).srcbasepath, "data", "starts rooted under the data token");

  // Switching to app-onlymnt = "mnt": data is unavailable, falls back to the mnt-biased default.
  const toOnlyMnt = rowHandleVolume(s3, volumeOptions, "app-onlymnt");
  assertEqual(toOnlyMnt.volumename, "app-onlymnt", "volumename updates to app-onlymnt");
  assertEqual(toOnlyMnt.volumedir, "mnt/files", "falls back to the new volume's mnt token, preserving the 'files' subdir");

  // Switching to app-shared = "etc log" (no mnt at all): falls back to the first token.
  const toShared = rowHandleVolume(s3, volumeOptions, "app-shared");
  assertEqual(toShared.volumedir, "etc/files", "falls back to the new volume's first token (etc) when neither the current token nor mnt is available");

  // Switching between app-multi and app-onlymnt while rooted under mnt preserves the mnt token directly.
  const mntRooted = { name: "uploads", volumename: "app-multi", volumedir: "mnt/uploads" };
  const stillMnt = rowHandleVolume(mntRooted, volumeOptions, "app-onlymnt");
  assertEqual(stillMnt.volumedir, "mnt/uploads", "mnt token is preserved directly when the new volume also exposes it");
}

console.log("\nTest Suite: S3DirectoryRowForm — legacy unplaced mount (volumename blank, volumedir is a bare name)");
{
  // Pre-Phase-14 autofilled mounts have volumename="" and volumedir set to just the mount name (no '/').
  const s3 = { name: "legacy-bucket-mount", volumename: "", volumedir: "legacy-bucket-mount" };
  const { vol, availableDirs, srcbasepath, subpath } = rowView(s3, volumeOptions);
  assertEqual(vol, undefined, "no saved volume row matches a blank volumename");
  assertEqual(availableDirs, [], "no directory tokens are available without a resolved volume");
  assertEqual(srcbasepath, "", "srcbasepath is empty for an unresolved volume");
  assertEqual(subpath, "legacy-bucket-mount", "the full legacy volumedir is shown as the subpath, unchanged");
}

console.log("\nTest Suite: S3DirectoryNewForm — Task 1, unspecified placement defers to backend autofill");
{
  let s3 = { volumename: "", volumedir: "", subpath: "" };
  const view = newView(s3, volumeOptions);
  assertEqual(view.vol, undefined, "no volume selected initially");
  assertEqual(view.srcbasepath, "", "no base token suggested without a selected volume");

  // Selecting a multi-token volume with mnt before typing a Sub Dir persists
  // volumedir as the bare suggested token "mnt" (server appends the
  // generated mount name, Phase 16).
  s3 = newHandleVolume(s3, volumeOptions, "app-multi");
  assertEqual(s3.volumename, "app-multi", "volumename updates to app-multi");
  assertEqual(s3.volumedir, "mnt", "blank Sub Dir persists the mnt-biased suggested token as a bare value");
  assertEqual(newView(s3, volumeOptions).srcbasepath, "mnt", "the suggested base token is the mnt-biased default (mnt)");
  assert(newView(s3, volumeOptions).availableDirs.length > 1, "Volume Dir dropdown is available on the new-mount form too");
}

console.log("\nTest Suite: S3DirectoryNewForm — Phase 16, explicit non-default token selection is preserved with a blank Sub Dir");
{
  // app-multi = "data mnt"; selecting it before typing a Sub Dir suggests "mnt".
  let s3 = { volumename: "", volumedir: "", subpath: "" };
  s3 = newHandleVolume(s3, volumeOptions, "app-multi");
  assertEqual(newView(s3, volumeOptions).srcbasepath, "mnt", "initially suggests the mnt-biased default token");

  // The user explicitly overrides the suggestion to "data" without typing a Sub Dir.
  s3 = newHandleBaseDir(s3, "data");
  assertEqual(s3.volumedir, "data", "explicitly selecting 'data' persists the bare 'data' token, not '' or 'mnt'");
  assertEqual(newView(s3, volumeOptions).srcbasepath, "data", "the dropdown does not snap back to the mnt suggestion");

  // Switching back to mnt is also preserved as a bare token.
  s3 = newHandleBaseDir(s3, "mnt");
  assertEqual(s3.volumedir, "mnt", "switching back to mnt persists the bare 'mnt' token");
  assertEqual(newView(s3, volumeOptions).srcbasepath, "mnt", "srcbasepath tracks the explicit selection, not just the suggestion");
}

console.log("\nTest Suite: S3DirectoryNewForm — Task 2/3, explicit placement becomes first-class once Sub Dir is set");
{
  let s3 = { volumename: "app-multi", volumedir: "", subpath: "" };

  // Typing an explicit Sub Dir resolves volumedir under the suggested (mnt) token.
  s3 = newHandleSubPath(s3, volumeOptions, "my-mount");
  assertEqual(s3.volumedir, "mnt/my-mount", "explicit Sub Dir is placed under the mnt-biased default token");
  assertEqual(s3.subpath, "my-mount", "raw Sub Dir input mirrors the typed value");

  // The user can still override the base token explicitly (mnt is only a suggestion).
  s3 = newHandleBaseDir(s3, "data");
  assertEqual(s3.volumedir, "data/my-mount", "explicitly selecting 'data' persists data/my-mount, overriding the mnt suggestion");

  // Switching to a single-token volume (app-onlymnt = "mnt") collapses to its lone token, preserving the subdir.
  s3 = newHandleVolume(s3, volumeOptions, "app-onlymnt");
  assertEqual(s3.volumedir, "mnt/my-mount", "switching to a single-token volume falls back to its lone token, keeping the subdir");
  assertEqual(newView(s3, volumeOptions).availableDirs.length, 1, "single-token volume exposes no Volume Dir dropdown");
}

console.log("\nTest Suite: Fullpath preview always matches the persisted volumedir");
{
  const row = rowHandleBaseDir({ name: "app-src", volumename: "app-multi", volumedir: "mnt/app-src" }, volumeOptions, "data");
  assertEqual(row.volumedir, "data/app-src", "row form: explicit placement -> data/app-src");

  let added = { volumename: "app-multi", volumedir: "", subpath: "" };
  added = newHandleSubPath(added, volumeOptions, "app-src");
  added = newHandleBaseDir(added, "data");
  assertEqual(added.volumedir, "data/app-src", "new form: explicit placement -> data/app-src");

  assert(row.volumedir === "data/app-src" && added.volumedir === "data/app-src", "Fullpath preview value is identical to the persisted volumedir in both forms");
}

// --- Summary ---
console.log(`\n========================================`);
console.log(`Total: ${passed + failed} tests`);
console.log(`Passed: ${passed}`);
console.log(`Failed: ${failed}`);

if (typeof require !== "undefined" && require.main === module) {
  process.exit(failed === 0 ? 0 : 1);
}
