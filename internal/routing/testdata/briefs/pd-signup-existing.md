# Signup page: detect an existing account and offer "Log in instead"

Repo: `fpresta0607/PrecisionDocs-AI` (the Overlord's own repo). Branch from `main`, one small PR,
`no-mistakes` gate on real CI. Keep the diff small: this is one component and one context method.

## What the Overlord wants

"What happens if users enter an account that's already created on the sign up page? They should
just be logged in, or it should detect when they type in their email address and say: you already
have an account, with a button that says Log in instead."

## Build

1. In `frontend/src/components/auth/EmbeddedAuth.tsx` (signup path, around the `signUp` call) and
   `frontend/src/context/AuthContext.tsx` (`supabase.auth.signUp` around line 277): detect the
   existing-account case. With Supabase email confirmation on, `signUp` for an existing confirmed
   email returns a user with an empty `identities` array and no error; without confirmation it
   returns an "already registered" error. Handle both.
2. On detection, do not show a generic error. Show, inline under the email field:
   "You already have an account with this email." and a primary button `Log in instead` that
   switches to the login form with the email prefilled. If the password they typed is correct, you
   may attempt `signInWithPassword` first and log them straight in; on failure fall back to the
   message. Never reveal whether an email exists to an unauthenticated user beyond this signup
   flow (no enumeration endpoint, no change to the login form's error wording).
3. Header on the marketing host: the auth link reads `Log in` (not Login, not Sign in). Coordinate
   with the open home-page PR from `gb-pd-home-ctas`, which also touches `NewHeader.tsx`; rebase
   on main after it lands if needed, and keep to the label change only.
4. Tests: existing email with confirmation on (empty identities) shows the message and the button;
   the button switches to login with the email prefilled; a fresh email still signs up; the
   already-registered error path shows the same message.
5. Verify in a real browser against the local build with a Supabase test account, screenshots at
   1440 and 390 in the PR body.

## Constraints

- No new dependencies, no `.env` reads, no secrets in the PR.
- Do not touch billing, onboarding, or MFA flows.
- One PR against `fpresta0607/PrecisionDocs-AI`, never another remote.
