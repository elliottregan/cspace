# SwipeHire — Development Guidelines

**Audience:** Human developers and AI coding agents working on this codebase.
**Authority:** These patterns are mandatory. Do not deviate without discussion.

---

## 1. Code Organization

### 1.1 SOLID Principles

All code follows SOLID. In a SvelteKit + Convex context, this means:

- **Single Responsibility:** One component does one thing. A `SkillBadge` renders a skill tag — it does not fetch data, manage state, or handle routing. A Convex mutation validates and writes — it does not score, notify, or redirect.
- **Open/Closed:** Extend through composition, not modification. Add new form steps by adding components, not by adding branches to an existing component.
- **Liskov Substitution:** Components accepting slots or props should work with any valid input of the declared type. A `SwipeCard` works with seeker data or job data.
- **Interface Segregation:** Don't pass entire documents to components that need two fields. Destructure props to the minimum required.
- **Dependency Inversion:** Components depend on schemas and types, not on specific Convex queries. Data fetching happens at the route level and flows down as props.

### 1.2 Colocation

Keep related code close together. The route tree is the primary organizing structure.

```
# ✅ Good — feature code lives near its route
src/routes/(app)/employer/jobs/new/
├── +page.svelte              # Page component
├── +page.server.ts           # Form action (validate + call Convex)
├── JobPostingForm.svelte     # Form UI
├── ScheduleSection.svelte    # Schedule sub-form
├── CompensationSection.svelte # Salary sub-form
└── MatchingConfig.svelte     # Matching options sub-form

# ❌ Bad — dumping everything in a flat components folder
src/lib/components/
├── JobPostingForm.svelte
├── ScheduleSection.svelte
├── CompensationSection.svelte
├── MatchingConfig.svelte
├── CandidateCard.svelte
├── SeekerOnboardingStep1.svelte
├── ... (50 more files)
```

**`$lib/components/`** is reserved for truly shared, cross-feature components: `SwipeCard`, `ProfileAvatar`, `SkillBadge`, `SalaryDisplay`, and the `ui/` directory (shadcn-svelte primitives).

**`$lib/schemas/`** holds Zod schemas — these are shared across routes (a schema may be used in both a creation form and an edit form).

**`$lib/utils/`** holds pure utility functions with no side effects.

**Convex functions** follow the same principle:

```
convex/
├── auth.ts           # requireAuth, requireRole helpers
├── users.ts          # user_roles mutations/queries
├── seekers.ts        # seeker profile CRUD
├── experience.ts     # work experience CRUD
├── education.ts      # education CRUD
├── orgs.ts           # organization CRUD
├── listings.ts       # job listing CRUD
├── matching.ts       # matching action + scoring
├── candidates.ts     # match queries for employer swipe
├── files.ts          # upload URL + serve
├── lib/
│   ├── auth.ts       # requireAuth, requireRole, requireOrgAdmin
│   ├── scoring.ts    # pure scoring functions
│   └── salary.ts     # cents ↔ display conversions
└── schema.ts
```

### 1.3 File Size Limits

**Target: under 200 lines per file.** This is a guideline, not a wall — but exceeding it is a signal to split.

When a file grows past 200 lines:

1. Identify logical sections (e.g., a form with 4 sections → 4 components)
2. Extract each section into its own file in the same directory
3. The parent file becomes an orchestrator that imports and composes

When a component has more than 5–6 imported dependencies, it's likely doing too much.

```svelte
<!-- ❌ 400-line form with inline validation, 6 sections, and API calls -->
<script>
  import { ... } from 20 different places;
  // 300 lines of logic
</script>
<!-- 100 lines of template -->

<!-- ✅ Orchestrator that composes focused sub-components -->
<script>
  import BasicInfoSection from './BasicInfoSection.svelte';
  import SkillsSection from './SkillsSection.svelte';
  import ScheduleSection from './ScheduleSection.svelte';
  import CompensationSection from './CompensationSection.svelte';
</script>

<form method="POST" use:enhance>
  <BasicInfoSection {form} />
  <SkillsSection {form} />
  <ScheduleSection {form} />
  <CompensationSection {form} />
  <SubmitButton />
</form>
```

---

## 2. Form Patterns

Every form in this application follows this exact pattern. No exceptions.

### 2.1 Validation Strategy

