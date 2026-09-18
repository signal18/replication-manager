# Producing an "Incrust" Demo Video

Procedure for building a narrated marketing video of the replication-manager GUI,
where live demo footage plays full-screen and a small **inset ("incrust")** panel
in the bottom-right shows the current topic, section labels, and (optionally) a
countdown — with a synthetic mouse cursor and a human voiceover.

> The intro / explainer slides reuse the split-brain orchestration diagrams —
> see [ORCHESTRATION_DIAGRAMS.md](./ORCHESTRATION_DIAGRAMS.md). When those diagrams
> change, the video's slides must be re-exported from them.

> **Secrets stay out of this repo.** The GUI URL, login, and cluster names are
> environment-specific and are **not** committed here — supply them at runtime via
> env vars / a local token file. Keep credentials in a private local note, never in
> `doc/`.

---

## 1. Why it's built this way (constraints that shaped the method)

- **Headless Chromium cannot decode/paint a `<video>` element** — it renders black.
  So we never composite the demo footage *inside* a browser. Instead the demo is
  recorded as its own video, and any animated overlay (inset, labels, countdown) is
  a **separate HTML page** recorded on a solid **chroma-key** background, then keyed
  over the footage with ffmpeg.
- **Headless has no real mouse pointer** — recordings show no cursor. We inject a
  **synthetic cursor** (a styled `div`) and animate it to each target.
- **Playwright synthetic clicks (`el.click()`) don't drive some Chakra components**
  (accordion headers). Use **trusted input** (`page.mouse.click(x,y)` /
  `locator.click()`) for those; tab navigation may still need a `scrollIntoView` +
  synthetic click helper.
- **ffmpeg from a static build**, not Homebrew — on older macOS there is no bottle
  and `brew install ffmpeg` compiles from source (~1h). Download a prebuilt static
  binary instead.
- **CPU contention ruins voice takes.** Recording footage or encoding while the
  narrator records their microphone causes audio dropouts. **Never run
  Playwright/ffmpeg while the mic is live** — record voice on an idle machine, then
  process.

---

## 2. Tooling setup

**Static ffmpeg** (no compile):
```bash
mkdir -p ./bin
curl -sL -o ff.zip "https://evermeet.cx/ffmpeg/getrelease/ffmpeg/zip"
unzip -o ff.zip -d ./bin && chmod +x ./bin/ffmpeg
```

**Playwright** — run recording scripts from a directory that has
`node_modules/playwright` installed.

**Auth** — obtain a session token from the GUI's login API and inject it as
`localStorage.user_token` via `addInitScript`. Tokens are short-lived; re-mint per
session. Provide `BASE` (GUI URL) and the token file as env vars — do not hard-code.

---

## 3. Record the demo footage (Playwright + synthetic cursor)

Key building blocks (see the working scripts kept in the local
`REPMAN/video/scripts/` cheatsheet):

- Launch headless, `viewport 1600x900`, `recordVideo` on the context.
- `addInitScript` #1: `localStorage.setItem('user_token', <token>)`.
- `addInitScript` #2: inject the cursor — a fixed `#fakecursor` div with a CSS
  `transition` on `left/top`, plus a keyframed `.rip` click-ripple element.
- Helpers:
  - `rectOf(text, role)` → scrolls the element into view, returns its center.
  - `move(x,y)` → sets the cursor div's position, waits for the ~0.6s glide.
  - `moveClick(x,y)` → glide, spawn a ripple, then **`page.mouse.click(x,y)`**
    (trusted).
- Pace it **relaxed** — dwell 10–15s per screen with natural pauses; this reads as
  human, and gives room for narration.
- Optional top **caption banner** (a fixed dark bar) to label each phase; matches
  the pre-recorded arbitration footage which already carries banners.

Gotchas learned:
- Tab (e.g. "Maintenance") — use the scrollIntoView + synthetic-click helper.
- Accordion section headers (backups snapshots, database jobs) — use
  `page.mouse.click` on the header row, not the text node.
- A card's settings **gear** (far-right of a section header) can deep-link to the
  relevant Settings section — good for "jump to the config" beats.
- Pages can load slowly; **sync on real elements** (`waitForSelector('text=…')`)
  rather than fixed sleeps, and expect the final clip to run longer than scripted.

---

## 4. Build the overlay (inset + labels + countdown)

An HTML page on a **magenta `#FF00FF`** background (a color absent from the GUI):

