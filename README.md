# Stream Dock Manager plugins

Add media controls, timers, system information, weather, and connected services to
[Stream Dock Manager](https://github.com/StreamDockManager/Manager). Install just the plugins
you want. These native Go plugins are a **development preview**.

[Browse the catalog](https://streamdockmanager.app/plugins) · [Build from source](#build-from-source) · [Developer guide](DEVELOPMENT.md)

## Install a plugin

1. In Manager, use **Plugins → Browse catalog** to find a plugin, or **Import plugin** if you already have the ZIP.
2. Review the permissions, install, and **enable** it. Only install packages you trust: plugins run native code.
3. Assign a plugin action to a supported button, dial, or touch control, then **Save binding**.
   Open **Plugin settings** to configure the action or connect its service if needed.

No Go or Node.js is needed to run these ZIPs. Current development packages require **Manager 0.1.1 or newer** in Manager. Check each catalog entry for downloads and compatible hosts.

| Host                | Package                                                  |
| ------------------- | -------------------------------------------------------- |
| Stream Dock Manager | OS-specific ZIP, or Manager universal ZIP when available |
| VSD Craft           | Matching OS-specific ZIP for a compatible plugin         |
| OpenDeck            | OpenDeck universal ZIP for a compatible plugin           |

System Monitor and Random Meme use Manager-only capabilities. Packages are unsigned. A successful build does not establish native host or device compatibility.

If a plugin offers **Suggested profiles & setup**, review its actions and conflicts before importing. Enabled plugins can stay idle until an action or settings panel needs them.

## Available plugins

| Use case                  | Plugins                                                                                                |
| ------------------------- | ------------------------------------------------------------------------------------------------------ |
| Focus and everyday tools  | Counter & Tally, Timers & Pomodoro, Randomizer & Dice                                                  |
| System and network status | System Monitor, Battery & Power, Network & VPN Status, Internet Speed Test, Service Monitor, Tailscale |
| Audio and streaming       | Spotify, VLC, Microphone Controls, OBS Studio                                                          |
| Automation                | Home Assistant, HTTP & Webhooks, MQTT, Make                                                            |
| Games                     | Steam Library, OSRS Hiscores, RuneScape Hiscores, RetroAchievements                                    |
| Information and sharing   | Weather, Screenshot Tools, Random Meme                                                                 |
| Manager examples          | [SDM Vault Example](internal/plugins/sdm-vault-example/README.md)                                      |

**Dice:** put **Roll All** and the dice on the same page. Roll count and total only track when assigned, and totals update after the animation. Hosts that only report visible actions use a manual reset instead of clearing when the last assignment is removed.

**Spotify:** suggested volume dial layouts rotate for volume and press for **Mute Toggle**. They do not change profiles you already imported. Spotify needs a supported playback backend; Linux MPRIS control uses `gdbus`.

## Permissions and connected services

Configure a plugin's account or service in Manager, not in source files or issue reports. Check the connected destination before using a webhook or another remote action.

| Feature                                          | Check first                                                     |
| ------------------------------------------------ | --------------------------------------------------------------- |
| Screen capture                                   | Supported desktop session and macOS Screen Recording permission |
| macOS input or link pasting                      | Accessibility permission                                        |
| Linux microphone control                         | PulseAudio-compatible `pactl`, including PipeWire compatibility |
| VLC or OBS Studio                                | VLC authenticated HTTP or OBS WebSocket v5                      |
| Home Assistant, MQTT, Make, and similar services | Endpoint, credentials, and permissions for the intended service |

### Use Manager Vault with official plugins

Link an entry to the action in Manager's package review or installed-plugin permissions, then select **Use Manager Vault** for that input in the action settings. Review direct sharing before granting it. Existing settings keep working until you switch an input; a locked Vault, missing entry, or revoked grant blocks managed use. Compatible VSD Craft and OpenDeck packages use their ordinary settings without Vault.

| Plugin                               | What Vault can supply                         |
| ------------------------------------ | --------------------------------------------- |
| Home Assistant                       | Bearer token                                  |
| Make                                 | Scenario API token or webhook URL             |
| MQTT                                 | Username and password, independently          |
| OBS Studio and VLC                   | Connection password                           |
| RetroAchievements                    | Username and API key                          |
| HTTP & Webhooks; Service Monitor     | Private URLs for supported HTTP actions       |
| OSRS Hiscores and RuneScape Hiscores | Player name                                   |
| Steam Library                        | Account ID for Playtime                       |
| Weather                              | Coordinates for Conditions, Forecast, and Sun |

For Weather, approve a Location entry and select **Use Manager Vault for location**. The plugin sends the coordinates to its configured HTTPS weather service. The free Open-Meteo service is for noncommercial use; use a suitable paid or self-hosted endpoint for commercial use. An explicitly disabled noncommercial-use setting prevents requests to the free endpoint. You can still enter coordinates manually or use Manager's **Find a location** picker, which also supports a one-time device position.

### Use inline Vault text in a result

On a Manager build with Vault text formatting, **Randomizer & Dice → List** and
**Draw without replacement**, and the [SDM Vault Example](internal/plugins/sdm-vault-example/README.md),
can show inline references. In the package review, link an unprotected **Text**
entry to each action that needs it. Then use **Copy reference** in Vault and paste
the reference into the list item or the example's **Display text** field.

The saved item and draw history keep the reference, not the resolved text. The
value appears on the dock when that result is rendered, so only link text you
want displayed. Plain items still work without a Vault entry. This does not
expand service URLs, credentials, selectors, or command arguments.

### Action failure notifications

Official plugins require permission to show action failures in Manager. Review this permission during installation. Messages omit credentials and raw service responses. Compatible VSD Craft and OpenDeck packages do not require Manager notifications.

## Troubleshooting

| Problem                         | What to check                                                                   |
| ------------------------------- | ------------------------------------------------------------------------------- |
| Package cannot install or start | Match the host, OS, CPU, and Manager version                                    |
| Action is missing               | Enable the plugin and choose a supported control                                |
| Service action is unavailable   | Check the required app, permissions, credentials, and endpoint                  |
| Failure still unclear           | Open plugin health or capture **Logs**, then review the contents before sharing |

## Build from source

Install Git, Go 1.27.4, Python 3.11+, and GNU Make. On Windows use MSYS2 Make with native Go and Python on `PATH`.

| What you're building       | SDK to use                                                                                                                                                                          |
| -------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Published source release   | The exact public dependency in `go.mod` and `go.sum`                                                                                                                                |
| Current development source | A [matching local SDK checkout](DEVELOPMENT.md#sdk-access-and-local-development). The public Go pin is still `v0.1.0`; current Vault and text-formatting code needs newer SDK APIs. |

For a released source checkout whose SDK pin supplies all required APIs:

```sh
git clone https://github.com/StreamDockManager/Plugins.git
cd Plugins
# Check out the published tag you intend to build.
make deps
make package PLUGIN=weather
```

Import the printed ZIP from `dist/packages/<os>/<arch>/`. Omit `PLUGIN=weather` to package all plugins supported by the current target. Native targets are Linux and Windows x64 and macOS Apple Silicon; Intel Mac is optional.

[Developer commands](DEVELOPMENT.md#developer-reference) · [Universal packages](DEVELOPMENT.md#optional-unified-packages) · [Release workflow](DEVELOPMENT.md#github-actions-prepare-once-publish-later)

## Local cleanup

Preview with `CLEAN_DRY_RUN=1 make clean`, then run `make clean` to remove generated outputs and local build caches. Stop active builds first and save reports you need from `artifacts/`. Source, global caches, installed app data, dependencies, and credentials are not cleaned. Pass custom `DATA_DIR` or `MANAGER_DATA_DIR` again if it overlaps a generated directory.

## Independence and license

This project is not affiliated with, endorsed by, or sponsored by VSDInside, MiraBox, OpenDeck, or Elgato Stream Deck. Product names identify compatibility only.

Official plugins are [GPL-3.0-or-later](LICENSE). Keep the license and [third-party notices](THIRD_PARTY_NOTICES.md) with redistributed packages.
