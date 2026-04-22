# SwipeHire MVP — Implementation Tickets

**Dependency chain:** Tickets are ordered. Later tickets depend on earlier ones. Each ticket lists explicit blockers.
**All work must follow the SwipeHire Coding Guidelines** — form patterns, file size limits, colocation rules, and accessibility requirements apply to every ticket.

---

## Epic 1: Project Scaffolding & Auth

### T-001: Project Setup
**Priority:** P0
**Blocked by:** —
**Estimate:** 2–4 hrs

**SvelteKit + Core Dependencies:**
- Initialize SvelteKit project with TypeScript
- Clone `get-convex/convex-agent-plugins` → copy rules and skills into project (`.cursor/rules/` or `.claude/` depending on agent)
- Configure Convex MCP server for AI agent access to deployment
- Install and configure Convex (`npx convex init`, SvelteKit client provider)
- Install and configure Clerk (SvelteKit SDK, Convex integration)
- Install `@sveltejs/adapter-vercel`, configure in `svelte.config.js`
- Install `mdsvex`, configure extensions and preprocessor

**UI Layer:**
- Initialize shadcn-svelte (`npx shadcn-svelte@latest init`), pick a built-in theme
- Scaffold core UI components: button, card, input, select, badge, tabs, slider, toggle-group, dialog, toast, form
- Install `lucide-svelte` (icons) and `@fontsource-variable/inter` (font)
- Tailwind CSS setup (utility layer for shadcn-svelte; theming via CSS variables)

**Form Validation + SEO:**
- Install `svelte-meta-tags` for SEO
- Install `zod`, `sveltekit-superforms`, `formsnap` for form validation

**Error Monitoring:**
- Install and configure `@sentry/sveltekit` (run `npx @sentry/wizard@latest -i sveltekit` or manual setup)
- `hooks.client.ts` — Sentry.init with DSN, tracing, replay integration
- `hooks.server.ts` — Sentry.init with DSN, tracing
- Source map upload configured in `vite.config.ts` via Sentry Vite plugin
- `.env.sentry-build-plugin` added to `.gitignore`

**Testing:**
- Install Playwright (`pnpm add -D @playwright/test`, `npx playwright install chromium`)
- Create `playwright.config.ts` (see Dev Guidelines §7.4)
- Create `tests/` directory structure (fixtures, flows, smoke)
- Install Vitest for unit tests (`pnpm add -D vitest`)
- Write foundation smoke test (`tests/smoke/foundation.spec.ts`):
  - App loads at `/` without errors
  - Landing page renders with "Sign Up" / "Sign In" CTAs
  - Navigation to `/auth` renders Clerk sign-in component
  - Unauthenticated access to `/seeker/dashboard` redirects to `/auth`
  - Unauthenticated access to `/employer/dashboard` redirects to `/auth`
  - No console errors on any visited page
- Write scaffold smoke test (`tests/smoke/scaffold.spec.ts`):
  - Landing page loads with 200 status
  - Clerk sign-in component renders on `/auth`
  - Convex client connects (no WebSocket errors in console)
  - shadcn-svelte component renders with correct CSS variable theming
  - Unauthenticated access to `/seeker/dashboard` redirects to `/auth`
  - Unauthenticated access to `/employer/dashboard` redirects to `/auth`

**Code Style Enforcement (see Dev Guidelines §9):**
- Install and configure Biome (`biome.json`) — linting + formatting for `.ts`, `.js`, `.json`
- Install and configure `eslint-plugin-svelte`, `svelte-eslint-parser`, `@typescript-eslint/parser` — Svelte-only linting (`eslint.config.js`)
- Install and configure `prettier-plugin-svelte` — Svelte-only formatting (`.prettierrc`, `.prettierignore`)
- Configure strict TypeScript (`tsconfig.json`): `strict: true` (SvelteKit default) + `noUncheckedIndexedAccess`, `noImplicitOverride`, `noFallthroughCasesInSwitch`, `forceConsistentCasingInFileNames`, `noImplicitReturns`, `allowUnreachableCode: false`, `exactOptionalPropertyTypes` — do NOT override SvelteKit-managed flags (see Dev Guidelines §9.5)
- Install and configure Lefthook (`lefthook.yml`): pre-commit hooks for biome check, eslint, prettier, svelte-check, tsc
- Add package.json scripts: `lint`, `lint:fix`, `format`, `check`, `validate`