- **Inline errors** appear when a field is **dirty** (user has interacted with it) and invalid
- **Submit button is never disabled.** Disabled buttons are not WCAG 2.2 compliant — screen readers skip them and users get no feedback on what's wrong
- **On submit with invalid form:** An error alert appears at the top of the form listing which fields need attention. Focus moves to the alert.

### 2.2 Error Alert Pattern

Every form includes a `FormErrorAlert` at the top:

```svelte
<!-- $lib/components/FormErrorAlert.svelte -->
<script lang="ts">
  import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert';
  import { AlertCircle } from 'lucide-svelte';

  export let errors: Record<string, string[]>;
  export let show: boolean;

  let alertRef: HTMLDivElement;

  $: fieldNames = Object.keys(errors).filter((k) => errors[k]?.length > 0);
  $: hasErrors = show && fieldNames.length > 0;

  $: if (hasErrors && alertRef) {
    alertRef.focus();
  }
</script>

{#if hasErrors}
  <div bind:this={alertRef} tabindex="-1" role="alert">
    <Alert variant="destructive">
      <AlertCircle class="h-4 w-4" />
      <AlertTitle>Please fix the following errors</AlertTitle>
      <AlertDescription>
        <ul>
          {#each fieldNames as field}
            <li>
              <button
                type="button"
                on:click={() => document.querySelector(`[name="${field}"]`)?.focus()}
              >
                {errors[field][0]}
              </button>
            </li>
          {/each}
        </ul>
      </AlertDescription>
    </Alert>
  </div>
{/if}
```

Key behaviors:
- Uses `role="alert"` for screen reader announcement
- Each error is a button that focuses the offending field when clicked
- Alert receives focus via `tabindex="-1"` on failed submit
- Only shown after a submit attempt, not on initial load

### 2.3 Standard Form Structure

```svelte
<script lang="ts">
  import { superForm } from 'sveltekit-superforms';
  import { zodClient } from 'sveltekit-superforms/adapters';
  import FormErrorAlert from '$lib/components/FormErrorAlert.svelte';
  import * as Form from '$lib/components/ui/form';
  import { Button } from '$lib/components/ui/button';
  import { someSchema } from '$lib/schemas/some-schema';

  export let data;

  const form = superForm(data.form, {
    validators: zodClient(someSchema),
    // Don't clear form on error — keep user's input
    resetForm: false,
  });
  const { form: formData, errors, allErrors, submitted } = form;
</script>

<form method="POST" use:form.enhance>
  <FormErrorAlert errors={$errors} show={$submitted} />

  <Form.Field {form} name="title">
    <Form.Control let:attrs>
      <Form.Label>Title</Form.Label>
      <Input {...attrs} bind:value={$formData.title} />
    </Form.Control>
    <Form.FieldErrors />  <!-- inline errors, auto-shown when dirty -->
  </Form.Field>

  <!-- ... more fields ... -->

  <Button type="submit">Save</Button>  <!-- NEVER disabled -->
</form>
```

### 2.4 Form Action Pattern (Server-Side)

```typescript
// +page.server.ts
import { superValidate, message } from 'sveltekit-superforms';
import { zod } from 'sveltekit-superforms/adapters';
import { fail, redirect } from '@sveltejs/kit';
import { someSchema } from '$lib/schemas/some-schema';

export const load = async () => {
  const form = await superValidate(zod(someSchema));
  return { form };
};

export const actions = {
  default: async ({ request, locals }) => {
    const form = await superValidate(request, zod(someSchema));
    if (!form.valid) return fail(400, { form });

    // Transform and pass to Convex
    await locals.convex.mutation(api.someModule.create, form.data);

    throw redirect(303, '/success-route');
  },
};
```

### 2.5 Multi-Step Wizard Pattern

The seeker onboarding wizard validates **per-step**, not on final submit:

- Each step has its own Zod schema (or a `.pick()` / `.partial()` of the full schema)
- "Next" button validates current step only — shows alert if invalid
- "Back" button always works, no validation
- Final "Submit" validates the full combined schema
- Step state is held in a Svelte store, not in the URL — refreshing resets to step 1 (acceptable for MVP)

---

## 3. Component Patterns

### 3.1 Props Over Stores

Pass data as props. Reserve stores for truly global state (auth session, current step in a wizard).

