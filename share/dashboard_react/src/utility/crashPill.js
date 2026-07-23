// Crash-pill signal — zero dependencies (unit-testable standalone with `node`).
//
// The crash pill reflects CURRENT cluster health, so it reads the HA state
// machine's live open states — NOT the durable crash archive (failoverHistory).
// repman re-evaluates these every monitoring tick and they self-clear when the
// condition resolves, so a cluster that crashed and recovered shows a calm pill
// without anyone rewriting history.
//
// The rejoin/crash health states are served at
// /api/clusters/{name}/topology/alerts (redux: cluster.clusterAlerts) as
// { errors, warnings, infos } of { number, desc, from }, and carry from: "REJOIN"
// (WARN0184/0185 analyzed-but-diverged, WARN0186/0187 rejoin needs operator /
// peer unreachable). Keying on failoverHistory[].rejoinResult was the original
// bug: a healthy cluster (belair) kept blinking on an old archived failure even
// though its live REJOIN state had already cleared.

// crashRejoinNeedsAttention reports whether the crash pill should alarm (blink +
// red): true iff the cluster currently has an OPEN rejoin/crash health state that
// NEEDS THE OPERATOR. WARN0189 ("reseed in progress") is a live, self-resolving
// restore — informational, surfaced by the dedicated Reseed pill/panel — so it is
// excluded here; otherwise every normal reseed made the crash pill blink red as if
// something were broken.
export const crashRejoinNeedsAttention = (clusterAlerts) => {
  if (!clusterAlerts) return false
  const needsAttention = (s) => s && s.from === 'REJOIN' && s.number !== 'WARN0189'
  return (clusterAlerts.warnings || []).some(needsAttention) ||
    (clusterAlerts.errors || []).some(needsAttention)
}