**Environment:**
- Set up environment variables (PUBLIC_CONVEX_URL, Clerk keys)
- Basic `+layout.svelte` with Clerk + Convex providers

**Acceptance:**
- `pnpm dev` shows a page with a working Clerk sign-in button and Convex dev logs confirm connection
- `pnpm validate` passes clean (svelte-check + biome + eslint + tsc)
- Lefthook pre-commit hook fires and auto-fixes formatting on staged files
- shadcn button renders with theme colors from CSS variables
- Sentry test error (from wizard example page) appears in Sentry dashboard
- Foundation smoke test passes: `npx playwright test tests/smoke/foundation.spec.ts`

---

### T-002: Auth Schema & Role Assignment
**Priority:** P0
**Blocked by:** T-001
**Estimate:** 2–3 hrs

- Define `user_roles` table in `convex/schema.ts`
- `users.createRole` mutation (called from Clerk webhook or post-signup flow)
- `users.getRole` query
- Clerk webhook endpoint or client-side post-signup handler to write role
- Role selection UI on sign-up page ("Looking for Work" / "Hiring")
- Email verification enforcement via Clerk config

**Acceptance:** New user signs up → selects role → role persisted in `user_roles` → `getRole` returns correct role.

---

### T-003: Route Guards & Role-Based Routing
**Priority:** P0
**Blocked by:** T-002
**Estimate:** 1–2 hrs

- `hooks.server.ts` Clerk middleware for JWT validation
- `/seeker/+layout.server.ts` — redirect if not `job_seeker`
- `/employer/+layout.server.ts` — redirect if not `employer`
- Post-login redirect logic: fetch role → route to `/seeker/dashboard` or `/employer/dashboard`
- Unauthenticated users hitting protected routes → redirect to `/auth`

**Acceptance:** Employer cannot access `/seeker/*`. Seeker cannot access `/employer/*`. Unauthenticated users bounce to `/auth`.

---

## Epic 2: Job Seeker Profile

### T-004: Seeker Schema
**Priority:** P0
**Blocked by:** T-001
**Estimate:** 1 hr

- Add to `convex/schema.ts`: `job_seeker_profiles`, `work_experience`, `education`, `certifications`
- All indexes as defined in TDD §3

**Acceptance:** `npx convex dev` runs with no schema errors.

---

### T-005: Seeker Onboarding Wizard
**Priority:** P0
**Blocked by:** T-003, T-004
**Estimate:** 4–6 hrs

- `OnboardingWizard.svelte` component (step indicator, back/next navigation)
- Colocated `schema.ts` in onboarding route dir — Zod schema for all profile fields
- `$lib/schemas/work-experience.ts` and `education.ts` — shared schemas for nested entries
- All form fields use shadcn-svelte Form components (Superforms + Formsnap) for client+server validation and WCAG 2.0 compliance
- Step 1: Basic Info (full name, city, state, zip, bio, professional summary)
- Step 2: Skills (free-text tag input, at least 1 required)
- Step 3: Work Experience (add/edit/remove entries)
- Step 4: Education (add/edit/remove entries)
- Step 5: Certifications (UI present, table scaffolded — hidden for MVP per BRD)
- Step 6: Job Preferences (search radius slider 5–100, default 25)
- `seekers.createProfile` mutation — writes profile + nested records in one call
- Sets `onboarding_complete: true` on final submit
- Redirect to `/seeker/dashboard` on completion

**Acceptance:** Complete wizard → all data persisted → user lands on dashboard. Refreshing mid-wizard retains step (local state OK for MVP).

---

### T-006: Seeker Profile Management
**Priority:** P1
**Blocked by:** T-005
**Estimate:** 2–3 hrs

- `/seeker/profile` page
- `seekers.getProfile` query (profile + work experience + education + certifications)
- `seekers.updateProfile` mutation
- `experience.list` / `create` / `update` / `remove`
- `education.list` / `create` / `update` / `remove`
- Inline editing for all fields from onboarding