```svelte
<!-- ✅ Good — data flows down as props -->
<CandidateCard name={candidate.name} city={candidate.city} score={match.score} />

<!-- ❌ Bad — component reaches into a global store -->
<CandidateCard candidateId={id} />
<!-- ...internally does $candidateStore[id] -->
```

### 3.2 Minimal Props

Only pass what a component needs. Destructure at the call site.

```svelte
<!-- ✅ Good -->
<SkillBadge skill={skill} highlighted={matchingSkills.includes(skill)} />

<!-- ❌ Bad — passing entire listing just to check skill match -->
<SkillBadge skill={skill} listing={listing} />
```

### 3.3 Slot-Based Composition

Prefer slots over prop-driven rendering for layout variation:

```svelte
<!-- SwipeCard.svelte — content-agnostic -->
<div class="card" on:swipeleft on:swiperight>
  <slot name="header" />
  <slot />
  <slot name="actions" />
</div>
```

### 3.4 Loading & Error States

Every component that depends on async data handles three states:

```svelte
{#if $query.isLoading}
  <Skeleton />
{:else if $query.error}
  <ErrorMessage error={$query.error} />
{:else}
  <!-- Render data -->
{/if}
```

No component should render blank with no explanation.

### 3.5 Test Selectors

Every interactive element and every element asserted against in E2E tests must have a `data-testid` attribute. Add these during development, not as a testing afterthought. See §7.4 for the full pattern.

```svelte
<!-- Required on: buttons, inputs, cards, counts, status badges, sections -->
<button data-testid="accept-button" on:click={handleAccept}>Interview</button>
<span data-testid="opportunity-count">{count}</span>
<div data-testid="opportunity-card">...</div>
```

---

## 4. Convex Patterns

