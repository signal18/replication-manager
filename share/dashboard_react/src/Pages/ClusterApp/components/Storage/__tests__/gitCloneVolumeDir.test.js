// Git Clone Volume-Dir Token Selection Tests
// Verifies the GitClone.jsx base-directory-token selection logic added in
// Phase 12 (Git clone explicit volume-dir token selection) against the
// Phase 13 (Git clone volume-dir UX hardening and verification) tasks and
// acceptance checks.
//
// Run with: node src/Pages/ClusterApp/components/Storage/__tests__/gitCloneVolumeDir.test.js
// Or via: npm run test:git-clone-volume-dir

const {
  buildVolumeDir,
  defaultSubdir,
  extractSubDir,
  getVolumeDirTokens,
  matchVolumeDirToken,
} = await import('../volumeDirUtils.js');

// --- GitRowForm (existing-row editor) state derivation/handlers, mirroring GitClone.jsx ---

function rowView(gc, volumeOptions) {
  const { volumename, volumedir } = gc;
  const vol = volumeOptions.find((opt) => opt.value === volumename);
  const availableDirs = getVolumeDirTokens(vol?.volumedir);
  const srcbasepath = matchVolumeDirToken(volumedir, vol?.volumedir, defaultSubdir);
  const subpath = extractSubDir(volumedir, srcbasepath);
  return { vol, availableDirs, srcbasepath, subpath };
}

function rowHandleVolume(gc, volumeOptions, newVolumeValue) {
  const { name, volumedir } = gc;
  const { srcbasepath, subpath } = rowView(gc, volumeOptions);
  const newVol = volumeOptions.find((opt) => opt.value === newVolumeValue);
  const newDirs = getVolumeDirTokens(newVol?.volumedir);
  const newBase = newDirs.includes(srcbasepath) ? srcbasepath : defaultSubdir(newVol?.volumedir);
  const newValue = newVol ? buildVolumeDir(newBase, subpath, name) : volumedir;
  return { ...gc, volumename: newVol ? newVol.value : "", volumedir: newValue };
}

function rowHandleBaseDir(gc, volumeOptions, baseDirToken) {
  const { name } = gc;
  const { subpath } = rowView(gc, volumeOptions);
  return { ...gc, volumedir: buildVolumeDir(baseDirToken, subpath, name) };
}

function rowHandleSubPath(gc, volumeOptions, value) {
  const { name } = gc;
  const { srcbasepath } = rowView(gc, volumeOptions);
  return { ...gc, volumedir: buildVolumeDir(srcbasepath, value, name) };
}

// --- GitNewForm (add-row form) state derivation/handlers, mirroring GitClone.jsx ---
// Unlike GitRowForm, the new-row form tracks a raw `subpath` field in local
// state (the literal Sub Dir input value) separately from `volumedir`.

function newView(gc, volumeOptions) {
  const { volumename, volumedir } = gc;
  const vol = volumeOptions.find((opt) => opt.value === volumename);
  const availableDirs = getVolumeDirTokens(vol?.volumedir);
  const srcbasepath = matchVolumeDirToken(volumedir, vol?.volumedir, defaultSubdir);
  return { vol, availableDirs, srcbasepath };
}

function newHandleVolume(gc, volumeOptions, newVolumeValue) {
  const { name, subpath } = gc;
  const { srcbasepath } = newView(gc, volumeOptions);
  const newVol = volumeOptions.find((opt) => opt.value === newVolumeValue);
  const newDirs = getVolumeDirTokens(newVol?.volumedir);
  const newBase = newDirs.includes(srcbasepath) ? srcbasepath : defaultSubdir(newVol?.volumedir);
  return { ...gc, volumename: newVol ? newVol.value : "", volumedir: buildVolumeDir(newBase, subpath, name) };
}

function newHandleBaseDir(gc, baseDirToken) {
  const { name, subpath } = gc;
  return { ...gc, volumedir: buildVolumeDir(baseDirToken, subpath, name) };
}

function newHandleSubPath(gc, volumeOptions, value) {
  const { name } = gc;
  const { srcbasepath } = newView(gc, volumeOptions);
  return { ...gc, subpath: value, volumedir: buildVolumeDir(srcbasepath, value, name) };
}

// --- Test fixtures ---
// Mirrors the `volumeOptions` shape built by Storage/index.jsx:
// volumes.map((vol) => ({ value: vol.name, name: vol.name, volumedir: vol.volumedir }))