**Acceptance:** Edit any profile field → save → refresh → changes persist.

---

## Epic 3: Organizations & Employer Onboarding

### T-007: Organization Schema
**Priority:** P0
**Blocked by:** T-001
**Estimate:** 30 min

- Add to schema: `organizations`, `org_memberships`
- Indexes as defined in TDD §3

**Acceptance:** Schema deploys cleanly.

---

### T-008: Employer Onboarding
**Priority:** P0
**Blocked by:** T-003, T-007
**Estimate:** 2–3 hrs

- `/employer/onboarding` page — single form
- Colocated `schema.ts` in onboarding route dir — Zod schema for org fields
- Form uses shadcn-svelte Form components (Superforms + Formsnap) for validation + accessibility
- Fields: business name, city, state, zip, description, website, phone
- `orgs.create` mutation — creates org + inserts `org_memberships` row with role `"admin"`
- Redirect to `/employer/dashboard` on completion

**Acceptance:** Employer completes form → org created → membership created → lands on dashboard.

---

### T-009: Company Profile Management
**Priority:** P1
**Blocked by:** T-008
**Estimate:** 1–2 hrs

- `/employer/company` page
- `orgs.get` query
- `orgs.update` mutation (with `requireOrgAdmin` guard)
- Edit all org fields

**Acceptance:** Edit org name → save → refresh → updated.

---

## Epic 4: Job Listings

### T-010: Job Listing Schema
**Priority:** P0
**Blocked by:** T-007
**Estimate:** 30 min

- Add `job_listings` table to schema with all fields from TDD §3
- Salary stored as annual cents
- Indexes: `by_org`, `by_org_status`

**Acceptance:** Schema deploys.

---

### T-011: Post a Job
**Priority:** P0
**Blocked by:** T-008, T-010
**Estimate:** 4–6 hrs

- `/employer/jobs/new` page — multi-section form
- Colocated `schema.ts` with cross-field refinements (salary max ≥ min, hours max ≥ min)
- All form fields use shadcn-svelte Form components (Superforms + Formsnap)
- Basic Info: title, job type (select), description, requirements
- Skills: free-text tag input
- Work Schedule: schedule type toggle → conditional fields (fixed times vs shift checkboxes), days of work toggle buttons, hours/week min-max
- Compensation: salary min/max inputs with hourly/yearly toggle, live conversion display, stored as annual cents via `displayToCents()`
- Matching Config: match volume select (5/10/20/50), search radius slider
- Interview: calendar link (optional)
- `listings.create` mutation — validates, writes listing with `status: "active"`, then schedules `matching.runForListing`
- `SalaryDisplay.svelte` component for conversion display

**Acceptance:** Post job → listing appears on dashboard → matching action fires (match records verified in next ticket).

---

### T-012: Employer Dashboard
**Priority:** P0
**Blocked by:** T-011
**Estimate:** 3–4 hrs

- `/employer/dashboard` page
- Org name header + "Post New Job" CTA
- `listings.getByOrg` query
- Summary stats (reactive via Convex subscriptions):
  - Active jobs count
  - Candidates to review count
  - Interviews booked count
- Job listing cards: title, type, status, candidate review count, interview count, "Review Candidates" link
- `listings.updateStatus` mutation (active ↔ paused ↔ closed ↔ filled, including reopen)
- `listings.remove` mutation (permanent delete, cascades match records)

**Acceptance:** Dashboard shows live stats. Changing listing status updates counts in real time. Delete removes listing + matches.

---

## Epic 5: Matching Engine

### T-013: Matching Algorithm
**Priority:** P0
**Blocked by:** T-004, T-010
**Estimate:** 3–4 hrs

- `convex/lib/scoring.ts` — pure scoring functions:
  - `scoreSkills()` — partial string match, neutral 50% when no required skills
  - `scoreLocation()` — city exact 100%, same state 50%, else 0%
  - `scoreExperience()` — keyword extraction + stop-word filter vs work experience text
  - `scoreJobTitle()` — bidirectional keyword overlap, best across all entries
