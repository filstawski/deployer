# Android app

The Android app manages a server's deployments from your phone. You can:

- deploy a branch, and update, stop, start, extend, or destroy a deployment;
- open a deployment's URL and follow its logs;
- get notified when a deployment is ready, fails, or is about to expire;
- see your deployments at a glance on the home screen.

It uses the same [HTTP API](api.md) as the CLI, and needs Android 8.0 or newer.

## Install

Each [release](https://github.com/filstawski/deployer/releases/latest) with an
APK has it as `deployer-android_vX.Y.Z.apk`. Open the link on the phone, download
the APK, and open it. Android asks once to allow installing apps from your
browser. To update, install the next release's APK the same way. The app tells
you when the server runs a newer release than the app.

To build it yourself instead, you need JDK 17 and the Android SDK; [Android
Studio](https://developer.android.com/studio) installs both:

```sh
cd android
./gradlew assembleRelease
adb install app/build/outputs/apk/release/app-release.apk
```

A build you make is signed with your computer's debug key. Android won't install
it over the published app, or a build from another computer. Uninstall the app
first, then connect again.

## Connect

On a computer where the CLI is logged in as an administrator, run:

```sh
deployer pair                     # a token named phone: deployer role, all projects, 90 days
deployer pair tablet --role viewer    # can look, can't change anything
```

It creates a token for the phone and shows it as a QR code. In the app, tap
**Scan QR code** and point the camera at it. The app checks the token with the
server, then saves it. Can't scan? `deployer pair` also prints the address and
token to type in.

The code holds the token's secret, so show it only to the phone it's for. The
phone connects to the server's public HTTPS address. If the CLI reaches the
server over SSH and the server has no `api.public_url`, pass the address:
`deployer pair --server https://api.previews.example.com`.

The token is encrypted with a key that stays in the phone's keystore, and is left
out of backups and device transfers. To add or remove a server, tap the server
name above the deployment list. Removing a server forgets its token on the
phone. To make the token stop working, revoke it; the server list shows its ID:

```sh
deployer token revoke tok_3f9a2c7d1e5b
```

## What it does

- **Deployments.** Each one's state, project, branch, and time left, and how many
  are live out of the server's limit. Filter by state, and pull down to refresh.
  The list refreshes every 10 seconds, and every 3 while something is building.
  Deployments with less than an hour left offer **Extend** right in the list.
- **Deploy.** Choose a project and a branch, or type a branch or commit. The app
  suggests a name and offers the server's lifetimes, like `deployer deploy`. For
  a branch that already has a preview, it offers to update that one, or you can
  create another.
- **A deployment.** Open, copy, or share its URL. Extend it by up to what the
  server's `max_ttl` still allows. Update it to the latest commit of its branch,
  stop or start it, or destroy it. Update, stop, and destroy ask first. While it
  builds, including an update started from another device, the app shows the
  end of the build log. A failed deployment shows why, and the hint.
- **Logs.** The app's output or the build log, following new lines as they
  arrive, and starting over when a new build begins. Scroll up to read;
  **Latest** jumps back. Copy or share the log (the newest 100,000 characters
  of a longer one).

A token with the `viewer` role sees everything, without the buttons that change
things.

## Notifications

The app notifies you when a deployment becomes ready or fails, when an update
finishes or fails, and when a running deployment has less than an hour left.
That last notification has buttons to extend it. On Android 13 and later, the
deployment list asks once whether to turn notifications on. You can switch them
on or off in the server list.

The server doesn't push anything; the phone asks it. Android lets apps check in
the background about every 15 minutes, and less often when the battery is low.
After you deploy, update, or start something from the phone, the app checks
every 10 seconds until that is done, so you hear about it within seconds. While
the app is open, it shows changes on screen instead of notifying.

## Widget

Long-press the home screen, choose **Widgets**, and add **Deployer**. It shows the
current server's deployments, most urgent first, and refreshes with the app's
background checks. Tap a deployment to open it.

## Limitations

- The server's API must be reachable over HTTPS. `ssh://` connections work only
  from the CLI.
- Background notifications can arrive up to about 15 minutes late, except for
  work you started from the phone.
