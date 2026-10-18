# Use the board from your phone

Code Goblins runs locally by default. Remote access is optional for every
installation: the Windows app discovers that PC's Tailscale name and signed-in
owner account. No personal hostname, account or key is included in the build.

## Set up once

1. Install and connect [Tailscale](https://tailscale.com/download) on the Windows
   PC and phone, using the same personal Tailscale account. A tagged PC cannot
   use owner-only access. Tailnet access rules must allow the phone to reach the
   PC on TCP port 4311.
2. Open Code Goblins on the PC. Open the **CFO** panel, choose **Task**, expand
   **Remote access**, and turn the **Tailscale** switch on. The board finds
   Tailscale by itself and offers the switch once Tailscale is installed and
   signed in on the PC; until then a few words under it say what is missing,
   such as Not installed or Signed out, and it reads Tailscale again whenever
   you come back to the board.
3. Copy the private HTTPS link shown there. On the phone, connect Tailscale and
   open the link in its browser. Open the CFO terminal to control the existing
   session. To keep the board beside your apps, use Safari's **Share → Add to
   Home Screen** on an iPhone, or Chrome's menu **→ Add to Home screen** on
   Android.

The panel checks the signed-in owner and reads back the exact private proxy
before calling it ready, which a green check beside the switch says; an amber
triangle says something is wrong, and its tip says what. The section has no
explanatory text: the private link to copy while it is on and, on the PC, the
command that mends what is missing under the switch it is for, to copy or to run
with its play button. Play runs that command, only while it is still the one the
switch shows, in a PowerShell terminal drawn in the section, as a Command Center
run card runs its command; that terminal opens only on the PC, it never becomes
a Command Center item, and the section reads the switch again when it ends. It
does not connect the phone's VPN; Tailscale handles that on each device. No
developer checkout, Go installation or account-specific configuration is needed
with a released build carrying this feature.

Tailscale may require one-time administrator permission or HTTPS approval; the
switch then says HTTPS not approved, and under it the board shows the exact
`tailscale serve --bg --https=4311 http://127.0.0.1:4310` command (the port
follows your actual running board). Press its play button, or run it in a
PowerShell on the PC, as Administrator if Tailscale asks; follow Tailscale's
approval link, then turn the switch on: it takes the route that command made as
its own. The same command shows after a turn-on that waited on approval. Code
Goblins never follows an approval link for you, and turning remote access on
never logs it; a command run with play keeps the end of what its terminal
showed, as a run card does, which can hold that link. Existing services on port
4311 are left unchanged; free that port in Tailscale before enabling this
feature. Without Tailscale the command under the switch installs it with winget.

Tailscale for [Windows](https://tailscale.com/download/windows),
[iPhone](https://tailscale.com/download/ios) and
[Android](https://tailscale.com/download/android) comes from Tailscale's own
downloads, and [HTTPS setup](https://tailscale.com/docs/how-to/set-up-https-certificates)
needs the tailnet's DNS settings and permissions; the first Serve command can
print the exact approval link in your terminal. Turning remote access on times
out that command instead of waiting forever; run with play, it waits in its
terminal until you approve it or press Stop.

## Scrawl reviews

Once Tailscale is on, **Scrawl reviews** shows under it, indented, as a switch
of its own; turn it on on the PC. It is separate so the board and CFO terminal
work even if Scrawl needs setup, and a phone can turn it off but not on. Beside
it a green check says reviews reach your phone, or an amber triangle and a word
or two say what is wrong: Direct route when a Tailscale route of your own
already serves Scrawl to your tailnet without the owner check, Not private,
Other server, Update Scrawl or Needs setup. A stopped Scrawl counts as on: the
next review a phone opens starts it. On the PC, the command that removes a
direct route of your own shows under the switch, to copy or to run with its
play button. Turning it off removes
only Scrawl's own route on port 4312 and keeps the board's; the phone's open
views reconnect, as they do when it turns on. Turning it on uses the bundled
Scrawl protocol (lavish-axi v0.1.79-codegoblins.16 or a compatible newer build),
discovers the configured local port and state directory, and verifies the actual
installation.

Setup restarts a non-private Scrawl server with its supported `stop` command,
preserving saved review sessions and queued feedback. Open review pages
reconnect; active CLI polls may need to retry. It then serves Scrawl through
its own owner-authenticated HTTPS origin on port **4312**. Tailnet access rules
must allow that port too. Other services on that port are left unchanged.
Review links on the phone are validated and redirected there, keeping the
session, query and fragment. Local board links stay local. Other websites
are never proxied.

Existing numeric loopback and this PC's discovered Tailscale-IP review links
are accepted at the configured Scrawl port too. A previous custom HTTPS link
needs the review reopened on the PC to refresh its saved address. Setup refuses
an existing Serve route (including raw TCP forwarding) that points directly at
Scrawl, because it would bypass the owner gate. Remove that route in Tailscale
first; Code Goblins never removes another service's route for you.

New CFO and goblin terminals and Code Goblins' own Scrawl calls start Scrawl
on loopback. No global environment settings are changed. **One-time upgrade
step:** restart or resume CFO and goblin terminal sessions created before this
update through Code Goblins' existing controls when their work is safe. Their
work and saved sessions remain; setup never restarts those terminals for you.
An older terminal keeps its old environment and can start Scrawl's automatic
tailnet listener after idle shutdown, a crash or an upgrade, even if it never
explicitly set a network address. A healthy current server does not prove those
future launches are private. Use `cfo review` from an external shell, or set
`$env:LAVISH_AXI_HOST = '127.0.0.1'` before directly running `lavish-axi` there.
If another process starts a network listener, remote Scrawl refuses requests
and its status asks for setup again.
The gate checks both bound and requested listeners, since Scrawl may retry a
temporarily unavailable address. A different installation or older server
without its identity is never adopted or stopped. Upgrade or resolve the
port conflict on the PC, then retry. An idle Scrawl server is safely started
again when an authorized owner opens a review.

Scrawl has a separate origin because review HTML must not gain the board's API
or terminal access. Its existing artifact sandbox headers and WebSockets are
preserved. Turning Tailscale off, and uninstall, revoke and remove both owned
routes.

## Staying connected

Turning Tailscale on also turns on Code Goblins' existing **Start at login**
setting. Tailscale's background proxy and the saved owner setting survive
restarts; the board and its sessions return after Windows sign-in. The PC must
be awake, online, signed in, and connected to Tailscale. This does not change
power settings, Windows sign-in policy, router ports or the firewall, and cannot
wake a powered off PC. Closing the phone's browser or disconnecting the phone
leaves the CFO and workers running.

Turning the **Tailscale** switch off revokes remote requests immediately
and closes existing remote streams within one second. It detaches their views;
it never stops a terminal, worker or local board. Only this feature's exact
proxy is removed, and other Tailscale Serve routes stay untouched. Uninstall
performs the same cleanup before removing its ownership record. If cleanup
fails, the record remains and uninstall reports how to finish.

## Terminal commands

```powershell
cfo remote on
cfo remote status
cfo remote off
```

`on` requires this build's board to be running and prints the same phone link.
`status` verifies the owner, device name, board address and Serve route, and
reports whether the board and Start at login are running; while access is off
it says what this PC's Tailscale lacks and the command that mends it, as the
board's switch does. If the PC's Tailscale name, account or board address
changes, turn remote access off and on again on the PC. `off` revokes
access even when Tailscale is stopped, then reports any remaining proxy cleanup.
`on` and `off` are refused in a goblin's or a gate agent's terminal: only you,
or the CFO on your word, turn remote access on or off. `cfo doctor` reports it
in one `remote access:` line, off, ready or what it needs, without naming your
tailnet or account.

A board started before Tailscale was installed finds Tailscale where its
installer puts it. Without Tailscale, the switch says Not installed; with
Tailscale stopped, signed out or disconnected, it says which. The board and
Scrawl keep working on this PC either way.

## Security boundary

The board keeps its numeric loopback listener. Tailscale Serve provides HTTPS
on port 4311 inside the tailnet, and Code Goblins accepts only the exact saved
HTTPS host and the signed-in owner's identity supplied by that local proxy.
Other tailnet users, shared-node guests, tagged devices, missing identities and
public Funnel requests are refused. A Tailscale ACL still governs which of the
owner's devices may reach the PC.

The gate checks browser Origin before translating it to the local board and
retains the existing mutation and terminal WebSocket token checks. Forwarding
headers remain attached, so credential entry, AFK switches and other existing
PC-only actions remain PC-only. The local process trust boundary is unchanged:
this does not authenticate against a process already running on the PC. Do not
point a different reverse proxy or Funnel at the board.

Tailscale's [Serve documentation](https://tailscale.com/docs/features/tailscale-serve)
describes the identity headers and loopback trust boundary; its
[CLI reference](https://tailscale.com/docs/reference/tailscale-cli/serve)
documents persistent `--bg` setup.