- `matching.runForListing` internal action (TDD §4.2):
  - Fetch listing
  - `seekers.getCandidatePool` internal query — filter by state, exclude already-matched, limit 100
  - Score all candidates
  - Sort descending, take top N (match_volume)
  - `matching.writeMatches` internal mutation — bulk insert match records with `batch_number: 1`
- Triggered from `listings.create` via `ctx.scheduler.runAfter(0, ...)`

**Acceptance:** Create a listing with 5 match volume → 5 match records created with scores and breakdowns. Scores are deterministic given the same input data.

---

### T-014: Request More Candidates
**Priority:** P1
**Blocked by:** T-013
**Estimate:** 2 hrs

- `matching.requestMore` action
  - Query existing matched seeker IDs for this listing
  - Fetch next pool excluding them
  - Score, take top N, write with incremented `batch_number`
  - Return `{ exhausted: true }` if 0 new candidates found
- UI button on candidate review page
- Disable button + show message when exhausted

**Acceptance:** After swiping through initial batch, employer clicks "Load More" → new candidates appear. When pool is empty, button disabled with message.

---

## Epic 6: Matches Schema & Records

### T-015: Matches Schema
**Priority:** P0
**Blocked by:** T-001
**Estimate:** 30 min

- Add `matches` table to schema with all fields and indexes from TDD §3

**Acceptance:** Schema deploys.

---

## Epic 7: Candidate Review (Employer Swipe)

### T-016: SwipeCard Component
**Priority:** P0
**Blocked by:** —
**Estimate:** 3–4 hrs

- `SwipeCard.svelte` — reusable for both employer and seeker flows
- Touch/mouse drag with spring animation (svelte/motion)
- Swipe threshold: 30% card width
- Left/right visual indicators (X / heart icons)
- Keyboard support: left arrow = pass, right arrow = like
- Undo toast: 5-second window after pass, mutation deferred until timeout
- Slot-based content area for different card layouts

**Acceptance:** Card can be swiped, springs back under threshold, commits over threshold. Undo toast appears on pass. Keyboard works.

---

### T-017: Employer Candidate Review Page
**Priority:** P0
**Blocked by:** T-013, T-015, T-016
**Estimate:** 4–5 hrs

- `/employer/jobs/[jobId]/candidates` page
- Two tabs: "To Review" and "Accepted"
- **To Review tab:**
  - `candidates.getQueue` query — matches where `employer_action: "pending"`, returns tiered seeker data (name, city, skills, bio, summary, score, score breakdown). No work history/education.
  - `SkillBadge.svelte` with match highlighting (seeker skill matches listing skill)
  - `ProfileAvatar.svelte` (photo or initials)
  - Navigation arrows between cards
  - `candidates.swipe` mutation — sets `employer_action` to `"liked"` or `"passed"` + timestamp
  - Pre-fetch next 2 cards
- **Accepted tab:**
  - `candidates.getAccepted` query — matches where `employer_action: "liked"`, returns full seeker profile after seeker accepts
  - List view: match score, profile info, skills
  - Status badges: "Awaiting Response" / "Interview Accepted" / "Declined"

**Acceptance:** Employer swipes through candidates. Liked candidates appear in Accepted tab with correct status. Passed candidates disappear (with undo window).

---

## Epic 8: Seeker Dashboard & Opportunities

### T-018: Seeker Dashboard
**Priority:** P0
**Blocked by:** T-015, T-016
**Estimate:** 4–5 hrs

- `/seeker/dashboard` page
- Welcome message + pending opportunity count (reactive)
- **New Opportunities section:**
  - `seekers.getOpportunities` query — matches where `employer_action: "liked"` AND `seeker_response: "pending"` AND `listing.status: "active"`
  - Card-based feed (SwipeCard) showing: job title, company name, city, job type, salary range (formatted via `centsToDisplay`), description
  - Actions: Pass (decline) / Interview (accept)
  - `seekers.respondToMatch` mutation — sets `seeker_response` + timestamp
  - Undo on pass (same deferred pattern as employer)
- **Awaiting Interview Booking section:**
  - `seekers.getAcceptedMatches` query — `seeker_response: "accepted"`
  - "Schedule Interview" button → opens `calendar_link` in new tab
  - If no calendar link: "Employer will reach out" message
