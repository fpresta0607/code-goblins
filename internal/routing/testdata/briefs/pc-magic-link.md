# Brief: magic-link sign-in for Peak Craftsman

Replace password sign-in with a Supabase magic link, so the client never receives a password by email and never needs one reset.

Do not use em dashes anywhere, in code, comments, docs or output.

## Why

The app has exactly one user, `client@example.com`, created by hand in the Supabase dashboard.
Today `/login` is an email plus password form and there is no password reset anywhere in the app: no reset link, no token-exchange route, no email provider wired.
That means the only way to hand the client access is to email him a password that he can never change, and the only recovery from a lockout is the Overlord editing Supabase by hand.
A magic link removes both problems.

## Read first

- `docs/bob-onboarding-readiness.md`, section "Around the login form (built vs missing)". It is an evidence-based report written 2026-09-02 and is accurate.
- `docs/architecture.md`
- `src/app/login/page.tsx` - the current password form
- `src/proxy.ts` - the session refresh and route guard

## What to build

**1. `/login` becomes a magic-link request.** One email field, one button. On submit call
`supabase.auth.signInWithOtp({ email, options: { shouldCreateUser: false, emailRedirectTo: <origin>/auth/confirm } })`.

`shouldCreateUser: false` is mandatory and is the single most important line in this task.
The default is `true`, which would silently create an account for any address typed into the box, in an app whose entire isolation model is "Bob is the only user".
Show the same neutral "check your email" confirmation whether or not the address exists, so the form never reveals who has an account.

**2. Add `/auth/confirm`, the token-exchange route this app has never had.**
Read `token_hash` and `type` from the query string, call `verifyOtp`, set the session cookie, redirect to `/`.
On a bad or expired link render a plain page saying the link expired with a link back to `/login`. Never a silent redirect, and never a loop.

**3. Fix the route guard, which will otherwise eat the link.**
`src/proxy.ts:34` redirects every unauthenticated path except `/login` to `/login`.
A visitor arriving on `/auth/confirm` from his email is unauthenticated by definition, so today the guard would bounce him before the exchange runs and the link would appear broken.
Allow `/auth/confirm` through unauthenticated. Prove it with a test, not by reading the code.

**4. Leave the password path intact in Supabase.** Stop showing the password form; do not disable password auth on the project.
It stays as the Overlord's recovery lever if email delivery ever fails.

**5. Email delivery is the risk in this task, not the code.**
Supabase's built-in mailer is rate limited and sends from a shared address, so magic links land in spam routinely.
Report what the project uses today and exactly what wiring custom SMTP would take, with the rate limits named.
Do not sign the project up for any service and do not add a dependency for this.

**6. Tests.** Cover the login submit path, the confirm route on both a good and an expired token, and the guard exemption.
`npm run test`, `npm run lint` and `npm run build` all stay green.

## Dashboard steps you cannot perform

Document these in your report for the Overlord to execute. Do not attempt them and do not treat them as blockers.

- Authentication, URL Configuration: Redirect URLs must include `https://peak-craftsman.vercel.app/**`. Site URL is already `https://peak-craftsman.vercel.app`.
- Authentication, Sign In / Up: "Allow new users to sign up" must be OFF. It was still ON as of 2026-08-31.
- The magic-link email template must point at the confirm route you build.

## Constraints

- Match the existing patterns in this repo. Read why something is written the way it is before replacing it.
- No new dependencies. No abstraction with one caller.
- `origin` is `https://github.com/fpresta0607/peakCraftsman`, the Overlord's own repo. Push there and nowhere else, and confirm before opening a PR.
  The project used to live at `gcaruso-precisiondocs/peakCraftsman`, which is a different account. That remote is still present as `gcaruso` for fetching, its push URL is hard-disabled, and the no-mistakes gate has been repointed. Never push, open a PR, or file an issue there.

## Deliverable

Write your report to `docs/magic-link-report.md` and commit it on your branch so it survives worktree cleanup.
Report done with `cfo notify pc-magic-link --done --pr <url>`.