> **AI Agent Setup:** This project uses rules and skills from [get-convex/convex-agent-plugins](https://github.com/get-convex/convex-agent-plugins). These provide persistent guidance on query optimization, security, schema design, and Convex-specific patterns. Agents should follow these rules when working in the `convex/` directory. The Convex MCP server should be configured for direct deployment access.

### 4.1 Auth Guards First

Every query and mutation that accesses user-scoped data starts with an auth check. No exceptions.

```typescript
export const getProfile = query({
  handler: async (ctx) => {
    const { userId } = await requireRole(ctx, "job_seeker");
    // ... rest of query
  },
});
```

### 4.2 Thin Mutations, Internal Actions for Heavy Work

Mutations write data. If a mutation triggers expensive computation (scoring, batch operations), schedule an internal action:

```typescript
// ✅ Good — mutation writes, action computes
export const create = mutation({
  args: { /* ... */ },
  handler: async (ctx, args) => {
    const listingId = await ctx.db.insert("job_listings", { ...args, status: "active" });
    await ctx.scheduler.runAfter(0, internal.matching.runForListing, { listingId });
    return listingId;
  },
});

// ❌ Bad — mutation does scoring inline
export const create = mutation({
  handler: async (ctx, args) => {
    const listingId = await ctx.db.insert("job_listings", args);
    const seekers = await ctx.db.query("job_seeker_profiles").collect();
    // ... 50 lines of scoring logic in a mutation
  },
});
```

### 4.3 Query Composition

Build complex reads from small, focused queries. Use helpers in `convex/lib/` for shared logic.

```typescript
// ✅ Good — helper for reuse
// convex/lib/auth.ts
export async function getOrgForUser(ctx: QueryCtx, clerkId: string) {
  const membership = await ctx.db
    .query("org_memberships")
    .withIndex("by_clerk_id", (q) => q.eq("clerk_id", clerkId))
    .first();
  if (!membership) throw new ConvexError("No org membership");
  return ctx.db.get(membership.organization_id);
}
```

### 4.4 Argument Validation

Convex `v` validators handle runtime arg validation. Zod schemas validate in SvelteKit before data reaches Convex. This is intentional double-validation — never rely on client-side validation alone.

---

## 5. Naming Conventions

| Thing | Convention | Example |
|-------|-----------|---------|
| Svelte components | PascalCase | `SwipeCard.svelte` |
| Route files | SvelteKit convention | `+page.svelte`, `+page.server.ts` |
| Convex functions | camelCase | `seekers.getProfile` |
| Convex tables | snake_case | `job_seeker_profiles` |
| Zod schemas | camelCase + "Schema" suffix | `jobListingSchema` |
| CSS variables | kebab-case, prefixed by shadcn convention | `--primary`, `--muted-foreground` |
| TypeScript types/interfaces | PascalCase | `JobListing`, `MatchScoreBreakdown` |
| Utility functions | camelCase | `displayToCents()`, `scoreSkills()` |
| Directories | kebab-case | `toggle-group/`, `work-experience/` |
| Environment variables | SCREAMING_SNAKE, `PUBLIC_` prefix for client | `PUBLIC_CONVEX_URL` |

---

## 6. Accessibility Requirements

### 6.1 WCAG 2.2 AA Baseline

All UI must meet WCAG 2.2 Level AA. Non-negotiable items:

- **No disabled submit buttons.** Use the FormErrorAlert pattern (§2.2) instead.
- **Color contrast:** 4.5:1 minimum for normal text, 3:1 for large text. The shadcn-svelte theme presets meet this — don't override colors without checking contrast.
- **Keyboard navigation:** Every interactive element is reachable via Tab and operable via Enter/Space. Swipe cards also support arrow keys.
- **Focus management:** After form submission errors, focus moves to the error alert. After navigation, focus moves to the main content area.
- **Labels:** Every input has a visible `<label>` via `Form.Label`. Placeholder text is not a substitute for a label.
- **Error identification:** Errors are associated to inputs via `aria-describedby` (handled by shadcn Form components). Error text describes what's wrong and how to fix it.
- **Motion:** Swipe animations respect `prefers-reduced-motion`. Provide an alternative (buttons) for users who disable motion.

### 6.2 Semantic HTML

Use proper elements. Do not use `<div>` with `on:click` when `<button>` exists.

```svelte
<!-- ✅ -->
<button on:click={handlePass}>Pass</button>
<nav aria-label="Main navigation">...</nav>
<main id="content">...</main>

<!-- ❌ -->
<div role="button" on:click={handlePass}>Pass</div>
<div class="nav">...</div>
```

---

## 7. Testing

### 7.1 Unit Tests (Vitest)

Required for MVP:

- **Scoring functions:** Unit tests for all four scoring functions in `convex/lib/scoring.ts`. These are pure functions and easy to test. Edge cases: empty skills, no experience, same city, different state, etc.
- **Zod schemas:** Snapshot or assertion tests to verify validation rules match BRD constraints.
- **Salary conversion:** Unit tests for `displayToCents` / `centsToDisplay` to catch rounding.

### 7.2 E2E Test Strategy (Playwright)

E2E tests cover the critical user flows end-to-end. They run against a live deployment (preview, develop, or production).

```bash
pnpm add -D @playwright/test
npx playwright install chromium  # single browser for CI speed
```

**Test directory structure** — organized by user flow, not by route:

```
tests/
├── fixtures/
│   ├── auth.ts              # Login/signup helpers, seeded test users
│   ├── seeder.ts            # Convex test data seeding utilities
│   └── pages/
│       ├── AuthPage.ts       # Page object: /auth
│       ├── SeekerDashboard.ts
│       ├── SeekerOnboarding.ts
│       ├── EmployerDashboard.ts
│       ├── JobPostingForm.ts
│       └── CandidateReview.ts
├── flows/
│   ├── seeker-signup.spec.ts
│   ├── employer-signup.spec.ts
│   ├── post-job.spec.ts
│   ├── employer-swipe.spec.ts
│   ├── seeker-opportunity.spec.ts
│   ├── profile-edit.spec.ts
│   └── listing-lifecycle.spec.ts
└── smoke/
    ├── foundation.spec.ts    # Scaffolding verification (T-001)
    └── smoke.spec.ts         # Production smoke subset
```

### 7.3 Required E2E Flows

Every flow listed here must have a passing Playwright test before MVP launch.

**Flow 1: Seeker Signup → Onboarding**
```
1. Navigate to /auth
2. Sign up with email + password, select "Looking for Work"
3. Verify redirect to /seeker/onboarding
4. Complete all 6 wizard steps:
   - Basic Info (name, city, state, zip)
   - Skills (add at least one)
   - Work Experience (add one entry)
   - Education (add one entry)
   - Certifications (skip — scaffolded only)
   - Job Preferences (set search radius)
5. Submit wizard
6. Verify redirect to /seeker/dashboard
7. Verify empty state message ("Your profile is live")
```

**Flow 2: Employer Signup → Onboarding**
```
1. Navigate to /auth
2. Sign up with email + password, select "Hiring"
3. Verify redirect to /employer/onboarding
4. Fill org form (business name, city, state, zip)
5. Submit
6. Verify redirect to /employer/dashboard
7. Verify dashboard shows org name + "Post New Job" CTA
8. Verify stats all show 0
```

**Flow 3: Post a Job**
```
1. (Employer logged in) Navigate to /employer/jobs/new
2. Fill all required fields:
   - Title, job type, description
   - At least one skill
   - Schedule type + days + hours
   - Salary range
   - Match volume (select 5)
   - Search radius
3. Submit
4. Verify redirect to /employer/dashboard
5. Verify new listing appears with "active" status
6. Verify candidate review count > 0 (matches were generated)
```

**Flow 4: Employer Swipe Flow**
```
1. (Employer with posted job) Navigate to candidates page
2. Verify "To Review" tab shows candidate cards
3. Verify card displays: match score, name, city, skills, summary
4. Verify card does NOT display: work history, education (tiered visibility)
5. Swipe right on first candidate
6. Verify undo toast appears briefly
7. Swipe left on second candidate
8. Verify undo toast appears, dismiss it
9. Switch to "Accepted" tab
10. Verify first candidate appears with "Awaiting Response" badge
```

**Flow 5: Seeker Opportunity Flow**
```
1. (Seeker who was liked by employer) Navigate to /seeker/dashboard
2. Verify opportunity count > 0
3. Verify opportunity card shows: job title, company, city, job type, salary, description
4. Click "Interview" (accept)
5. Verify card moves to "Awaiting Interview Booking" section
6. Verify "Schedule Interview" button is visible (if calendar link exists)
   OR "Employer will reach out" message (if no calendar link)
7. (Back on employer side) Verify accepted tab shows "Interview Accepted" badge
```

**Flow 6: Profile Edit**
```
1. (Seeker logged in) Navigate to /seeker/profile
2. Change full name
3. Add a new skill
4. Save
5. Refresh page
6. Verify changes persisted
```

**Flow 7: Listing Lifecycle**
```
1. (Employer with active listing) Navigate to dashboard
2. Pause listing → verify status badge changes to "paused"
3. Re-activate listing → verify status returns to "active"
4. Close listing → verify "closed" status
5. Re-open listing → verify "active" status
6. Delete listing → verify removed from dashboard
```

**Smoke Suite (Production)**
```
Subset of the above — runs against production after release deploy:
1. Login with pre-seeded test account (seeker)
2. Verify /seeker/dashboard loads
3. Login with pre-seeded test account (employer)
4. Verify /employer/dashboard loads
5. Verify navigation to job posting form works
6. One happy-path: post job → verify matches generated → swipe right → verify seeker sees opportunity
```

### 7.4 Playwright Patterns

**Page Object Model:** Every page has a corresponding class in `tests/fixtures/pages/`. Page objects encapsulate selectors and actions — tests read like user stories.

```typescript
// tests/fixtures/pages/SeekerDashboard.ts
import { type Page, type Locator } from '@playwright/test';

export class SeekerDashboard {
  readonly page: Page;
  readonly opportunityCount: Locator;
  readonly firstOpportunityCard: Locator;
  readonly acceptButton: Locator;
  readonly passButton: Locator;
  readonly awaitingSection: Locator;

  constructor(page: Page) {
    this.page = page;
    this.opportunityCount = page.getByTestId('opportunity-count');
    this.firstOpportunityCard = page.getByTestId('opportunity-card').first();
    this.acceptButton = page.getByTestId('accept-button');
    this.passButton = page.getByTestId('pass-button');
    this.awaitingSection = page.getByTestId('awaiting-interview-section');
  }

  async acceptFirstOpportunity() {
    await this.acceptButton.click();
  }

  async passFirstOpportunity() {
    await this.passButton.click();
  }

  async getOpportunityCount(): Promise<number> {
    const text = await this.opportunityCount.textContent();
    return parseInt(text ?? '0', 10);
  }
}
```

**Selectors — `data-testid` everywhere:**

Every interactive element and every element asserted against in tests must have a `data-testid` attribute. This is a development requirement, not a testing afterthought.

```svelte
<!-- ✅ Good -->
<button data-testid="accept-button" on:click={handleAccept}>Interview</button>
<span data-testid="opportunity-count">{count}</span>
<div data-testid="opportunity-card">...</div>

<!-- ❌ Bad — test selects by text or CSS class -->
<!-- page.getByText('Interview') — brittle, breaks on copy change -->
<!-- page.locator('.btn-primary') — coupled to styling -->
```

**No `sleep()` calls.** Playwright auto-waits for elements. Use explicit waits only when necessary:

```typescript
// ✅ Good — Playwright auto-waits for the element
await expect(page.getByTestId('opportunity-card')).toBeVisible();

// ✅ Good — explicit wait for network-dependent state
await page.waitForResponse((r) => r.url().includes('convex') && r.status() === 200);

// ❌ Bad
await page.waitForTimeout(3000);
```

**Test isolation:** Each test creates its own users and data via the seeder fixture. Tests do not depend on state from other tests. Tests can run in parallel.

**Playwright config:**

```typescript
// playwright.config.ts
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 2 : undefined,
  reporter: process.env.CI ? [['html'], ['github']] : 'list',
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? 'http://localhost:5173',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
  ],
});
```

### 7.5 Manual Testing Checklist

Before any feature is considered complete:

- [ ] Works with keyboard only (no mouse)
- [ ] Screen reader announces form errors correctly
- [ ] Works at 200% browser zoom
- [ ] Loading and error states render
- [ ] Data persists after page refresh
- [ ] All interactive elements have `data-testid` attributes

---

## 8. Git Conventions

### 8.1 Commit Messages

```
feat(seeker): add onboarding wizard step 2 (skills)
fix(matching): handle empty skills array in scoring
refactor(listings): extract schedule section component
chore: update shadcn-svelte components
```

Prefix with ticket ID when applicable: `T-005: feat(seeker): add onboarding wizard`

### 8.2 Branch Strategy

```
main              ← production, always deployable
├── feat/T-005-seeker-onboarding
├── feat/T-011-post-job
├── fix/T-013-empty-skills-scoring
```

One branch per ticket. PR into main. Squash merge.

---

## 9. Code Style Enforcement

### 9.1 Toolchain

| Tool | Scope | Role |
|------|-------|------|
| **Biome** | `.ts`, `.js`, `.json` | Linting + formatting for all non-Svelte files (Convex functions, schemas, utils, server files) |
| **eslint-plugin-svelte** | `.svelte` | Svelte-specific lint rules (reactivity, a11y, template logic) |
| **prettier-plugin-svelte** | `.svelte` | Svelte template formatting |
| **svelte-check** | `.svelte` + `.ts` | TypeScript diagnostics inside Svelte components |
| **Lefthook** | Pre-commit | Orchestrates all checks before code enters the repo |

Biome handles the majority of the codebase. ESLint + Prettier are scoped to `.svelte` files only — Biome cannot parse Svelte templates.

### 9.2 Biome Configuration

```jsonc
// biome.json
{
  "$schema": "https://biomejs.dev/schemas/1.9.0/schema.json",
  "organizeImports": { "enabled": true },
  "files": {
    "include": ["src/**/*.ts", "src/**/*.js", "convex/**/*.ts", "*.json"],
    "ignore": [
      "node_modules",
      ".svelte-kit",
      "build",
      "src/**/*.svelte"
    ]
  },
  "linter": {
    "enabled": true,
    "rules": {
      "recommended": true,
      "complexity": {
        "noExcessiveCognitiveComplexity": { "level": "error", "options": { "maxAllowedComplexity": 15 } },
        "noForEach": "error",
        "useFlatMap": "error"
      },
      "correctness": {
        "noUnusedImports": "error",
        "noUnusedVariables": "error",
        "useExhaustiveDependencies": "error"
      },
      "style": {
        "noNonNullAssertion": "error",
        "useConst": "error",
        "useImportType": "error",
        "useBlockStatements": "error",
        "noParameterAssign": "error"
      },
      "suspicious": {
        "noExplicitAny": "error",
        "noConsoleLog": "warn"
      },
      "performance": {
        "noAccumulatingSpread": "error"
      }
    }
  },
  "formatter": {
    "enabled": true,
    "indentStyle": "space",
    "indentWidth": 2,
    "lineWidth": 100
  },
  "javascript": {
    "formatter": {
      "quoteStyle": "single",
      "trailingCommas": "all",
      "semicolons": "always"
    }
  }
}
```

### 9.3 ESLint Configuration (Svelte Only)

```javascript
// eslint.config.js
import svelte from 'eslint-plugin-svelte';
import svelteParser from 'svelte-eslint-parser';
import tsParser from '@typescript-eslint/parser';

export default [
  {
    // Only lint .svelte files — Biome handles everything else
    files: ['**/*.svelte'],
    languageOptions: {
      parser: svelteParser,
      parserOptions: {
        parser: tsParser,
        project: './tsconfig.json',
        extraFileExtensions: ['.svelte'],
      },
    },
    plugins: { svelte },
    rules: {
      // Svelte-specific
      ...svelte.configs.recommended.rules,
      'svelte/no-at-html-tags': 'error',
      'svelte/require-each-key': 'error',
      'svelte/no-unused-svelte-ignore': 'error',
      'svelte/valid-compile': 'error',

      // Accessibility — strict
      'svelte/a11y-click-events-have-key-events': 'error',
      'svelte/a11y-no-static-element-interactions': 'error',
      'svelte/a11y-missing-attribute': 'error',
      'svelte/a11y-label-has-associated-control': 'error',
      'svelte/a11y-no-noninteractive-element-interactions': 'error',
    },
  },
  {
    ignores: ['**/*.ts', '**/*.js', '**/*.json', 'node_modules', '.svelte-kit', 'build'],
  },
];
```

### 9.4 Prettier Configuration (Svelte Only)

```jsonc
// .prettierrc
{
  "plugins": ["prettier-plugin-svelte"],
  "overrides": [
    {
      "files": "*.svelte",
      "options": {
        "parser": "svelte",
        "svelteIndentScriptAndStyle": true,
        "singleQuote": true,
        "trailingComma": "all",
        "printWidth": 100
      }
    }
  ]
}
```

```
// .prettierignore — Biome handles these
*.ts
*.js
*.json
node_modules
.svelte-kit
build
```

### 9.5 TypeScript Configuration

SvelteKit generates its own base tsconfig at `.svelte-kit/tsconfig.json` with settings required for route type generation, `$types` imports, and Vite compatibility. We extend it and add strictness flags on top.

**Do not override** any of the following — they are managed by SvelteKit: `verbatimModuleSyntax`, `isolatedModules`, `noEmit`, `moduleResolution`, `module`, `target`, `lib`, `rootDirs`, `paths`.

```jsonc
// tsconfig.json
{
  "extends": "./.svelte-kit/tsconfig.json",
  "compilerOptions": {
    // Already enabled by SvelteKit scaffold — listed for clarity
    "strict": true,

    // Additional strictness (safe with SvelteKit)
    "noUncheckedIndexedAccess": true,        // array/object index returns T | undefined
    "noImplicitOverride": true,              // require 'override' keyword
    "noFallthroughCasesInSwitch": true,      // require break/return in switch cases
    "forceConsistentCasingInFileNames": true, // catch case-sensitive import mismatches
    "noImplicitReturns": true,               // all code paths must return
    "allowUnreachableCode": false,           // error on dead code

    // Strict but occasionally requires explicit handling with Svelte optional props
    // and some third-party libraries. When a type error appears on an optional prop,
    // use `prop?: string | undefined` explicitly rather than disabling the flag.
    "exactOptionalPropertyTypes": true,

    // Do NOT add these — SvelteKit manages them:
    // "verbatimModuleSyntax", "isolatedModules", "noEmit",
    // "moduleResolution", "module", "target", "lib"
  }
}
```

**Why `exactOptionalPropertyTypes`?** It distinguishes between "property is missing" and "property is explicitly `undefined`". This catches bugs where `undefined` is accidentally passed to an API that doesn't expect it. The tradeoff: some Svelte components and libraries use `{ prop?: T }` expecting you can pass `undefined`. Fix these cases by widening the type to `T | undefined` at the call site — don't disable the flag.

### 9.6 Lefthook Configuration

```yaml
# lefthook.yml
pre-commit:
  parallel: true
  commands:
    biome-check:
      glob: "*.{ts,js,json}"
      run: npx biome check --write {staged_files}
      stage_fixed: true

    eslint-svelte:
      glob: "*.svelte"
      run: npx eslint --fix {staged_files}
      stage_fixed: true

    prettier-svelte:
      glob: "*.svelte"
      run: npx prettier --write {staged_files}
      stage_fixed: true

    svelte-check:
      run: npx svelte-check --tsconfig ./tsconfig.json

    typecheck:
      run: npx tsc --noEmit
```

### 9.7 Package Scripts

```jsonc
// package.json (scripts)
{
  "scripts": {
    "dev": "vite dev",
    "build": "vite build",
    "preview": "vite preview",
    "check": "svelte-check --tsconfig ./tsconfig.json",
    "lint": "biome check src/ convex/ && eslint 'src/**/*.svelte'",
    "lint:fix": "biome check --write src/ convex/ && eslint --fix 'src/**/*.svelte'",
    "format": "biome format --write src/ convex/ && prettier --write 'src/**/*.svelte'",
    "validate": "pnpm check && pnpm lint && tsc --noEmit"
  }
}
```

`pnpm validate` is the single command that runs everything. CI runs this. Lefthook runs the same checks per-file on commit.

---

## 10. CI/CD Pipeline

### 10.1 Pipeline Overview

```
PR into main
├── lint + format + typecheck ──┐
│   (biome, eslint, svelte-     │  parallel
│    check, tsc --noEmit)       │
├── unit tests ─────────────────┘
│   (scoring, schemas, salary)
│       │
│       ▼ (on pass)
├── deploy to Vercel Preview
│       │
│       ▼
└── Playwright E2Es against preview URL

Merge into main
├── lint + format + typecheck ──┐
│                               │  parallel
├── unit tests ─────────────────┘
│       │
│       ▼ (on pass)
├── deploy to develop environment
│       │
│       ▼
└── Playwright E2Es against develop URL

Release (tag)
├── deploy to production
│       │
│       ▼
└── Playwright smoke tests against production URL
```

### 10.2 Workflow Details

**PR Workflow** (`.github/workflows/pr.yml`):

| Stage | Trigger | Runs | Depends on |
|-------|---------|------|------------|
| Quality | `pull_request → main` | `pnpm validate` (biome, eslint, svelte-check, tsc) | — |
| Unit Tests | `pull_request → main` | `pnpm test` (vitest — scoring, schemas, salary) | — |
| Preview Deploy | Quality ✅ + Unit Tests ✅ | Deploy to Vercel preview via CLI or GitHub integration | Quality, Unit Tests |
| E2E Tests | Preview Deploy ✅ | Playwright against preview deployment URL | Preview Deploy |

Quality and Unit Tests run **in parallel**. Preview deploy waits for both to pass. E2E runs last against the live preview.

**Main Workflow** (`.github/workflows/main.yml`):

| Stage | Trigger | Runs | Depends on |
|-------|---------|------|------------|
| Quality | `push → main` | `pnpm validate` | — |
| Unit Tests | `push → main` | `pnpm test` | — |
| Develop Deploy | Quality ✅ + Unit Tests ✅ | Deploy Convex to staging + SvelteKit to develop environment | Quality, Unit Tests |
| E2E Tests | Develop Deploy ✅ | Playwright against develop URL | Develop Deploy |

Same structure as PR, different deployment target.

**Release Workflow** (`.github/workflows/release.yml`):

| Stage | Trigger | Runs | Depends on |
|-------|---------|------|------------|
| Production Deploy | Tag push (`v*`) | Deploy Convex to production + SvelteKit to production | — |
| Smoke Tests | Production Deploy ✅ | Playwright smoke test suite against production URL | Production Deploy |

Releases are created by tagging. No quality/unit gate — the merge into main already passed those.

### 10.3 Environment Map

| Environment | Convex | Vercel | When |
|-------------|--------|--------|------|
| Preview | Dev backend | Preview deploy (unique URL per PR) | PR opened/updated |
| Develop | Staging backend | Develop deploy (stable URL) | Merge to main |
| Production | Production backend | Production deploy | Tag push (`v*`) |

### 10.4 Playwright Test Suites

| Suite | Scope | Runs against |
|-------|-------|-------------|
| Full E2E | All critical paths (signup → onboarding → post job → match → swipe → respond) | Preview, Develop |
| Smoke | Login, dashboard loads, core navigation, one happy-path match flow | Production |

The smoke suite is a subset of the full suite — fast enough to run against production without risk of test data pollution.