- A full-screen **intro** slide that zooms in from the inset corner, then shrinks
  to the inset (CSS `opacity`+`transform`, `transform-origin` bottom-right).
- A **bottom-right inset** card: logo, section label, and either a static line or
  a few **slowly cycling** points. With a voiceover, keep it **minimal** — the
  voice carries the detail; on-screen text competing with narration reads as "too
  much, too fast."
- Optional footer **countdown** to the next section.

Record it with Playwright `recordVideo` for the section's duration.

---

## 5. Composite (chroma-key overlay over footage) + mux voice

```bash
./bin/ffmpeg -i FOOTAGE.webm -i OVERLAY.webm -i VOICE.m4a -filter_complex \
 "[0:v]scale=1600:900,setsar=1[bg];\
  [1:v]colorkey=0xFF00FF:0.32:0.14[o];\
  [bg][o]overlay=shortest=1,format=yuv420p[v]" \
 -map "[v]" -map 2:a -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest -r 25 out.mp4
```

To **sync footage to the narration**, split the footage into segments and re-time
them (`trim` + `setpts`) so key reveals land on the narrator's cues (e.g. "the
snapshot list appears when I say *snapshots*"). Slow static screens gently — heavy
slowdown looks sluggish.

---

## 6. Voiceover workflow (voice-first)

1. Narrator records **freely** over a silent reference cut (talk naturally; do not
   race any countdown).
2. **Trim** dead air with ffmpeg — cut the leading/trailing silence and any
   genuinely long pauses, but **keep the calm mid-phrase pauses** (they're
   delivery, not silence):
   ```bash
   ./bin/ffmpeg -i VOICE.m4a -filter_complex \
    "[0:a]atrim=start=S1:end=E1,asetpts=PTS-STARTPTS[a1];\
     [0:a]atrim=start=S2,asetpts=PTS-STARTPTS[a2];[a1][a2]concat=n=2:v=0:a=1[a]" \
    -map "[a]" -c:a aac voice-trim.m4a
   ```
3. **Gentle** de-breath only — a downward expander, never a hard gate (a hard gate
   *pumps* and destroys the voice on a regular period):
   ```bash
   ./bin/ffmpeg -i voice-trim.m4a \
    -af "highpass=f=80,agate=threshold=0.04:ratio=1.6:range=0.4:attack=30:release=420:knee=6" \
    voice-soft.m4a
   ```
4. Retime the **video** to the final narration length and mux.

---

## 7. Messaging guardrails (technical audience will scrutinize)

- **Do not** claim "backups make rejoin fast." **Flashback** is the fast path
  (rewind the divergent delta, seconds). **Backups** are the safety-net / reseed
  fallback used when flashback isn't possible.
- Backup value, stated accurately: repman **auto-selects** the right backup+method
  → faster than a **manual** restore (never "faster than flashback"); binlogs +
  backups archived to a **remote S3** store (restic, deduplicated) → offsite DR and
  long-retention point-in-time recovery; **single-table physical restore into a
  live server** (no restart, no downtime, consistent PITR); **concurrent** restore;
  **pigz** parallel decompression.
- A visible **failed job** is a **feature, not a demo-effect blemish** — narrate it
  as proof that repman **tracks every job's state and flags failures** (silent
  backup/restore failures are the real nightmare). The narration turns an on-camera
  failure into a deliberate proof point.

---

## 8. Section map / status (update as it progresses)

The video follows the arbitration story:

| # | Section | State |
|---|---------|-------|
| 1 | Registration & arbitration settings | TODO (re-record with cursor) |
| 2 | Backups & restore | **DONE** — footage + synced VO + gentle de-breath |
| 2b | Scheduled jobs & monitoring → scheduler settings | footage recorded, awaiting VO |
| 3 | Network split — minority isolated | TODO |
| 4 | Failover — arbitration promotes new master | TODO |
| 5 | Partition healed — old master diverged | TODO |
| 6 | Rejoin (flashback fast-path / mariabackup reseed fallback) | TODO |
| 7 | Recovered — old master healthy slave | TODO |

Sections 1 and 3–7 come from the **master demo take**, to be re-recorded with the
synthetic cursor (safe on a test cluster, since it re-fires the split→failover→
rejoin scenario).

---

*Working assets (footage, voice, finals, and the runnable scripts + a private
command cheatsheet with the environment-specific URL/auth) are kept locally under
the operator's `REPMAN/video/` working folder — not in this repo.*