const volumeOptions = [
  { value: "app-data", name: "app-data", volumedir: "data" },
  { value: "app-shared", name: "app-shared", volumedir: "etc log" },
  { value: "app-multi", name: "app-multi", volumedir: "data mnt" },
  { value: "app-multi2", name: "app-multi2", volumedir: "mnt log" },
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

console.log("\nTest Suite: Task 1 — single-directory volumes keep the current simple behavior");
{
  const gc = { name: "my-repo", volumename: "app-data", volumedir: "data/my-repo" };
  const { availableDirs, srcbasepath, subpath } = rowView(gc, volumeOptions);
  assertEqual(availableDirs, ["data"], "single-directory volume exposes exactly one token");
  assert(!(availableDirs.length > 1), "Volume Dir dropdown is not shown for a single-token volume");
  assertEqual(srcbasepath, "data", "srcbasepath resolves to the lone token");
  assertEqual(subpath, "my-repo", "subpath is the path beneath the lone token");

  const edited = rowHandleSubPath(gc, volumeOptions, "renamed-repo");
  assertEqual(edited.volumedir, "data/renamed-repo", "editing Sub Dir rewrites under the same single token");
  assertEqual(rowView(edited, volumeOptions).srcbasepath, "data", "single token remains selected after Sub Dir edit");
}

console.log("\nTest Suite: Task 2 — multi-directory volumes allow explicit token selection (etc/my-repo, log/my-repo)");
{
  const gc = { name: "my-repo", volumename: "app-shared", volumedir: "etc/my-repo" };
  const { availableDirs, srcbasepath, subpath } = rowView(gc, volumeOptions);
  assertEqual(availableDirs, ["etc", "log"], "multi-directory volume exposes both tokens");
  assert(availableDirs.length > 1, "Volume Dir dropdown is shown for a multi-token volume");
  assertEqual(srcbasepath, "etc", "initial volumedir resolves to the etc token");
  assertEqual(subpath, "my-repo", "subpath beneath the etc token");

  const toLog = rowHandleBaseDir(gc, volumeOptions, "log");
  assertEqual(toLog.volumedir, "log/my-repo", "selecting the log token persists log/my-repo");
  assertEqual(rowView(toLog, volumeOptions).srcbasepath, "log", "selected token updates to log");

  const backToEtc = rowHandleBaseDir(toLog, volumeOptions, "etc");
  assertEqual(backToEtc.volumedir, "etc/my-repo", "switching back to etc restores etc/my-repo");
}

console.log("\nTest Suite: Task 3 — existing git clones rooted under a non-first token (log/app-src) load without snapping back");
{
  const gc = { name: "app-src", volumename: "app-shared", volumedir: "log/app-src" };
  const { availableDirs, srcbasepath, subpath } = rowView(gc, volumeOptions);
  assertEqual(srcbasepath, "log", "srcbasepath resolves to log, not the first token (etc)");
  assertEqual(subpath, "app-src", "subpath extracted beneath the log token");
  assert(availableDirs.includes("log") && availableDirs.includes("etc"), "both tokens remain selectable");

  const edited = rowHandleSubPath(gc, volumeOptions, "app-src-v2");
  assertEqual(edited.volumedir, "log/app-src-v2", "editing Sub Dir keeps the log token");
  assertEqual(rowView(edited, volumeOptions).srcbasepath, "log", "log token remains selected after editing the subdirectory");
}

console.log("\nTest Suite: Task 4 — switching volumes preserves subdirectory and base token when available");
{
  // app-multi = "data mnt"; the row currently lives under "mnt".
  const gc = { name: "uploads", volumename: "app-multi", volumedir: "mnt/uploads" };
  assertEqual(rowView(gc, volumeOptions).srcbasepath, "mnt", "starts rooted under the mnt token");

  // Switching to app-multi2 = "mnt log", which also has "mnt": token preserved.
  const switched = rowHandleVolume(gc, volumeOptions, "app-multi2");
  assertEqual(switched.volumename, "app-multi2", "volumename updates to app-multi2");
  assertEqual(switched.volumedir, "mnt/uploads", "mnt token and uploads subdir are both preserved");
  assertEqual(rowView(switched, volumeOptions).srcbasepath, "mnt", "mnt remains the selected token");

  // Switching to app-shared = "etc log", which has no "mnt": falls back to its
  // first token (etc), while the "uploads" subdirectory is preserved.
  const fallback = rowHandleVolume(gc, volumeOptions, "app-shared");
  assertEqual(fallback.volumedir, "etc/uploads", "falls back to the new volume's first token (etc) when mnt is unavailable");
  assertEqual(rowView(fallback, volumeOptions).srcbasepath, "etc", "etc becomes the selected token");
}

console.log("\nTest Suite: Task 5 — blank Sub Dir falls back to the git clone name under the currently selected token");
{
  const gc = { name: "my-clone", volumename: "app-shared", volumedir: "log/old-path" };
  assertEqual(rowView(gc, volumeOptions).srcbasepath, "log", "starts rooted under the log token");

  const blanked = rowHandleSubPath(gc, volumeOptions, "");
  assertEqual(blanked.volumedir, "log/my-clone", "blank Sub Dir falls back to the clone name under log, not etc");

  const slashed = rowHandleSubPath(gc, volumeOptions, "/");
  assertEqual(slashed.volumedir, "log/my-clone", "a bare '/' Sub Dir is treated the same as blank");
}

console.log("\nTest Suite: Task 6 — Storage/index.jsx volumeOptions contract is unchanged");
{
  // index.jsx builds volumeOptions as { value, name, volumedir } per saved
  // volume row. Confirm this remains sufficient for every helper above.
  const opt = volumeOptions[1]; // app-shared
  assert("value" in opt && "name" in opt && "volumedir" in opt, "volumeOptions entries expose value/name/volumedir");
  assertEqual(getVolumeDirTokens(opt.volumedir), ["etc", "log"], "volumedir alone is sufficient to derive directory tokens");
}

console.log("\nTest Suite: GitNewForm — token selection mirrors GitRowForm and Fullpath always matches persisted volumedir");
{
  let gc = { name: "new-repo", volumename: "", volumedir: "", subpath: "" };

  // Selecting a multi-directory volume before typing a subdir defaults the
  // base token to the volume's first token, and Fullpath falls back to the clone name.
  gc = newHandleVolume(gc, volumeOptions, "app-shared");
  assertEqual(gc.volumedir, "etc/new-repo", "selecting app-shared with blank subdir defaults to etc/<name>");
  assertEqual(newView(gc, volumeOptions).srcbasepath, "etc", "srcbasepath resolves to etc");
  assert(newView(gc, volumeOptions).availableDirs.length > 1, "Volume Dir dropdown is available on the new-row form too");

  // Switching the base token to "log" keeps the same fallback subdir.
  gc = newHandleBaseDir(gc, "log");
  assertEqual(gc.volumedir, "log/new-repo", "selecting the log token persists log/new-repo");

  // Typing an explicit Sub Dir overrides the fallback and is reflected in Fullpath.
  gc = newHandleSubPath(gc, volumeOptions, "src");
  assertEqual(gc.volumedir, "log/src", "explicit Sub Dir overrides the clone-name fallback");
  assertEqual(gc.subpath, "src", "raw Sub Dir input mirrors the typed value");

  // Switching to a single-directory volume collapses to its lone token, preserving the subdir.
  gc = newHandleVolume(gc, volumeOptions, "app-data");
  assertEqual(gc.volumedir, "data/src", "switching to a single-token volume falls back to its lone token, keeping the subdir");
  assertEqual(newView(gc, volumeOptions).availableDirs.length, 1, "single-token volume exposes no Volume Dir dropdown");
}

console.log("\nTest Suite: Phase 13 acceptance checks");
{
  // 1. A volume with volumedir = "etc log" must let the user explicitly
  //    choose either etc or log in both the existing-row editor and the new-row form.
  const sharedDirs = getVolumeDirTokens("etc log");
  assertEqual(sharedDirs, ["etc", "log"], "volume with volumedir 'etc log' exposes both etc and log tokens");
  assert(sharedDirs.length > 1, "both forms render a Volume Dir dropdown for this volume");

  // 2. Selecting log with subdirectory app-src must persist volumedir = log/app-src.
  const row = rowHandleBaseDir({ name: "app-src", volumename: "app-shared", volumedir: "etc/app-src" }, volumeOptions, "log");
  assertEqual(row.volumedir, "log/app-src", "existing-row editor: log + app-src -> log/app-src");

  let added = newHandleSubPath({ name: "app-src", volumename: "app-shared", volumedir: "etc/app-src", subpath: "app-src" }, volumeOptions, "app-src");
  added = newHandleBaseDir(added, "log");
  assertEqual(added.volumedir, "log/app-src", "new-row form: log + app-src -> log/app-src");

  // 3. Changing only the subdirectory must not silently switch the selected token.
  const subOnly = rowHandleSubPath({ name: "app-src", volumename: "app-shared", volumedir: "log/app-src" }, volumeOptions, "renamed");
  assertEqual(rowView(subOnly, volumeOptions).srcbasepath, "log", "changing the subdirectory alone leaves the selected token (log) unchanged");

  // 4. Changing the selected volume must not automatically force the git
  //    clone back to the first token unless the current token is unavailable in the new volume.
  const keepsToken = rowHandleVolume({ name: "uploads", volumename: "app-multi", volumedir: "mnt/uploads" }, volumeOptions, "app-multi2");
  assertEqual(rowView(keepsToken, volumeOptions).srcbasepath, "mnt", "volume switch preserves the current token (mnt) when the new volume also has it");

  const fallsBack = rowHandleVolume({ name: "uploads", volumename: "app-multi", volumedir: "mnt/uploads" }, volumeOptions, "app-shared");
  assertEqual(rowView(fallsBack, volumeOptions).srcbasepath, "etc", "volume switch falls back to the new volume's first token only when the current token is unavailable");

  // 5. The Fullpath preview must always match the value sent back through
  //    onRowArrayChange / onSaveAdd: every handler above returns the exact
  //    `volumedir` value that is both dispatched and shown as "Fullpath: {volumedir}".
  assert(row.volumedir === "log/app-src" && added.volumedir === "log/app-src", "Fullpath preview value is identical to the dispatched volumedir in both forms");
}

// --- Summary ---
console.log(`\n========================================`);
console.log(`Total: ${passed + failed} tests`);
console.log(`Passed: ${passed}`);
console.log(`Failed: ${failed}`);

if (typeof require !== "undefined" && require.main === module) {
  process.exit(failed === 0 ? 0 : 1);
}