- **Empty state:** "Your profile is live — we'll notify you when an employer is interested"

**Acceptance:** Seeker sees opportunities from employers who liked them. Accept → shows in "Awaiting Interview" with calendar link. Pass → card dismissed with undo window.

---

## Epic 9: File Storage

### T-019: Photo & Logo Upload
**Priority:** P1
**Blocked by:** T-005, T-008
**Estimate:** 2–3 hrs

- `files.generateUploadUrl` mutation
- `files.getUrl` query
- Upload component (drag-drop or click, 5MB max, jpeg/png/webp only)
- Seeker profile photo upload (in profile management)
- Org logo upload (in company profile management)
- `ProfileAvatar.svelte` — render photo from storage URL or initials fallback

**Acceptance:** Upload photo → see it on profile. Delete photo → initials avatar renders. Oversized/wrong-type files rejected.

---

## Epic 10: Settings & Misc

### T-020: Settings Pages
**Priority:** P2
**Blocked by:** T-003
**Estimate:** 1 hr

- `/seeker/settings` — sign out button (Clerk `signOut()`)
- `/employer/settings` — sign out button
- Clear session → redirect to `/`

**Acceptance:** Sign out works from both flows.

---

### T-021: Marketing Pages, Blog & SEO Setup
**Priority:** P1
**Blocked by:** T-001
**Estimate:** 5–7 hrs

- Install and configure `mdsvex` in `svelte.config.js`
- Create `(marketing)` route group with its own layout (nav, footer, no auth)
- Set `export const prerender = true` in `(marketing)/+page.ts`
- Landing page (`/`) — value prop for seekers and employers, CTAs to `/auth`
- About page (`/about`) — team, mission
- Pricing page (`/pricing`) — placeholder tiers
- Blog index (`/blog`) — `getPosts()` loader, lists posts sorted by date
- Blog post page (`/blog/[slug]`) — dynamic mdsvex render from `src/content/blog/*.md`
- `src/lib/utils/posts.ts` — glob import, frontmatter parse, sort, filter by `published`
- Create 1–2 seed blog posts with frontmatter (title, date, excerpt, author, published, og_image)
- **SEO setup:**
  - Root layout `<MetaTags>` with defaults (title template, site OG image, Twitter card)
  - Per-page meta overrides on landing, about, pricing
  - Blog posts auto-wire title/excerpt/og_image from frontmatter
  - `sitemap.xml/+server.ts` — prerendered, includes static pages + blog posts
  - `/static/robots.txt` — allow all, reference sitemap
  - `/static/og/default.png` — 1200×630 default OG image
  - App routes (`/seeker/*`, `/employer/*`) get `noindex` meta
- Responsive layout for all marketing pages

**Acceptance:** Marketing pages prerender at build time. Blog index lists posts. Individual post URLs render markdown with correct OG tags. Sharing a blog post URL on Slack/Twitter/LinkedIn shows rich preview with title, excerpt, and image. `sitemap.xml` returns valid XML with all public routes. App routes are excluded from sitemap and have noindex.

---

### T-021b: Route Group Migration
**Priority:** P0
**Blocked by:** T-003
**Estimate:** 1 hr

- Move all seeker/employer routes under `(app)` route group
- `(app)/+layout.server.ts` — redirect unauthenticated users to `/auth`
- Verify `(marketing)` routes have no auth checks
- Verify `(app)` routes remain fully auth-gated

**Acceptance:** Unauthenticated user can browse `/`, `/about`, `/pricing`, `/blog/*`. Hitting `/seeker/dashboard` redirects to `/auth`.

---

### T-022: Notification Scaffolding
**Priority:** P2
**Blocked by:** T-001
**Estimate:** 30 min

- Add `notifications` table to schema
- `/seeker/notifications` and `/employer/notifications` placeholder pages — "Coming soon" message
- No queries, no delivery logic

**Acceptance:** Routes exist, show placeholder. Schema deploys.

---

### T-023: CI/CD Pipeline (GitHub Actions)
**Priority:** P1
**Blocked by:** T-001
**Estimate:** 4–6 hrs

