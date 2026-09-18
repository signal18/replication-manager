# Split-Brain Orchestration Diagrams

Procedure for the three arbitration diagrams — **Free**, **Support**, and
**Lose–Lose (symmetric split)** — that illustrate how replication-manager handles a
two-datacenter partition.

These diagrams are used in **two** places, so keep them in sync:
- the arbitration documentation (docs.signal18.io → Architecture → Arbitration
  §4.7.1.6), and
- the demo video, as the explainer / intro slides — see
  [INCRUST_DEMO_VIDEO.md](./INCRUST_DEMO_VIDEO.md).

---

## 1. Single source of truth

Everything is generated from one **self-contained HTML generator**:

```
doc/implementation/cluster/split-brain-orchestration.html
```

It contains the three `<svg viewBox="0 0 1120 560">` diagrams plus an inline
`<style>` with the whole design system. No external assets, no build step —
open it in a browser to view/edit. A companion reference lives at
`doc/implementation/cluster/SPLIT_BRAIN_ORCHESTRATION.md`.

### Design system (CSS custom properties, theme-aware light/dark)
- `--accent` teal = **repman / proxy / FTWRL fence** color
- `--majority` green = the winning side (NEW MASTER)
- `--minority` amber = the yielding side (OLD MASTER, read-only)
- `--cut` red = severed links / NETWORK PARTITION
- `--arb` indigo = the arbitrator
- `--ok` blue = healthy link

### What the three frames show
- **Free** — weak arbiter (a 3rd repman): the isolated old master piles
  unrecoverable binlogs → restore from backup.
- **Support** — dedicated arbitrator: old master **FTWRL-fenced** (teal broken
  link), single flashback-able delta → rejoin in seconds.
- **Lose–Lose** — symmetric split, both sides still reach the arbitrator → repman
  fences **both** masters (write outage, but zero divergence).

Shared elements: three DC "clouds", per-DC **proxy** boxes, **repman** boxes (teal),
the arbitrator, the **FTWRL** fence (teal cut on the minority side, blue healthy on
the majority side), and a single horizontal **NETWORK PARTITION** cross between
DC1↔DC2.

---

## 2. Edit → verify

1. Edit the SVG(s) in `split-brain-orchestration.html`.
2. Render-check with Playwright (headless is fine — these are pure HTML/SVG, no
   `<video>`): `page.goto('file://…/split-brain-orchestration.html')` →
   `page.screenshot({fullPage:true})`, and eyeball the PNG.
3. Iterate. Keep both light and dark tokens working.

---

## 3. Export — two targets

### a) Standalone theme-aware SVGs (for the repman docs)
Each `<svg>` in the generator relies on the page-level `<style>`, so to stand alone
each needs its **own** embedded style + a panel background. Extract each `<svg>`,
inject the full `<style>` and a background `<rect>`, add the SVG namespace, and pad
the viewBox:

```py
# pseudo: for each <svg> block in the generator html
svg = svg.replace('<svg ', '<svg xmlns="http://www.w3.org/2000/svg" ', 1)
svg = svg.replace('viewBox="0 0 1120 560"', 'viewBox="-18 -18 1156 596"', 1)
inject = f'<style>{css}</style><rect x="-18" y="-18" width="1156" height="596" rx="18" fill="var(--panel)"/>'
svg = re.sub(r'(<svg[^>]*>)', lambda m: m.group(1)+inject, svg, count=1)
```
→ write to `doc/implementation/cluster/img/sbs-{free,support,lose-lose}.svg`.

### b) PNGs (for the Grav docs site + the video slides)
Grav uses PNG (`/images/*.png`) and PNG is safest for slides/PDF. Render each
standalone SVG via Playwright at 1600×900 (`deviceScaleFactor:2`) and screenshot:
→ `docs.signal18.io/pages/images/arbitration{free,support,loselose}.png`,
referenced from the arbitration overview as `![...](/images/arbitration*.png)`.

> Caveat: an SVG embedded via `<img>` follows the *OS* color scheme, not a site's
> light/dark toggle — so on GitHub/Grav it renders in the viewer's OS theme.

---

## 4. Where each output lives

| Output | Location | Consumed by |
|--------|----------|-------------|
| Generator HTML | `doc/implementation/cluster/split-brain-orchestration.html` | source of truth |
| Standalone SVGs | `doc/implementation/cluster/img/sbs-*.svg` | repman implementation doc |
| PNGs | `docs.signal18.io/pages/images/arbitration*.png` | arbitration docs §4.7.1.6 |
| Slide frames | rendered into the demo video | see [INCRUST_DEMO_VIDEO.md](./INCRUST_DEMO_VIDEO.md) |

**When the diagram changes, re-export all three targets** so the docs and the video
stay consistent.
