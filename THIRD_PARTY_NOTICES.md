# Dependency notices

Go runtime and standard library: BSD-style license, https://go.dev/LICENSE.
Coder WebSocket v1.8.15: ISC, https://github.com/coder/websocket/blob/v1.8.15/LICENSE.txt.
Gorilla WebSocket v1.5.3 (transitive dependency of Paho MQTT and speedtest-go): BSD-2-Clause, https://github.com/gorilla/websocket/blob/v1.5.3/LICENSE.
Ebitengine purego v0.11.3: Apache-2.0, https://github.com/ebitengine/purego/blob/v0.11.3/LICENSE.
gopsutil v4.26.10 and its transitive dependencies: BSD-3-Clause and their respective licenses; `go.sum` pins resolved dependencies. See https://github.com/shirou/gopsutil/blob/v4.26.10/LICENSE.
Weather data: Open-Meteo, CC BY 4.0 (https://open-meteo.com/), free endpoint restricted to noncommercial usage under https://open-meteo.com/en/terms. Paid/self-hosted endpoint supported.
Spotify is a third-party application and trademark. No Spotify Web API credentials are used or distributed.

Eclipse Paho MQTT v1.5.1: EPL-2.0 / Eclipse Distribution License 1.0 (dual licensed), https://github.com/eclipse-paho/paho.mqtt.golang/blob/v1.5.1/LICENSE.
golang.org/x/net, x/sync and x/sys: BSD-3-Clause; exact versions are recorded in go.mod/go.sum and their licenses are included only in packages using them.

OBS Studio, Home Assistant, GitHub, Syncthing, VLC, Tailscale, MPD and Make are third-party products/services. Hiscores data is supplied by Jagex; RetroAchievements data is supplied by RetroAchievements. Their clients, credentials and service access are not bundled. Package/action glyphs are original code-native SVG geometry.

RuneScape and OSRS skill and cape artwork © Jagex Limited is supplied through the RuneScape and OSRS Wikis. Image provenance and hashes are recorded in `internal/catalog/artwork/hiscores/sources.json` and packaged as `hiscores/artwork-sources.txt`. This artwork is separately owned and is not relicensed under the project's GPL license.

Created using intellectual property belonging to Jagex Limited under the terms of [Jagex's Fan Content Policy](https://legal.jagex.com/docs/policies/fan-content-policy). This content is not endorsed by or affiliated with Jagex.

Each package’s THIRD_PARTY_LICENSES.txt contains licenses and required NOTICE files from its target-specific runtime dependency closure. The generated package notice lists data/service attributions for that plugin. This source document also lists development and packaging dependencies.