**PR Workflow** (`.github/workflows/pr.yml`):
- Triggered on `pull_request` targeting `main`
- **Quality job:** runs `pnpm validate` (biome check, eslint svelte, svelte-check, tsc --noEmit)
- **Unit test job:** runs `pnpm test` (vitest — scoring functions, Zod schemas, salary conversion)
- Quality and unit test jobs run **in parallel**
- **Preview deploy job:** depends on both quality + unit tests passing. Deploys Convex to dev backend and SvelteKit to Vercel preview. Captures preview URL as job output.
- **E2E test job:** depends on preview deploy. Installs Playwright, runs full E2E suite against the preview deployment URL. Uploads test report + trace artifacts on failure.

**Main Workflow** (`.github/workflows/main.yml`):
- Triggered on `push` to `main`
- Same parallel quality + unit test structure as PR workflow
- **Develop deploy job:** depends on both passing. Deploys Convex to staging backend and SvelteKit to develop environment (stable URL).
- **E2E test job:** runs full E2E suite against develop URL. Uploads artifacts on failure.

**Release Workflow** (`.github/workflows/release.yml`):
- Triggered on tag push matching `v*`
- **Production deploy job:** deploys Convex to production backend and SvelteKit to production.
- **Smoke test job:** depends on production deploy. Runs Playwright smoke test suite (subset of full E2E — login, dashboard loads, core navigation, one happy-path match flow) against production URL. Uploads artifacts on failure.

**Shared infrastructure:**
- Convex deploy step uses `CONVEX_DEPLOY_KEY` secret, scoped per environment
- Vercel deploy uses Vercel CLI or GitHub integration with environment targets (preview/develop/production)
- Playwright installed via `npx playwright install --with-deps chromium` (single browser for CI speed)
- Test report artifacts uploaded via `actions/upload-artifact` on failure
- Environment-specific `.env` values injected via GitHub environment secrets

**Acceptance:**
- PR opened → quality + unit tests run in parallel → on pass, preview deploys → Playwright E2Es run against preview URL → results visible in PR checks
- PR merged to main → same flow but deploys to develop environment with stable URL
- Tag `v1.0.0` pushed → production deploy → smoke tests pass against production
- Failed E2E uploads Playwright trace + screenshot artifacts for debugging
- Quality/unit test failure blocks deploy (preview deploy does not start)

---

## Dependency Graph

```
T-001 (Setup)
├── T-002 (Auth Schema) → T-003 (Route Guards)
│   ├── T-021b (Route Group Migration)
│   ├── T-005 (Seeker Onboarding) → T-006 (Profile Mgmt)
│   │                              → T-019 (File Upload)
│   ├── T-008 (Employer Onboarding) → T-009 (Company Profile)
│   │                                → T-019 (File Upload)
│   │                                → T-011 (Post Job) → T-012 (Dashboard)
│   ├── T-020 (Settings)
│   └── T-021 (Marketing + Blog + SEO)
├── T-004 (Seeker Schema)
│   └── T-005 (Seeker Onboarding)
│   └── T-013 (Matching) → T-014 (Request More)
├── T-007 (Org Schema)
│   └── T-008 (Employer Onboarding)
│   └── T-010 (Listing Schema) → T-011 (Post Job)
│                               → T-013 (Matching)
├── T-015 (Matches Schema)
│   └── T-017 (Candidate Review)
│   └── T-018 (Seeker Dashboard)
├── T-016 (SwipeCard) → T-017, T-018
├── T-022 (Notification Scaffold)
└── T-023 (CI/CD Pipeline)
```

## Critical Path

**T-001 → T-002 → T-003 → T-005 + T-008 → T-011 → T-013 → T-017 + T-018**

This is the shortest path to a working end-to-end demo: both users can sign up, seeker builds profile, employer posts job, matching runs, employer swipes, seeker responds.

---

## Summary

| Priority | Tickets | Estimate |
|----------|---------|----------|
| P0 (must ship) | T-001 through T-013, T-015 through T-018, T-021b | ~38–50 hrs |
| P1 (should ship) | T-006, T-009, T-014, T-019, T-021, T-023 | ~17–24 hrs |
| P2 (nice to have) | T-020, T-022 | ~1.5–2 hrs |
| **Total** | **24 tickets** | **~57–76 hrs** |
