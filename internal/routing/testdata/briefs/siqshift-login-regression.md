# Brief siqshift-login-regression

## Project

projects/clock-in (repo = SIQshift, github fpresta0607/SIQshift)

## Task - Sign-in page: version-mismatch rejection + missing sine wave background

The Overlord is on the deployed SIQshift sign-in page right now and reports:
1. Sign-in fails with the app's own error: "The server would not accept that
   request. This page and the server may be running different versions.
   Reload, and tell an admin if it keeps happening."
2. The sine wave animated background that used to be behind the sign-in page
   is gone.

Recent context: PRs #49 (pin-sql-line-endings), #50 (agents-tab-people),
#51 (activity-accuracy) merged recently; the deployed server is presumably
newer than the page the browser holds, or the deploy shipped mismatched
client/server artifacts.

1. FIND the deployment (Fly/Vercel/other - read the repo's deploy config) and
   determine deployed server version vs deployed client bundle version. Root-
   cause the mismatch: is it a stale client cache the app should survive, a
   deploy that shipped server without client (or vice versa), or an API
   contract change in #49-#51 that the version guard correctly catches?
2. FIX at the root:
   - If deploy skew: make the deploy atomic (client+server ship together) or
     make the client auto-recover (force-reload once on version mismatch
     instead of telling the user to reload manually).
   - If contract change: fix the compatibility properly.
3. SINE WAVE: git-bisect/log the sign-in page for where the sine wave
   background was lost (suspects: the Clock-In -> SIQshift rename sweep, or a
   layout/PR in the #45-#51 range). Restore it faithfully (same animation),
   unless it was deliberately removed in a commit that says so - then report
   that instead of restoring.
4. VERIFY on the live deployment in a real browser: sign-in page renders with
   the sine wave, and a real sign-in attempt is accepted (use a test account
   if one exists; do NOT use the Overlord's credentials). Screenshot
   before/after.
5. PR against main -> merge chain via CFO -> confirm the production deploy
   settles and the live page is fixed.

## Constraints

- SUBSCRIPTION only; usage/quota error -> STOP and `cfo notify --blocked`.
- If further defects surface across rounds, REASSESS and fix root causes
  (minimal diff, documented); park only for spend/destructive/production
  writes beyond the deploy itself.
- no-mistakes standing policy; rebase before PR; do not merge your own PR.
- Report `cfo notify siqshift-login-regression --done --pr <url>`.
