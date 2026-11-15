# Stream Dock Manager plugins

Add media controls, timers, system information, weather, and connected services to
[Stream Dock Manager](https://github.com/StreamDockManager/Manager). Each official plugin is a separate
package, so you can install only the ones you need.

**Unreleased development preview · 0.1.0 · Native Go plugins · GPL-3.0-or-later**

| Get started                                     | Learn more                                  |
| ----------------------------------------------- | ------------------------------------------- |
| [Install a plugin](#install-a-plugin)           | [Available plugins](#available-plugins)     |
| [Add your first action](#add-your-first-action) | [Troubleshooting](#troubleshooting)         |
| [Build from source](#build-from-source)         | [Developer reference](#developer-reference) |

## Independence and compatibility

This is an independent project and is not affiliated with, endorsed by, or sponsored by
VSDInside, MiraBox, OpenDeck, or Elgato Stream Deck. Product names are used to identify
products and describe compatibility; they do not imply an official relationship.

## Before you start

You need a working installation of Stream Dock Manager and a plugin ZIP that matches your operating
system and CPU. **You do not need Go or Node.js to run a packaged plugin.**

Browse the [plugin catalog](https://streamdockmanager.app/plugins) for descriptions and any offered downloads. This
project is not yet released; if a matching package is unavailable, [build it from source](#build-from-source).
Packages are unsigned, and features may vary by platform. Automated checks and cross-compilation do
not establish native desktop, hardware, or third-party host compatibility.

| Package                   | When to choose it                                                                          |
| ------------------------- | ------------------------------------------------------------------------------------------ |
| OS-specific ZIP           | The usual choice: a smaller download for your OS and CPU                                   |
| Unified ZIP, when offered | One download containing several OS/CPU executables                                         |
| OpenDeck universal ZIP    | For OpenDeck, not the normal Manager download; includes macOS, Windows, and Linux runtimes |

Manager prefers the OS-specific package. A unified package still needs an exact match for your OS and
CPU; it does not add support for an unsupported target. OpenDeck exports are universal only and may
require features the host does not provide. See [host compatibility](#optional-unified-packages) before
choosing one. A downloadable package is not a promise that every action works on every host.

## Install a plugin

1. Open **Plugins** in Manager.
2. Select **Browse catalog** to choose an offered package, or **Import plugin** to open a local ZIP.
3. Review the package and finish installation.
4. Enable the installed plugin. **Installation does not enable it automatically.**
5. Open **Plugin settings**, if offered, enter any required connection details, and apply them.

Plugins are native executables. Install only packages you trust. Manager starts an enabled plugin
when an action or an open settings panel needs it, so an idle plugin is not necessarily broken.

## Add your first action

1. Open your device in Manager and select a button, dial, or touch control.
2. Choose an action supplied by the installed plugin that supports that control type.
3. Fill in the action's settings, then choose **Save binding**.
4. Check the result on your device. For actions that change another service, review the target and
   settings before activating the control.

Some plugins also offer **Suggested profiles & setup**. Review the proposed placements and any
conflicts before importing a suggested profile.

## Available plugins

| Use case                  | Plugins                                                                                         |
| ------------------------- | ----------------------------------------------------------------------------------------------- |
| Focus and everyday tools  | Counter & Tally, Timers & Pomodoro, Randomizer & Dice                                           |
| System and network status | System Monitor, Battery & Power, Network & VPN, Internet Speed Test, Service Monitor, Tailscale |
| Audio and streaming       | Spotify, VLC, Microphone Controls, OBS Studio                                                   |
| Automation                | Home Assistant, HTTP & Webhooks, MQTT, Make                                                     |
| Games                     | Steam Library, OSRS Hiscores, RuneScape Hiscores, RetroAchievements                             |
| Information and sharing   | Weather, Screenshot Tools, Random Meme                                                          |

See the catalog for each plugin's actions and requirements. Developers can run `make catalog` to print
the definitions, IDs, capabilities, and supported platforms as JSON.

## Permissions and connected services

Some actions need an installed application, an OS permission, or an account on another service.

| Feature                                        | What may be needed                                                           |
| ---------------------------------------------- | ---------------------------------------------------------------------------- |
| macOS input or link pasting                    | Accessibility permission                                                     |
| Screen capture                                 | Screen Recording permission on macOS and a supported desktop session         |
| Linux microphone controls                      | PulseAudio-compatible `pactl`, including PipeWire's PulseAudio compatibility |
| Spotify                                        | Spotify desktop; `gdbus` for Linux MPRIS control                             |
| VLC                                            | VLC's authenticated HTTP interface                                           |
| OBS Studio                                     | OBS WebSocket v5 connection details                                          |
| Home Assistant, MQTT, Make, and other services | The configured endpoint, credentials, and appropriate service permissions    |

Enter credentials in the plugin's settings, not in source files or issue reports. Network-backed
plugins contact the configured service or their documented provider. Check the destination before
using a webhook or another action that changes remote state.

## Troubleshooting

| What you see                             | What to check                                                                   |
| ---------------------------------------- | ------------------------------------------------------------------------------- |
| A package cannot be installed or started | Confirm its OS/CPU, Manager compatibility, and the package review details       |
| No action appears for a control          | Enable the plugin and choose a control type supported by that action            |
| The plugin is enabled but idle           | Open its settings or a page with an assigned action; execution starts on demand |
| A service or OS action is unavailable    | Check the required application, permission, credentials, and endpoint           |
| An action fails after setup              | Open the plugin's health view and capture logs explicitly from **Logs**         |

Missing services or permissions must appear as unavailable/error states, not simulated successes.
Review captured logs before sharing them, especially URLs, service names, and other personal details.

## Build from source

These steps run entirely in this repository and use the SDK revision pinned in [go.mod](go.mod) and
[go.sum](go.sum).

| Requirement  | Notes                                                     |
| ------------ | --------------------------------------------------------- |
| Git          | Source checkout and optional private-fork access          |
| Go 1.27.4    | Version declared by this module                           |
| Python 3.11+ | Build wrapper and packaging helpers                       |
| GNU Make     | On Windows, use MSYS2 with native Go and Python on `PATH` |

```sh
git clone https://github.com/StreamDockManager/Plugins.git
cd Plugins
make deps
make package PLUGIN=weather
```

This builds Weather and writes a ZIP for the current host. Import the resulting ZIP in Manager using
**Plugins → Import plugin**, then follow the installation and first-action steps above. Omit
`PLUGIN=weather` to package every plugin supported on that platform.

| Package target      | Output folder                  |
| ------------------- | ------------------------------ |
| Linux x64           | `dist/packages/linux/amd64/`   |
| Windows x64         | `dist/packages/windows/amd64/` |
| macOS Apple Silicon | `dist/packages/darwin/arm64/`  |
| macOS Intel         | `dist/packages/darwin/amd64/`  |

Linux and Windows packages currently target x64. If SDK downloads fail, check network access, the pinned SDK revision, and any custom
[SDK source settings](#sdk-access-and-local-development) before retrying.

## Developer reference

Build and test locally before publishing. None of the build commands below installs a plugin or
publishes a release. The [release workflow](#github-releases-separate-from-the-cdn) is a separate,
explicit operation.

| Command                       | Purpose                                               |
| ----------------------------- | ----------------------------------------------------- |
| `make help`                   | Show build targets and configuration options          |
| `make deps`                   | Download pinned Go dependencies                       |
| `make catalog`                | Print plugin definitions as JSON                      |
| `make build PLUGIN=weather`   | Compile one plugin for the current host               |
| `make package PLUGIN=weather` | Compile and package one plugin for the current host   |
| `make cross`                  | Compile Linux/Windows x64 and macOS ARM64 executables |
| `make assets`                 | Stage property-inspector assets under `dist/ui/`      |
| `make test`                   | Run Go tests                                          |
| `make check`                  | Run `go vet` and compile all Go packages              |

Use `WORKSPACE_JOBS` to limit compile and package worker concurrency. Package workers
write separate plugin outputs and publish catalogs in deterministic source order only
after all workers succeed. The standalone `cross` command still visits platform targets
sequentially; its per-target worker pool obeys the same limit. `PLUGIN_MACOS_INTEL=1` adds Intel macOS to
cross-builds. For explicit package targets, see `go run ./tools/pluginbuild package --help`.
Building or packaging does not install or enable a plugin.

Public build defaults live in [workspace.toml](workspace.toml). `WORKSPACE_CONFIG` selects another
configuration file; `RELEASE_REPOSITORY` and `STREAM_DOCK_MANAGER_PLUGINS_CATALOG_URL` override the
corresponding public defaults.

### SDK access and local development

<details>
<summary>Configure SDK access or work on a local SDK checkout</summary>

The public SDK resolves over HTTPS using Go's normal proxy and checksum database behavior.
No GitHub token, App, SSH key, or special Go private-module setting is required. Existing user
proxy, checksum, and private-module settings are preserved rather than overwritten.

If you deliberately use a private mirror, set `STREAM_DOCK_MANAGER_SDK_REPOSITORY_URL` to its
credential-free HTTPS or SSH URL. That explicit override enables process-local private resolution
and Git URL routing for this SDK only, using your existing Git authentication. It does not change
global Git/Go settings. Do not put credentials in the URL or committed configuration.

For local SDK development, run this from the Plugins checkout:

```sh
go work init . /absolute/path/to/Plugin-SDK/Go
go work edit -replace=github.com/StreamDockManager/Plugin-SDK/Go@v0.1.0=/absolute/path/to/Plugin-SDK/Go
make test
```

Before the public `Go/v0.1.0` tag exists, ordinary builds cannot fetch the SDK. The version-specific
workspace replacement lets you build against your local SDK in the meantime. Once the tag is
published, use the pinned SDK and its verified checksums for ordinary builds.

If a Go workspace already exists, add the SDK with `go work use` instead. Keep local workspace files
and `replace` directives out of commits; ordinary builds must continue using the pinned SDK.

</details>

### Optional unified packages

<details>
<summary>Build, combine, and validate multi-platform plugin ZIPs</summary>

A unified ZIP contains one plugin with separate executables for Windows amd64, Linux amd64, and
macOS arm64. Intel macOS is optional. Manager selects the exact OS/CPU entry from `CodePaths`.

After downloading dependencies, build all platform packages from the same source, then combine them:

```sh
go run ./tools/pluginbuild package --os windows
go run ./tools/pluginbuild package --os linux
go run ./tools/pluginbuild package --os darwin
make unified
python3 scripts/unified_packages.py --host all --validate
```

`make unified` builds both Manager and OpenDeck universal ZIPs by default from the same validated inputs.
Set `PLUGIN_MACOS_INTEL=1` consistently for packaging and combining to include Intel macOS. Manager output is
written to `dist/packages/unified/`, with a separate `dist/optional/catalog-unified.json` inventory.
Plugins without all three operating systems are skipped rather than advertised as cross-platform.

To select only OpenDeck/OpenAction output, use those same verified three-platform inputs
(`--host manager` selects only Manager output):

```sh
python3 scripts/build.py unified --host opendeck
python3 scripts/unified_packages.py --host opendeck --validate
# Equivalent Make selection: make unified PLUGIN_HOST=opendeck
```

OpenDeck output is **universal only**: `dist/packages/opendeck/<id>.zip`, with its
own `dist/optional/catalog-opendeck.json`. No system-specific OpenDeck packages
are generated or offered as Manager platform downloads. `--host opendeck` is rejected for
native build/package/cross actions; build their ordinary inputs first. The Go
`tools/pluginbuild` remains the existing Manager/VSD platform-input builder.
The exporter keeps exact `CodePaths` and legacy OS fallbacks, uses standard ZIP
Deflate, and converts manifest image references to OpenAction stems, preserving
ambiguous PNG/SVG choices under verified aliases. Runtime image paths are not
rewritten. The exporter is self-contained in this repository.

This is package-format support, not a claim that every official plugin works on
OpenDeck: compiled capability requirements, Manager metadata, external service
requirements and permissions remain intact. Missing Manager-only features are
not supplied or bypassed by export. Native OpenDeck testing on each target is
still required; publishing remains separate.

The merger verifies source fingerprints, versions, architectures, compiled capabilities, shared
assets, and per-file digests. Dependency licenses stay in `licenses/<os>-<arch>/`. Archives have
deterministic member order, timestamps, and permissions. Publishing is a separate operation, and a
successful merge does not replace native testing on each target.

</details>

### Where to make changes

| Location                   | What belongs here                                                  |
| -------------------------- | ------------------------------------------------------------------ |
| `internal/plugins/<slug>/` | A plugin's actions, settings, metadata, and version                |
| `internal/catalog/`        | Catalog aggregation, inspector fields, profiles, and shared assets |
| `inspectors/`              | Property-inspector source                                          |
| `internal/engine/`         | Product adapters around the standalone SDK                         |
| `tools/pluginbuild/`       | Build, packaging, and local catalog tooling                        |
| `catalog/metadata.json`    | Product links and catalog descriptions                             |

Edit source files, not staged files in `dist/`. Reusable SDK behavior belongs in
[Plugin-SDK](https://github.com/StreamDockManager/Plugin-SDK). Keep official
plugins at `0.1.0` during this unreleased development period.

### Runtime acceptance snapshots

On macOS, compare two explicit, compatible source revisions with the current test harness:

```sh
python3 scripts/runtime_acceptance.py prepare --before <baseline-commit> --after <comparison-commit> --output /tmp/plugin-acceptance
python3 scripts/runtime_acceptance.py measure --output /tmp/plugin-acceptance
```

Both revisions must contain the maintained explicit-factory microphone adapter. Preparation
validates their layout before compiling isolated snapshots. Measurement requires the current
preparation metadata and verifies the shared harness, source files, and binaries; historical
reports must be prepared again with compatible tooling. Existing measurement reports are never
overwritten. This controlled workload is not native audio/device or Windows/Linux qualification.

### Packaging, caches, and reproducibility

All project-generated plugin ZIP members use standard ZIP DEFLATE (method 8),
explicit level 6, for Manager/VSD and OpenDeck exports. Executable permissions,
CRC checks, bounded extraction and deterministic ordering are unchanged. Changing
the compression policy invalidates platform archive caches; old LZMA packages
must be rebuilt before first-party catalog validation.

Universal package builds cache final ZIP compression results using the selected
source definitions, verified platform inventories, exporter/validator implementation,
host and compression runtime as inputs. Cache hits still validate platform packages
and compare output member bytes and executable modes with the assembled inputs;
cache metadata never authorizes different plugin content. Corrupt or stale caches
rebuild atomically. This saves recompression, not all archive reads. In-process
validated inventories are immutable snapshots, and subsequent reads must match
their content hashes before expensive descriptor/architecture checks can be reused.

Binary cache keys follow each plugin's actual target-selected Go dependency closure,
including imported local SDK/replacement sources and embedded resources, instead
of invalidating every plugin for an unrelated internal implementation edit. Compiler
settings, build tags, module/workspace resolution, capability descriptors and Go's
embedded VCS revision/dirty state still participate. Consequently, a clean-to-dirty
transition or a new commit can legitimately invalidate every binary. Standard
library inputs use the selected Go toolchain identity; the reviewed Windows
runtime patch keeps its separate content digest. Mutating a custom toolchain in
place without changing its identity is not supported.

Compilation and packaging share invocation-local dependency analysis and rendered
license notices. Before reuse, selected source contents, source-directory membership,
workspace/module inputs and license contents are checked again; added embedded
files or newly imported packages cannot silently reuse the old license inventory.
Build from a stable source tree: concurrent edits during compilation are not an
atomic snapshot. Custom `GOFLAGS=-overlay=...` release builds are rejected explicitly
because original source paths would otherwise conceal overlay content from cache
keys; the builder's reviewed internal Windows overlay remains supported.

### GitHub Releases (separate from the CDN)

Prepare a release from the exact clean, committed checkout. This builds **all**
plugins for their supported operating systems, plus Manager and OpenDeck universal
packages where all three platforms are supported. It stages `catalog.json`, ZIP
assets and a local digest manifest; preparation makes no GitHub requests.

```sh
python3 scripts/github_release.py prepare --build \
  --commit "$(git rev-parse HEAD)" --tag plugins-stable-20261113-1
# Review dist/github-release/plugins-stable-20261113-1 before explicitly publishing:
python3 scripts/github_release.py publish --tag plugins-stable-20261113-1 \
  --repository StreamDockManager/Plugins
```

Use `--macos-intel` to add Intel macOS. Development releases require
`--channel development` and a `plugins-development-*` tag. `--jobs` bounds build
workers; `PLUGIN` filtering is ignored because release catalogs include the entire
repository. The optional `--directory` preparation input must contain validated
platform builds; `--build` always uses this checkout's `dist`.

Publication requires an authenticated GitHub CLI with repository release access.
The publisher binds the tag to the exact source commit, creates or resumes only
its matching draft, uploads missing assets without `--clobber`, downloads and
hash-verifies every asset, and publishes only after the complete inventory passes.
Existing tags are never moved, and existing assets or published releases are never
overwritten. An identical completed retry only verifies; a conflicting tag,
release or asset fails for manual review. Stable releases are marked latest;
development releases are prereleases and never marked latest. GitHub administrators
can still change remote releases outside this tool.

### GitHub Actions: prepare once, publish later

1. On the **main** branch, run **Prepare Release** (`prepare-release.yml`). Choose
   stable or development and whether to include Intel macOS. No commit SHA or tag
   needs to be copied: the run checks out its exact triggering commit and uses
   `plugins-<channel>-<run-id>` as the release tag.
2. Preparation downloads and verifies pinned dependencies, checks Go formatting and
   vulnerabilities, runs Go vet/tests and
   Python release tests, validates versions, then builds all supported platform
   ZIPs and Manager/OpenDeck universal ZIPs once. The successful run retains
   `prepared-plugin-release` for **14 days**. Its summary shows the run ID.
3. Review that run and artifact. Run **Publish Release** (`publish-release.yml`)
   on **main**, entering its `prepare_run_id`. Publication downloads those exact
   bytes and does not compile, retest, or repackage them.

Publication rejects failed, unfinished, foreign-repository, non-main, or wrong-workflow
runs; expired/ambiguous artifacts; changed run attempts; missing artifact digests;
and mismatched source identity, versions, tags, file inventory, or checksums. An
expired artifact requires a new preparation, not a silent rebuild during publish.
The previous combined prepare/publish dispatcher is replaced by these two workflows.
Existing CDN commands remain separate. Cross-compilation is not native host qualification.

### CI prerequisites and permissions

The SDK repository must be public before running plugin builds. Pinned SDK versions and
checksums are retained; CI downloads them through normal Go module resolution. No dedicated
SDK-reader App, token, or secret is needed. The workflows do not disable checksum verification
or alter global Git/Go configuration.

Prepare Release uses repository Contents read and has a 55-minute job limit. Publish Release
uses the repository-scoped `GITHUB_TOKEN` with Actions read and Contents write to verify and
publish the prepared bundle. It does not access or build the SDK. Website OAuth configuration
is independent of these build workflows. These files do not change repository visibility.

For CORS-safe website reads, publication also writes the exact `catalog.json`
bytes to a Git blob retained by the separate immutable `catalog-<release-tag>`
metadata tag. It never changes the source release tag or a branch. Release notes
record the blob SHA and catalog SHA-256; publication requires GitHub's catalog
asset SHA-256 digest to agree. Existing metadata tags must contain the identical
catalog bytes or publication fails without retagging. The website verifies the
API representation against the release asset digest before displaying it.

## License

[GPL-3.0-or-later](LICENSE). See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for bundled assets and
dependencies, which retain their own licenses.

### Pinning a published SDK release

The Go module is `github.com/StreamDockManager/Plugin-SDK/Go`. Because it lives in the
SDK repository's `Go/` subdirectory, module version `vX.Y.Z` is published with
repository tag `Go/vX.Y.Z`, not a root `vX.Y.Z` tag.

After that release is public:

```sh
make sdk-pin-update SDK_VERSION=vX.Y.Z
make sdk-pin-check SDK_VERSION=vX.Y.Z
make check
make test
```

Replace `vX.Y.Z` with an actual published version. The updater accepts exact
release versions, including prereleases, not `main`, `latest`, pseudo-versions or
versions requiring a different module-major path. It uses ordinary Go module
resolution and checksum verification, honoring the existing explicit SDK source
policy. It disables workspace overrides and rejects an SDK `replace` directive.
It resolves in temporary module files, verifies unrelated requirements and sums
are retained, then replaces `go.mod` and `go.sum`. Ordinary failures roll back;
the two-file replacement is not crash-atomic, so review both files after an
interrupted update. Review and commit the resulting diff together, then run the
normal project checks before preparing packages.

`make sdk-pin-check` verifies the exact release and its recorded checksums without
rewriting tracked files. Prepare Release requires this strict check. When migrating
to a new SDK module path, wait for the first tag to resolve under that path, then
run the update to record Go-generated checksums before publishing changes to this
repository. This tooling does not create tags or fabricate checksums.
