---
name: step-card
description: Send the Supreme Overlord one Command Center card when browser or computer-use automation reaches a step only he can take, and pick up the moment it closes. Use whenever a page you drive asks to create an account, sign in, solve a CAPTCHA, accept terms, pass an identity check, enter a code from his phone, or copy a token or key, and whenever you are about to ask him for such a step in a message or a Scrawl walkthrough.
---

# step-card

A browser or computer-use run that reaches a step only the Supreme Overlord can take stops there and sends him one card.
You never take that step yourself: never create an account, sign in to his accounts, solve a CAPTCHA, accept terms or pass an identity check for him.
You never send the step as a chat message, a notify or a Scrawl walkthrough either.

## Send the card

```
cfo auth request --project <project> --task <your task id> --why "<what the step is for>" --link <the exact https page> [--step "<a click he makes>"]... [--plain NAME --label NAME="<a few words>"]... [SECRET_NAME]... --wait
```

- `--why` is the card's heading when it asks for nothing, so make it the step itself, such as "Create the shop's test account".
- `--link` is the exact screen he acts on, not the site's home page, with no query or fragment.
- A `--step` is one click or choice he makes there, in order, as few as the step needs.
  Do not add a step that only says to open the link: the card already does.
- Ask only for what he must enter.
  A secret, named by itself, goes straight into the project's vault and reaches you only through your credentials, never in your conversation.
  A `--plain` value, such as an account's email or a confirmation code, comes back to you.
  Give each input a `--label` of a few words he understands.
- A card that asks for nothing gets a Done button.

Run the command in the background, so you go on the moment the card closes.
When it ends it prints what came of the card:

- exit 0 once the step is done or every input arrived, with each plain value as `NAME=value` and each secret by its name alone,
- exit 3 when he closed the card: go on without it,
- exit 1 when the card was withdrawn or expired, with why.

## A step inside your own browser session

When the step must happen inside your own browser session, such as a sign-in the session must keep, add `--window`.
The card then has a button that shows your session's window at the link on his screen.
Your session restarts on a profile in your task's folder, so start the flow from that link.
Once the card closes your session runs headless again on the same profile and keeps what he did there.

## When you see the step done yourself

Keep checking the page while you wait, and when you see the step done, such as the signed-in dashboard, close the card:

```
cfo auth request --done <card> --task <your task id>
```

Take back a card you no longer need with `cfo auth request --withdraw <card> --task <your task id> --reason "<why>"`.
Wait on a card again after your wait was cut short with `cfo auth request --wait-on <card> --task <your task id>`.
Your open cards are withdrawn for you once you report done or failed.
