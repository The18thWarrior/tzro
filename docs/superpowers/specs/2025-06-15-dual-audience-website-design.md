# Dual-Audience Website: Developer / User Toggle

## Problem

External usability feedback rates non-technical comprehension of tzro.ai at **4/10**. The site communicates architectural sophistication well but fails to translate that into plain outcomes for non-developers. Key issues:

1. The hero explains the metaphor before the product
2. Copy assumes the visitor already has "an agent"
3. Benefits use systems-heavy language ("POSIX loopback boundary," "Procedural & Corrective Micro-Skills")
4. "Get Started" immediately asks users to build a Go binary
5. No concrete use cases appear before deep infrastructure explanation

## Solution

Add a **global Developer / User toggle** in the site header. The toggle uses a CSS-driven `data-mode` attribute on `<body>` to show/hide sections and swap inline copy. This is a single-HTML-file solution with no build step, matching the current static-site approach.

### Target Personas

- **Developer mode (⚡):** Go developers, infrastructure engineers, agent builders. These visitors want architecture diagrams, code samples, playground simulations. This is the current site, preserved.
- **User mode (👤):** Power users of AI tools — product managers, ops leads, solutions architects who use Claude Desktop, Cursor, or MCP-based agents daily. They configure tools but don't write Go code. They want: what does this do, why should I care, how do I install it.

### Default Mode

**User mode** is the default for first-time visitors. Stored in `localStorage` key `tzro-site-mode`. URL param `?view=user` or `?view=developer` overrides localStorage for shareable links.

---

## Toggle Mechanism

### Implementation

A `data-mode` attribute on `<body>` drives all visibility:

```css
body[data-mode="user"] [data-audience="developer"] { display: none; }
body[data-mode="developer"] [data-audience="user"] { display: none; }
/* Shared sections visible in both modes — no data-audience attribute needed */
```

### Header Pill Toggle

A segmented control sits in the header between the logo and nav links:

```
[tzro logo]  [ 👤 User | ⚡ Developer ]   Nav Links...   GitHub
```

- Active segment gets `#B6FF00` background with `#0A110D` text
- Inactive segment gets `#5C6B62` text on `#0A110D` background
- Container: `border-radius: 6px`, `border: 1px solid #223329`, `background: #0A110D`
- Toggle transition: 200ms ease on toggled elements (`opacity` + `display` via animation)

### Mobile

The pill moves into the mobile nav drawer, positioned above the nav links.

### URL Param + localStorage

```js
// On page load:
const urlMode = new URLSearchParams(location.search).get('view');
const storedMode = localStorage.getItem('tzro-site-mode');
const mode = urlMode || storedMode || 'user'; // default: user
document.body.setAttribute('data-mode', mode);
localStorage.setItem('tzro-site-mode', mode);

// On toggle click:
function setMode(mode) {
  document.body.setAttribute('data-mode', mode);
  localStorage.setItem('tzro-site-mode', mode);
  const url = new URL(location);
  url.searchParams.set('view', mode);
  history.replaceState(null, '', url);
}
```

---

## Section-by-Section Spec

### Navigation Links

Nav links swap per mode using `data-audience` attributes:

**User mode:** `Use Cases` · `How It Works` · `Install` · `GitHub`
**Developer mode:** `Architecture` · `Get Started` · `Playgrounds` · `Under the Hood` · `Quickstart` · `GitHub`

Both desktop and mobile nav drawers follow this pattern.

---

### 1. Hero Section (Shared — copy swaps inline)

The hero `<section>` remains a single element. Inner `<span>` and `<p>` elements use `data-audience` to swap copy.

#### Developer Mode (unchanged)

- Badge: `AGENTIC OPERATING SYSTEM`
- Title: **tzro — The Jumpdrive for AI Agents**
- Subtitle: "Your agent has a brain. What it's missing is a body — a portable runtime with a kernel, scheduler, persistent memory, and self-improving reflexes. Plug in tzro and your agent goes from stateless to fully autonomous."
- CTAs: `Get Started` → `#get-started` · `Explore the OS` → `#architecture`

#### User Mode

- Badge: `WORKS WITH CLAUDE, CURSOR & ANY MCP AGENT`
- Title: **Stop babysitting `<span class="glow-text">`your AI agents`</span>`**
- Subtitle: "Your agent loses context mid-task. It can't pick up where it left off. It racks up cloud API costs on every step. tzro fixes all of that — it runs agent tasks locally, saves progress, remembers past work, and only calls the cloud when it actually needs to think."
- CTAs: `See Use Cases` → `#use-cases` · `Install in 60 Seconds` → `#get-started`

---

### 2. "What can I actually do with tzro?" (User mode only, NEW)

`data-audience="user"`. Appears immediately after the hero, before the comparison section.

Three cards in a responsive grid (3-col desktop, 1-col mobile):

#### Card 1 — Research Agent 🔍

> **Run deep research while you sleep**
>
> Ask an agent to research 50 companies overnight. It breaks the work into steps, saves progress, and gives you a structured summary in the morning.

#### Card 2 — Document Processing 🔒

> **Process sensitive documents locally**
>
> Run document-processing workflows without sending raw contracts, medical records, or financial data to cloud APIs. Everything stays on your machine.

#### Card 3 — Overnight Automation 🌙

> **Queue up work. Come back to results.**
>
> Set up a multi-step workflow before you leave. Come back to results — not a "session expired" error. If anything fails, it resumes instead of starting over.

**Styling:** Reuse the existing `hood-card` glass card style from the "Under the Hood" section. Each card gets the icon as a large emoji, the headline as `<h3>`, and the body as `<p>`.

---

### 3. "Why Not Just Cloud?" Comparison (Shared — cell copy swaps inline)

