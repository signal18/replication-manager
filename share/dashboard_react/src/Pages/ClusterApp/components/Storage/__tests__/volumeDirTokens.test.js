// Volume Directory Tokenization Logic Tests
// Tests the pure helper functions added in Phase 8 (Frontend compatibility) of the
// app volume migration: parsing/merging-aware handling of Volume.volumedir as a
// whitespace-separated list of top-level directories, mirroring the Go helpers in
// config/deployment.go (Volume.GetVolumeDirs, Volume.DefaultSubdir).
// Run with: node src/Pages/ClusterApp/components/Storage/__tests__/volumeDirTokens.test.js
// Or via: npm run test:volume-dir-tokens

// --- Logic copied from GitClone.jsx / Volume.jsx / Paths.jsx for isolated testing ---

const getVolumeDirTokens = (volumedir) =>
  typeof volumedir === "string" ? volumedir.split(/\s+/).filter(Boolean) : [];

const defaultSubdir = (volumedir) => getVolumeDirTokens(volumedir)[0] || "";

const matchVolumeDirToken = (path, volumedir) => {
  const dirs = getVolumeDirTokens(volumedir);
  const match = dirs.find((dir) => path === dir || path.startsWith(`${dir}/`));
  return match || defaultSubdir(volumedir);
};

// --- Logic copied from Paths.jsx for isolated testing ---

const normalizeSubPath = (value) => {
  const raw = typeof value === 'string' ? value.trim() : '';
  if (!raw || raw === '/' || raw === '.') return '/';
  return raw.startsWith('/') ? raw : `/${raw}`;
};

const composeSourcePath = (srcbasepath, subpath) => {
  const normalizedSubPath = normalizeSubPath(subpath);
  if (!srcbasepath) {
    return normalizedSubPath === '/' ? '.' : normalizedSubPath.slice(1);
  }
  if (normalizedSubPath === '/') return srcbasepath;
  return `${srcbasepath}${normalizedSubPath}`.replace('//', '/');
};

// --- Test Runner ---

let passed = 0;
let failed = 0;

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

console.log("\nTest Suite: getVolumeDirTokens — splits whitespace-separated VolumeDir");
{
  assertEqual(getVolumeDirTokens("data"), ["data"], "single token");
  assertEqual(getVolumeDirTokens("data mnt"), ["data", "mnt"], "merged two-token row");
  assertEqual(getVolumeDirTokens("etc  log\tvar data"), ["etc", "log", "var", "data"], "collapses runs of whitespace/tabs");
  assertEqual(getVolumeDirTokens(""), [], "empty string yields no tokens");
  assertEqual(getVolumeDirTokens(undefined), [], "undefined yields no tokens");
  assertEqual(getVolumeDirTokens(null), [], "null yields no tokens");
}

console.log("\nTest Suite: defaultSubdir — mirrors config.Volume.DefaultSubdir()");
{
  assertEqual(defaultSubdir("data"), "data", "single-token row returns that token");
  assertEqual(defaultSubdir("data mnt"), "data", "merged row returns first token");
  assertEqual(defaultSubdir(""), "", "empty VolumeDir returns empty string");
  assertEqual(defaultSubdir(undefined), "", "undefined VolumeDir returns empty string");
}

console.log("\nTest Suite: matchVolumeDirToken — finds which directory token a path is rooted under");
{
  assertEqual(matchVolumeDirToken("data/myrepo", "data mnt"), "data", "path under first token matches that token");
  assertEqual(matchVolumeDirToken("mnt/mybucket", "data mnt"), "mnt", "path under second token matches that token");
  assertEqual(matchVolumeDirToken("mnt", "data mnt"), "mnt", "path equal to a token matches that token exactly");
  assertEqual(matchVolumeDirToken("mntlogs/x", "data mnt"), "data", "no false-positive on a token that is merely a prefix; falls back to default");
  assertEqual(matchVolumeDirToken("legacy/path", "data mnt"), "data", "legacy path predating multi-dir merge falls back to default subdir");
  assertEqual(matchVolumeDirToken("data", "data"), "data", "single-token volume matches its only token");
}

console.log("\nTest Suite: normalizeSubPath — round-trips srcpath display values");
{
  assertEqual(normalizeSubPath(""), "/", "empty string normalizes to root");
  assertEqual(normalizeSubPath("."), "/", "dot normalizes to root");
  assertEqual(normalizeSubPath("/"), "/", "slash stays root");
  assertEqual(normalizeSubPath("data"), "/data", "relative path gets leading slash");
  assertEqual(normalizeSubPath("/data/myrepo"), "/data/myrepo", "already-absolute path is unchanged");
  assertEqual(normalizeSubPath("  data  "), "/data", "surrounding whitespace is trimmed before normalizing");
}

console.log("\nTest Suite: composeSourcePath — volume-type sources (no single base path)");
{
  assertEqual(composeSourcePath("", "/"), ".", "root subpath composes to '.' (pool disk root) when there is no base path");
  assertEqual(composeSourcePath("", "/data/myrepo"), "data/myrepo", "absolute subpath becomes relative srcpath when there is no base path");
  assertEqual(composeSourcePath("", "/mnt"), "mnt", "single top-level directory becomes a relative srcpath");
}

console.log("\nTest Suite: composeSourcePath — git/s3 sources with a single base path");
{
  assertEqual(composeSourcePath("/repo-data", "/"), "/repo-data", "root subpath resolves to the base path itself");
  assertEqual(composeSourcePath("/repo-data", "/sub"), "/repo-data/sub", "subpath is appended to the base path");
}

// --- Summary ---
console.log(`\n========================================`);
console.log(`Total: ${passed + failed} tests`);
console.log(`Passed: ${passed}`);
console.log(`Failed: ${failed}`);

if (typeof require !== "undefined" && require.main === module) {
  process.exit(failed === 0 ? 0 : 1);
}
