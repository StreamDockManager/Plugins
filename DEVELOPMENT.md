# Develop and release plugins

[Back to plugin installation](README.md)

Start with one local package. If you are on the development checkout, [select a matching SDK](#sdk-access-and-local-development) before running `make test` or `make package PLUGIN=weather`. Builds do not install plugins, run actions, or publish anything.

## Developer reference

| Command                        | What it does                                            |
| ------------------------------ | ------------------------------------------------------- |
| `make deps`                    | Download the pinned Go modules                          |
| `make package PLUGIN=weather`  | Build and ZIP Weather for your host                     |
| `make build PLUGIN=weather`    | Compile Weather without packaging                       |
| `make test` / `make check`     | Run Go tests / vet and compile                          |
| `make catalog` / `make assets` | Print catalog metadata / stage inspectors in `dist/ui/` |
| `make cross`                   | Compile Linux and Windows x64 and macOS ARM64           |
| `make help`                    | List build options                                      |

Install the ZIP in Manager via **Plugins → Import plugin** to try it. Set `WORKSPACE_JOBS` to limit worker concurrency and `PLUGIN_MACOS_INTEL=1` to include optional macOS Intel builds. For explicit targets run `go run ./tools/pluginbuild package --help`.

Use [workspace.toml](workspace.toml) for defaults. Set `WORKSPACE_CONFIG` to use a different file; `RELEASE_REPOSITORY` and `STREAM_DOCK_MANAGER_PLUGINS_CATALOG_URL` override the corresponding defaults.

### SDK access and local development

Public release builds use the exact module in `go.mod` and `go.sum`. No GitHub token is needed for that public module. The current development source uses Vault, permission, notification, and text-formatting APIs newer than its public Go SDK pin (`v0.1.0`). Downloading that pin alone will not make these sources compile.

For development, obtain the matching SDK source with your existing repository access. From the **Plugins root**, point to that checkout's **Go/** directory and use a temporary Go workspace:

```sh
sdk_go=/absolute/path/to/Plugin-SDK/Go
plugins_dir="$(pwd -P)"
workdir="$(mktemp -d)"
(cd "$workdir" && GOWORK=off go work init "$plugins_dir" "$sdk_go")
export GOWORK="$workdir/go.work"
go work edit -replace "github.com/StreamDockManager/Plugin-SDK/Go@v0.1.0=$sdk_go"
go list -m -f '{{.Dir}}' github.com/StreamDockManager/Plugin-SDK/Go
make test
make package PLUGIN=weather
```

The listed directory should be your local SDK's `Go/` directory. In MSYS2, use `cygpath -am` for absolute paths passed to native Go, including the exported `GOWORK` path. If the pin changes, use the version in `go.mod` in the workspace replacement. This does not change either repository's dependency files. Run `unset GOWORK` to stop using the temporary workspace; run `GOWORK=off` when verifying a published pin.

If you intentionally use a private mirror, set `STREAM_DOCK_MANAGER_SDK_REPOSITORY_URL` to its credential-free HTTPS or SSH URL. This opts in to process-local routing for the SDK alone. It uses existing Git authentication and does not change global settings.

### Build and test Vault support

Vault and notification integration require their matching SDK APIs and a compatible Manager build. Run `go test ./...` with the intended SDK, then package and test a binding with saved settings, an approved Vault entry, and revoked access. Portable hosts use ordinary settings when Manager extensions are unavailable. See the [Vault example](internal/plugins/sdm-vault-example/README.md).

### Optional unified packages

Build all supported platform inputs from the same source revision, then combine and validate them:

```sh
go run ./tools/pluginbuild package --os windows
go run ./tools/pluginbuild package --os linux
go run ./tools/pluginbuild package --os darwin
make unified
python3 scripts/unified_packages.py --host all --validate
```

| Output                  | Location                  | Use                                                       |
| ----------------------- | ------------------------- | --------------------------------------------------------- |
| Manager unified ZIPs    | `dist/packages/unified/`  | Manager selects the exact OS/CPU runtime from `CodePaths` |
| OpenDeck universal ZIPs | `dist/packages/opendeck/` | One OpenAction package for macOS, Windows, and Linux      |
| Inventories             | `dist/optional/`          | Separate catalogs for the two host families               |

Set `PLUGIN_MACOS_INTEL=1` consistently when including Intel input builds and merging. Use `make unified PLUGIN_HOST=opendeck` for OpenDeck only. OpenDeck archives are universal only. The merger skips plugins without all three OS builds; System Monitor and Random Meme require Manager-only capabilities and are not exported to OpenDeck.

Packaging verifies file hashes, platform support, architectures, and source fingerprints. Keep bundled dependency notices. A valid archive is not proof of native host or hardware behavior.

### Where to make changes

| Path                       | Owns                                                        |
| -------------------------- | ----------------------------------------------------------- |
| `internal/plugins/<slug>/` | Actions, settings, metadata, and individual plugin versions |
| `internal/catalog/`        | Shared catalog metadata, inspector fields, and profiles     |
| `inspectors/`              | Property-inspector source                                   |
| `internal/engine/`         | Plugin adapters around the standalone SDK                   |
| `tools/pluginbuild/`       | Go build, package, and catalog tooling                      |
| `catalog/metadata.json`    | Catalog descriptions and product links                      |

Edit source, not staged `dist/` files. Put reusable SDK behavior in [Plugin-SDK](https://github.com/StreamDockManager/Plugin-SDK).

### Runtime acceptance snapshots

```sh
python3 scripts/runtime_acceptance.py prepare --before <baseline-commit> --after <comparison-commit> --output /tmp/plugin-acceptance
python3 scripts/runtime_acceptance.py measure --output /tmp/plugin-acceptance
```

Use this macOS harness to compare explicit compatible source revisions with the maintained microphone adapter. The harness verifies source and binary identities and does not overwrite reports. It does not qualify audio devices or other operating systems.

### Packaging, caches, and reproducibility

Use stable source files while building: concurrent edits are not an atomic snapshot. Packages use ZIP DEFLATE level 6. Rebuild older LZMA packages instead of submitting them to current catalog validation. Cache hits still verify input and output identity; custom `GOFLAGS=-overlay=...` release builds are not supported. Preview local cleanup with `CLEAN_DRY_RUN=1 make clean`.

### GitHub Releases (separate from the CDN)

Make release builds from a clean, committed source tree using a verified public SDK pin. GitHub and CDN publication are separate operations.

```sh
python3 scripts/github_release.py prepare --build \
  --commit "$(git rev-parse HEAD)" --tag plugins-stable-example
```

Review the ZIPs, `catalog.json`, and digests in `dist/github-release/plugins-stable-example/`. To publish a reviewed bundle deliberately, authenticate `gh` with release access for the canonical repository:

```sh
python3 scripts/github_release.py publish --tag plugins-stable-example \
  --repository StreamDockManager/Plugins
```

Use `--channel development` and a `plugins-development-*` tag for a prerelease. Release builds include all supported plugins and do not honor single-plugin filtering. Existing tags, published releases, and uploaded assets are never overwritten. Conflicts require manual review. The publisher checks asset bytes and creates an immutable browser mirror of `catalog.json`; the website verifies the mirror's digest.

### Check the development SDK before release

In the private Plugins repository, run **Development plugin checks** on `main` and enter the
full development SDK commit SHA. This checks plugins against that exact SDK source
without changing the public SDK pin.

Set the `SDK_DEV_READ_TOKEN` Actions secret to a credential with read-only Contents access to
the private SDK repository only. This workflow does not publish a release.

### GitHub Actions: prepare once, publish later

1. On **main**, run **Prepare Release** and choose the channel and optional Intel macOS target.
2. Review its successful run and `prepared-plugin-release` artifact, retained for 14 days.
3. Run **Publish Release** on **main** with that workflow's `prepare_run_id`. It publishes the already-verified bytes without rebuilding.

Publish rejects unfinished or wrong-source runs, expired artifacts, changed run attempts, and digest or inventory mismatches. Reprepare an expired artifact. Stable releases are marked latest; development releases are prereleases.

### CI prerequisites and permissions

**Development plugin checks**, **Prepare Release**, and **Publish Release** offer a
`runner_service` choice. Keep **github** for the existing runner defaults or choose
**blacksmith** after the repository is enabled for Blacksmith. Development checks use a
matching OS runner; release preparation and publication run on Linux, including when preparing
optional Intel macOS packages. The physical self-hosted native validation workflow is unchanged.

For **Development plugin checks**, select **all**, **linux**, **macos**, or **windows** and
provide the exact development SDK commit. **quick** (default) runs source checks and native tests.
**full** also builds and validates packages for each selected host. macOS checks run on ARM64; optional Intel macOS packaging
remains a separate release option. The development job needs read-only checkout access to that
SDK and does not change the public dependency pin or publish packages.

The **native Plugins validation** workflow on `test-build` has the same target selector.
Manual runs default to **quick**; choose **full** for native compiler and package evidence.
Pushes to `test-build` still run **full** across all three hosts. This supersedes the duplicate
Linux baseline workflow on `test-build`; cross-compilation remains part of the separate release
checks and packaging workflows. The release checks run the native Go tests; the package job
compiles and verifies each supported target once, including optional Intel macOS.

Prepare reads repository contents and requires a public, pinned SDK; it does not need an SDK-reader secret. Publish uses repository-scoped `GITHUB_TOKEN` with Actions read and Contents write. It never builds the SDK. These workflows do not change repository visibility.

### Pinning a published SDK release

The Go dependency lives in `Go/` in the SDK repository, so tag `Go/vX.Y.Z` resolves as module version `vX.Y.Z`.

```sh
make sdk-pin-update SDK_VERSION=vX.Y.Z
make sdk-pin-check SDK_VERSION=vX.Y.Z
GOWORK=off make check
GOWORK=off make test
```

Use an actual published version, not a branch or pseudoversion. Review `go.mod` and `go.sum` together; the check verifies the pin and hashes without rewriting them. The update command does not create SDK tags.

## License

[GPL-3.0-or-later](LICENSE). Keep the [third-party notices](THIRD_PARTY_NOTICES.md) with redistributed packages.