The comparison section structure is identical in both modes — same 5 rows, same grid layout. Individual `<div class="comparison-cell">` elements contain both copy variants as inner `<span>` elements with `data-audience`.

| Row | Cloud-Only (User) | tzro (User) |
|-----|-------------------|-------------|
| **API Cost** | Every step costs money — costs grow with every task | Cloud called **once** for planning; execution runs locally for free |
| **Data Privacy** | Your data gets sent to third-party servers | Your data stays on your machine or server |
| **Execution Latency** | 200–500ms wait for each step (network round-trip) | Near-instant — runs on your hardware, no network needed |
| **Self-Improvement** | Same behavior every run — learns nothing | Remembers successful procedures and reuses them automatically |
| **Crash Recovery** | If something fails, start over from scratch | If something fails, it resumes from exactly where it stopped |

Developer mode copy: unchanged from current site.

---

### 4. How It Works — 5-Step Pipeline (User mode only, NEW)

`data-audience="user"`. Appears after the comparison section, in the position where Architecture would be in Developer mode.

A horizontal 5-step pipeline with arrows between steps:

```
🎯 You give a goal  →  🧠 Cloud plans it  →  ⚡ Local executes  →  💾 Progress saves  →  📋 Results delivered
```

Each step is a card with:
- Emoji icon (large, centered)
- Step title (bold, `#B6FF00`)
- Subtitle (muted, `#8A9B90`)

Subtitles:
1. "Research these 30 companies"
2. "One smart API call breaks the goal into steps"
3. "Steps run on your machine — fast, free, private"
4. "Every step checkpointed. Crashes resume, not restart"
5. "Clean summary. Agent remembers for next time."

**Responsive:** On mobile, the pipeline stacks vertically with downward arrows.

**Section wrapper:**
- Glass badge: `How It Works`
- Section title: "Cloud plans. Your machine executes."
- Section desc: "tzro calls the cloud once to break your goal into steps. Everything else runs locally — fast, private, and crash-proof."

---

### 5. OS Architecture Diagram (Developer mode only)

`data-audience="developer"`. Current section, unchanged. ID `#architecture`.

---

### 6. Get Started (Shared — content differs per mode)

#### Developer Mode (unchanged)

Both tabs: `⚡ Plug In (MCP)` and `🔧 Build On (Framework)`. All current content preserved: MCP build steps, client config tabs, JSON-RPC handshake verification, Exposed OS Tools list, Agent Delegation & Wait Protocol section.

#### User Mode

Only the MCP tab is shown (framework tab hidden via `data-audience="developer"`). The MCP content is simplified:

**Step 1: Install tzro**
```
curl -sSL https://tzro.network/install.sh | bash
```

**Step 2: Add to your agent**
Client tabs (Claude Desktop / Cursor / Antigravity) with JSON config — same tabs as current.

Simplified instruction text: "Paste this into your agent's config file and restart."

**Step 3: Start using it**
"Your agent can now run long tasks, save memory, and recover from crashes. Try asking it to research a topic or process a batch of documents."

No JSON-RPC verification step. No "Exposed OS Tools" list. No Delegation Policy section. Those are all `data-audience="developer"`.

---

### 7. Playgrounds (Developer mode only)

`data-audience="developer"`. Both playground sections (Kahn Compiler + Context Compaction), unchanged.

---

### 8. Under the Hood (Developer mode only)

`data-audience="developer"`. All 6 glass cards, unchanged.

---

### 9. Quickstart (Shared — content differs per mode)

#### Developer Mode (unchanged)

Both tabs: MCP Path and Framework Path.

#### User Mode

Only MCP Path tab shown. Same 3 code boxes as current MCP quickstart. Framework tab hidden via `data-audience="developer"`.

---

### 10. Footer (Shared — link targets adapt)

Footer links swap per mode:

**User mode:** `GitHub Codebase` · `Use Cases` · `Install`
**Developer mode:** `GitHub Codebase` · `Architecture` · `Get Started`

---

## Files Changed

All changes are in the `website/` directory:

### `website/index.html`
- Add `data-mode="user"` to `<body>` tag
- Add pill toggle markup in header (and mobile drawer)
- Add `data-audience` attributes to all existing sections
- Add new User-mode sections: "What can I do?" cards, "How It Works" pipeline
- Duplicate hero inner elements with `data-audience` variants
- Duplicate comparison cell contents with `data-audience` variants
- Add duplicate nav links with `data-audience` attributes
- Simplify Get Started MCP content for User mode variant

### `website/styles.css`
- Add `body[data-mode]` visibility rules
- Add pill toggle component styles
- Add "What can I do?" card grid styles (reuse `hood-card` base)
- Add pipeline step styles (horizontal flow, responsive vertical stack)
- Add toggle transition animation (200ms opacity fade)

### `website/app.js`
- Add `setMode()` function with localStorage + URL param sync
- Add toggle click handler
- Add mode initialization on page load
- Update scroll-spy to respect current mode's nav links
- Update mobile menu to include pill toggle

### `website/NOTES.md`
- Document the dual-audience toggle system
- Update page structure section to reflect both modes

---

## Out of Scope

- No demo video (referenced in feedback as "Watch the demo" CTA — there's no demo to link to yet; CTA links to `#use-cases` instead)
- No changes to the bio-synthetic aesthetic / Verdant-Synth design system
- No changes to the interactive playground JS logic
- No new pages — everything stays in `index.html`
- Bold claims ("zero marginal cost," "sub-10ms") are not modified — the feedback's concern about benchmarking context is valid but is a separate content task
- GitHub 404 fix is out of scope (repo visibility issue, not a website code change)
