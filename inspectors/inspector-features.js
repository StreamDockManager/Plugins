// Feature-local state and UI behavior. Transport generations, scope and settings
// ownership remain with the inspector shell and SDK DiscoveryController
const { DiscoveryController } = window.StreamDockManagerWorkflows;

// Create microphone discovery and draft selection controls
export function createMicrophoneFeature(shell) {
    let microphones = [],
        microphoneDefault = '',
        microphoneLoaded = false;
    const microphoneDiscovery = new DiscoveryController({
        // Send microphone requests through the shell-owned transport
        send: (payload) => shell.inspector.send(payload),
        cancelOp: 'cancel_microphones',
        // Refresh microphone controls and explain discovery failures
        changed(phase) {
            renderMicrophones();
            if (phase === 'timeout')
                microphoneMessage(
                    'Microphone detection timed out. Check your audio service and refresh.',
                );
            if (phase === 'error')
                microphoneMessage('Microphone detection is unavailable. Refresh to retry.');
        },
    });
    // Show or hide microphone discovery feedback
    function microphoneMessage(message) {
        const node = document.querySelector('#microphone-status');
        node.textContent = message;
        node.hidden = !message;
    }
    // Render available microphones without losing an undetected saved selection
    function renderMicrophones() {
        const enabled = shell.module?.microphones === true && !shell.globalScope;
        document.querySelector('#microphone-picker').hidden = !enabled;
        if (!enabled) return;
        const select = document.querySelector('#microphone-device');
        const selected = shell.settings.device || 'default';
        const currentDefault = microphones.find((device) => device.id === microphoneDefault);
        select.replaceChildren(
            new Option(
                currentDefault
                    ? `System default — ${currentDefault.name}`
                    : 'System default microphone',
                'default',
            ),
        );
        if (selected !== 'default' && !microphones.some((device) => device.id === selected))
            select.add(
                new Option(
                    microphoneLoaded ? 'Saved microphone (not detected)' : 'Saved microphone',
                    selected,
                ),
            );
        const counts = new Map();
        for (const device of microphones)
            counts.set(device.name, (counts.get(device.name) || 0) + 1);
        const indexes = new Map();
        for (const device of microphones) {
            const index = (indexes.get(device.name) || 0) + 1;
            indexes.set(device.name, index);
            select.add(
                new Option(
                    counts.get(device.name) > 1 ? `${device.name} (${index})` : device.name,
                    device.id,
                ),
            );
        }
        select.value = selected;
        document.querySelector('#microphone-refresh').disabled = microphoneDiscovery.busy;
        document.querySelector('#microphone-cancel').hidden = !microphoneDiscovery.busy;
    }
    // Cancel microphone discovery and restore idle controls
    function cancelMicrophones() {
        const pending = microphoneDiscovery.busy;
        microphoneDiscovery.cancel();
        if (pending) microphoneMessage('');
        renderMicrophones();
    }
    // Request microphones only when the action and runtime permit discovery
    function requestMicrophones() {
        if (!shell.module?.microphones || shell.globalScope || microphoneDiscovery.busy) return;
        const runtime = shell.inspector.state().host.streamDockManager;
        if (runtime?.enabled === false && !runtime.setup_runtime) {
            microphoneMessage(
                'Enable Microphone Controls in Plugins, then refresh microphones. Your saved selection is kept.',
            );
            return;
        }
        microphoneMessage('Detecting microphones…');
        microphoneDiscovery.start({ op: 'microphones', scope: 'action' });
    }
    // Accept current discovery replies and retain only usable microphone records
    function receiveMicrophones(payload) {
        if (
            !microphoneDiscovery.accepts(payload) ||
            !shell.module?.microphones ||
            shell.globalScope
        )
            return;
        microphoneDiscovery.receive(payload);
        if (payload.available && Array.isArray(payload.devices)) {
            microphones = payload.devices.filter(
                (device) =>
                    device &&
                    typeof device.id === 'string' &&
                    device.id &&
                    device.id !== 'default' &&
                    typeof device.name === 'string',
            );
            microphoneDefault = payload.default_id;
            microphoneLoaded = true;
        }
        renderMicrophones();
        microphoneMessage(
            !payload.available
                ? String(payload.message || 'Could not detect microphones. Refresh to retry.')
                : microphones.length
                  ? `${microphones.length} microphone${microphones.length === 1 ? '' : 's'} found. Choose one, then apply settings and save the binding.`
                  : 'No microphones found. Connect a microphone, then refresh.',
        );
    }
    // Update the microphone draft without saving the binding
    document.querySelector('#microphone-device').onchange = (event) => {
        shell.settings.device = event.target.value;
    };
    document.querySelector('#microphone-refresh').onclick = requestMicrophones;
    // Cancel discovery and acknowledge the user request
    document.querySelector('#microphone-cancel').onclick = () => {
        cancelMicrophones();
        microphoneMessage('Microphone detection cancelled.');
    };

    return {
        discovery: microphoneDiscovery,
        render: renderMicrophones,
        message: microphoneMessage,
        cancel: cancelMicrophones,
        request: requestMicrophones,
        receive: receiveMicrophones,
        // Collect the dedicated microphone picker into action settings
        collect(next) {
            if (shell.module?.microphones && !shell.globalScope)
                next.device = document.querySelector('#microphone-device').value;
        },
        // Clear microphone discovery state when the inspector context changes
        reset() {
            microphoneDiscovery.cancel();
            microphoneLoaded = false;
            microphones = [];
            microphoneDefault = '';
            microphoneMessage('');
        },
    };
}

// Create speed-test server discovery and selection controls
export function createSpeedFeature(shell) {
    let speedServers = [];
    const speedDiscovery = new DiscoveryController({
        // Send server discovery through the shell-owned transport
        send: (payload) => shell.inspector.send(payload),
        cancelOp: 'cancel_speed_servers',
        timeout: 25000,
        // Refresh server controls and explain discovery failures
        changed(phase) {
            renderSpeedServers();
            if (phase === 'timeout')
                speedServerMessage('Server discovery timed out. Check the connection and retry.');
            if (phase === 'error')
                speedServerMessage('Server discovery is unavailable. Retry shortly.');
        },
    });
    // Show or hide speed-server discovery feedback
    function speedServerMessage(message) {
        const node = document.querySelector('#speed-server-status');
        node.textContent = message;
        node.hidden = !message;
    }
    // Render server choices only in plugin-wide settings
    function renderSpeedServers() {
        const enabled = shell.module?.speedtest === true && shell.globalScope;
        document.querySelector('#speed-server-settings').hidden = !enabled;
        document.querySelector('#speed-server-help').hidden =
            !shell.module?.speedtest || shell.globalScope;
        if (!enabled) return;
        const mode = document.querySelector('#speed-server-mode');
        mode.value = shell.settings.server_mode || 'auto';
        document.querySelector('#speed-server-choice').hidden = mode.value !== 'manual';
        const select = document.querySelector('#speed-server');
        const selected = shell.settings.server || '';
        select.replaceChildren(new Option('Load servers, then choose one', ''));
        select.options[0].disabled = true;
        if (selected && !speedServers.some((server) => server.id === selected))
            select.add(new Option(`Stored server ${selected}`, selected));
        for (const server of speedServers) {
            const ping = Number.isFinite(server.latency_ms)
                ? `${server.latency_ms.toFixed(1)} ms`
                : 'ping unavailable';
            select.add(new Option(`${server.name} · ${ping} · ${server.id}`, server.id));
        }
        select.value = selected;
        for (const id of ['speed-load-servers', 'speed-detect-server'])
            document.getElementById(id).disabled = speedDiscovery.busy;
        document.querySelector('#speed-cancel-servers').hidden = !speedDiscovery.busy;
    }
    // Update automatic or manual selection and refresh dependent controls
    document.querySelector('#speed-server-mode').onchange = (event) => {
        shell.settings.server_mode = event.target.value;
        renderSpeedServers();
    };
    // Store the selected server in the editable draft
    document.querySelector('#speed-server').onchange = (event) => {
        shell.settings.server = event.target.value;
    };
    // Cancel server discovery and restore idle controls
    function cancelSpeedServers() {
        const pending = speedDiscovery.busy;
        speedDiscovery.cancel();
        if (pending) speedServerMessage('');
        renderSpeedServers();
    }
    // Start server discovery and optionally restore automatic selection
    function requestSpeedServers(detect) {
        if (speedDiscovery.busy) return;
        if (detect) shell.settings.server_mode = 'auto';
        speedServerMessage('Loading nearby servers and checking ping…');
        speedDiscovery.start({ op: 'speed_servers', scope: 'plugin' });
    }
    // Accept current server replies and report the recommended responsive server
    function receiveSpeedServers(payload) {
        if (!speedDiscovery.accepts(payload) || !shell.module?.speedtest || !shell.globalScope)
            return;
        speedDiscovery.receive(payload);
        if (payload.available && Array.isArray(payload.servers)) {
            speedServers = payload.servers
                .slice(0, 100)
                .filter(
                    (server) =>
                        typeof server.id === 'string' &&
                        server.id.length <= 64 &&
                        typeof server.name === 'string',
                );
        }
        renderSpeedServers();
        const recommended = speedServers.find(
            (server) => server.id === payload.recommended && Number.isFinite(server.latency_ms),
        );
        speedServerMessage(
            !payload.available
                ? String(payload.message || 'Could not load servers. Retry shortly.')
                : recommended
                  ? `Lowest nearby ping: ${recommended.name} (${recommended.latency_ms.toFixed(1)} ms). Automatic mode rechecks at the start of each test.`
                  : 'No nearby server responded to ping. Choose a server manually or retry.',
        );
    }
    document.querySelector('#speed-load-servers').onclick = () => requestSpeedServers(false);
    document.querySelector('#speed-detect-server').onclick = () => requestSpeedServers(true);
    // Cancel server discovery and acknowledge the user request
    document.querySelector('#speed-cancel-servers').onclick = () => {
        cancelSpeedServers();
        speedServerMessage('Server discovery cancelled.');
    };

    return {
        discovery: speedDiscovery,
        render: renderSpeedServers,
        message: speedServerMessage,
        cancel: cancelSpeedServers,
        receive: receiveSpeedServers,
        // Collect server settings and reject an empty manual selection
        collect(next) {
            if (!shell.module?.speedtest || !shell.globalScope) return;
            next.server_mode = document.querySelector('#speed-server-mode').value;
            next.server =
                document.querySelector('#speed-server').value || shell.settings.server || '';
            if (next.server_mode === 'manual' && !next.server)
                throw Error('Load nearby servers and choose a server.');
        },
        // Clear server choices when the inspector context changes
        reset() {
            speedDiscovery.cancel();
            speedServers = [];
            speedServerMessage('');
        },
    };
}

// Create weather search and shared saved-location controls
export function createWeatherFeature(shell) {
    let weatherGlobals = {},
        savedLocationDraft = {},
        vaultLocation; // undefined means the host has not answered
    const locationCredits = new Map([
        ['nominatim', '© OpenStreetMap contributors (ODbL)'],
        ['open-meteo', 'Open-Meteo geocoding · GeoNames (CC BY 4.0)'],
    ]);
    // Read saved locations from the active settings scope
    function weatherLocations() {
        const list = (shell.globalScope ? shell.settings : weatherGlobals).saved_locations;
        return Array.isArray(list) ? list : [];
    }
    // Replace the shared location draft through the appropriate scope
    function setWeatherLocations(list) {
        if (shell.globalScope) shell.settings = { ...shell.settings, saved_locations: list };
        else {
            weatherGlobals = { ...weatherGlobals, saved_locations: list };
            shell.inspector.replaceGlobalSettings(weatherGlobals);
        }
    }
    // Populate location choices while preserving missing references
    function locationOptions(select, label, selected) {
        select.replaceChildren(new Option(label, ''));
        for (const entry of weatherLocations())
            select.add(new Option(String(entry.name || entry.label || 'Location'), entry.id));
        if (selected && !weatherLocations().some((entry) => entry.id === selected))
            select.add(new Option('Missing saved location', selected));
        select.value = selected || '';
    }
    // Render saved-location links and lock coordinates inherited from a linked entry
    function renderSavedLocations() {
        document.querySelector('#saved-weather-locations').hidden = !shell.module?.location;
        if (!shell.module?.location) return;
        const editor = document.querySelector('#saved-location-list');
        locationOptions(editor, 'New location', editor.value);
        document.querySelector('#weather-location-choice-label').hidden = shell.globalScope;
        const choice = document.querySelector('#weather-location-choice');
        const usingVault = !shell.globalScope && shell.settings.use_vault_location === true;
        locationOptions(choice, 'Custom location', shell.settings.location_id);
        if (!shell.globalScope && vaultLocation !== undefined && (vaultLocation || usingVault)) {
            // Imported plugin settings can already use arbitrary IDs, so avoid collisions
            let value = '@vault-location';
            while (Array.from(choice.options).some((option) => option.value === value))
                value += '@';
            const option = new Option(
                vaultLocation ? `Vault · ${vaultLocation}` : 'Vault location unavailable',
                value,
            );
            option.dataset.vaultLocation = 'true';
            option.disabled = !vaultLocation;
            choice.add(option);
            if (usingVault) choice.value = value;
        }
        const linked = weatherLocations().find((entry) => entry.id === shell.settings.location_id);
        const missing = !shell.globalScope && !usingVault && shell.settings.location_id && !linked;
        const vaultMissing = usingVault && vaultLocation === null;
        const warning = document.querySelector('#weather-location-warning');
        warning.hidden = !missing && !vaultMissing;
        warning.textContent = vaultMissing
            ? 'Unlock the Vault or review the location shared with this Weather action.'
            : missing
              ? 'Saved location unavailable. This action keeps its saved coordinates; choose a location to repair the link.'
              : '';
        if (!shell.globalScope) {
            for (const key of ['latitude', 'longitude']) {
                const field = document.querySelector('#field-' + key);
                if (field) {
                    field.disabled = Boolean(linked) || (usingVault && vaultLocation !== undefined);
                    if (linked && !usingVault) field.value = linked[key];
                }
            }
        }
        document.querySelector('#saved-location-remove').disabled = !editor.value;
        if (shell.globalScope)
            document.querySelector('#saved-weather-locations details').open = true;
        renderLocation();
    }
    // Apply a linked location or return the action to custom coordinates
    document.querySelector('#weather-location-choice').onchange = (event) => {
        shell.settings = shell.collect();
        if (event.target.selectedOptions[0]?.dataset.vaultLocation === 'true') {
            if (vaultLocation) shell.settings.use_vault_location = true;
            shell.render();
            return;
        }
        if (vaultLocation !== undefined) shell.settings.use_vault_location = false;
        const entry = weatherLocations().find((location) => location.id === event.target.value);
        if (entry)
            Object.assign(shell.settings, {
                location_id: entry.id,
                latitude: entry.latitude,
                longitude: entry.longitude,
                location: structuredClone(entry),
            });
        else delete shell.settings.location_id;
        shell.render();
    };
    // Load the selected saved location into the location editor
    document.querySelector('#saved-location-list').onchange = (event) => {
        savedLocationDraft = structuredClone(
            weatherLocations().find((location) => location.id === event.target.value) || {},
        );
        for (const key of ['name', 'latitude', 'longitude'])
            document.querySelector('#saved-location-' + key).value = savedLocationDraft[key] ?? '';
        renderSavedLocations();
    };
    // Reset the location editor for a new entry
    document.querySelector('#saved-location-new').onclick = () => {
        const select = document.querySelector('#saved-location-list');
        select.value = '';
        select.dispatchEvent(new Event('change'));
    };
    for (const key of ['latitude', 'longitude'])
        // Mark edited coordinates as manually supplied
        document.querySelector('#saved-location-' + key).oninput = () => {
            savedLocationDraft = { source: 'manual' };
            if (shell.globalScope) renderLocation();
        };
    // Validate and save a location into the draft without committing the binding
    document.querySelector('#saved-location-save').onclick = () => {
        try {
            shell.settings = shell.collect();
        } catch (error) {
            locationMessage(error.message);
            return;
        }
        const name = document.querySelector('#saved-location-name').value.trim();
        const latitude = document.querySelector('#saved-location-latitude').value;
        const longitude = document.querySelector('#saved-location-longitude').value;
        if (
            !name ||
            !latitude ||
            !longitude ||
            !Number.isFinite(Number(latitude)) ||
            !Number.isFinite(Number(longitude)) ||
            Math.abs(Number(latitude)) > 90 ||
            Math.abs(Number(longitude)) > 180
        ) {
            locationMessage('Enter a name and valid latitude and longitude.');
            return;
        }
        const list = structuredClone(weatherLocations());
        const id = document.querySelector('#saved-location-list').value || crypto.randomUUID();
        const index = list.findIndex((entry) => entry.id === id);
        if (index < 0 && list.length >= 128) {
            locationMessage('You can save up to 128 locations.');
            return;
        }
        const entry = {
            ...savedLocationDraft,
            id,
            name,
            label: name,
            latitude: Number(latitude),
            longitude: Number(longitude),
        };
        if (index < 0) list.push(entry);
        else list[index] = entry;
        setWeatherLocations(list);
        if (!shell.globalScope)
            Object.assign(shell.settings, {
                ...(vaultLocation !== undefined ? { use_vault_location: false } : {}),
                location_id: id,
                latitude: entry.latitude,
                longitude: entry.longitude,
                location: structuredClone(entry),
            });
        shell.render();
        document.querySelector('#saved-location-list').value = id;
        renderSavedLocations();
        locationMessage('Location saved to draft. Apply settings saves the shared list.');
    };
    // Remove a saved-location draft while preserving action coordinates
    document.querySelector('#saved-location-remove').onclick = () => {
        try {
            shell.settings = shell.collect();
        } catch (error) {
            locationMessage(error.message);
            return;
        }
        const id = document.querySelector('#saved-location-list').value;
        setWeatherLocations(weatherLocations().filter((entry) => entry.id !== id));
        document.querySelector('#saved-location-new').click();
        renderSavedLocations();
        locationMessage(
            'Location removed from draft. Linked actions retain their saved coordinates.',
        );
    };

    // Show or hide location-search feedback
    function locationMessage(message) {
        const node = document.querySelector('#location-status');
        node.textContent = message;
        node.hidden = !message;
    }
    // Show selected-location details only while their coordinates match the draft
    function renderLocation() {
        const enabled = shell.module?.location === true;
        document.querySelector('#location-search').hidden = !enabled;
        if (!enabled) return;
        const saved =
            weatherLocations().find((entry) => entry.id === shell.settings.location_id) ||
            shell.settings.location;
        const matches =
            !shell.globalScope &&
            shell.settings.use_vault_location !== true &&
            saved &&
            saved.latitude === Number(document.querySelector('#field-latitude').value) &&
            saved.longitude === Number(document.querySelector('#field-longitude').value);
        const selected = document.querySelector('#location-selection');
        const label = matches && (saved.label || saved.name);
        selected.textContent = label ? `Selected: ${label}` : '';
        selected.hidden = !label;
        const source = shell.globalScope ? savedLocationDraft.source : matches && saved.source;
        document.querySelector('#location-attribution').hidden = !locationCredits.has(source);
        for (const credit of document.querySelectorAll('#location-attribution [data-source]'))
            credit.hidden = credit.dataset.source !== source;
    }
    // Validate coordinates before applying a search or device location to the draft
    function useLocation(value, source) {
        if (
            !shell.module?.location ||
            !value ||
            typeof value.latitude !== 'number' ||
            typeof value.longitude !== 'number' ||
            !Number.isFinite(value.latitude) ||
            !Number.isFinite(value.longitude) ||
            Math.abs(value.latitude) > 90 ||
            Math.abs(value.longitude) > 180
        )
            return;
        const attribution = locationCredits.get(source);
        const location = {
            latitude: value.latitude,
            longitude: value.longitude,
            source,
            label:
                source === 'device'
                    ? 'Current device location'
                    : String(value.label || '').slice(0, 2048),
            ...(attribution ? { attribution } : {}),
        };
        savedLocationDraft = { ...value, ...location };
        document.querySelector('#saved-location-latitude').value = String(value.latitude);
        document.querySelector('#saved-location-longitude').value = String(value.longitude);
        if (!document.querySelector('#saved-location-name').value)
            document.querySelector('#saved-location-name').value = savedLocationDraft.label.slice(
                0,
                128,
            );
        if (shell.globalScope) {
            document.querySelector('#saved-weather-locations details').open = true;
            renderLocation();
            locationMessage(
                'Location added to the editor. Save location to draft, then apply settings.',
            );
            return;
        }
        delete shell.settings.location_id;
        const latitude = document.querySelector('#field-latitude');
        const longitude = document.querySelector('#field-longitude');
        latitude.value = String(value.latitude);
        longitude.value = String(value.longitude);
        shell.settings = shell.collect();
        if (vaultLocation !== undefined) shell.settings.use_vault_location = false;
        shell.settings.latitude = value.latitude;
        shell.settings.longitude = value.longitude;
        shell.settings.location = location;
        shell.render();
        locationMessage('Location added to draft. Apply settings, then save the binding.');
    }
    // Detach saved-location metadata when coordinates are edited directly
    document.querySelector('#fields').addEventListener('input', (event) => {
        if (!['field-latitude', 'field-longitude'].includes(event.target.id)) return;
        delete shell.settings.location;
        delete shell.settings.location_id;
        renderSavedLocations();
    });
    // Accept location messages only from the matching parent and context
    window.addEventListener('message', (event) => {
        if (event.source !== window.parent || event.data?.context !== shell.context) return;
        if (document.referrer && event.origin !== new URL(document.referrer).origin) return;
        if (event.data?.event === 'stream-dock-manager-vault-location') {
            // The parent provides display metadata only, never a secret or coordinates
            if (
                window.parent === window ||
                !shell.context?.startsWith('settings-draft:') ||
                !supportsVaultLocation()
            )
                return;
            const location = event.data.location;
            if (
                location !== null &&
                (typeof location?.label !== 'string' || !location.label.trim())
            )
                return;
            const checkbox = document.querySelector('#field-use_vault_location');
            if (checkbox) shell.settings.use_vault_location = checkbox.checked;
            vaultLocation = location === null ? null : location.label.slice(0, 256);
            shell.render();
            return;
        }
        if (event.data?.event !== 'stream-dock-manager-location-selected') return;
        // Older Manager versions omitted the source for device fixes
        const source = event.data.location?.source ?? 'device';
        if (source === 'device' || locationCredits.has(source))
            useLocation(event.data.location, source);
    });

    function supportsVaultLocation() {
        return (
            shell.module?.location &&
            !shell.globalScope &&
            shell.module.actions?.[shell.action]?.some(
                (field) => field.key === 'use_vault_location',
            )
        );
    }
    return {
        renderSaved: renderSavedLocations,
        usesVaultPicker: () => vaultLocation !== undefined,
        requestVaultLocation() {
            if (
                !supportsVaultLocation() ||
                !shell.context?.startsWith('settings-draft:') ||
                window.parent === window
            )
                return;
            // macOS may omit the referrer for Manager's custom webview scheme
            // The fallback request contains only the event and active panel context
            window.parent.postMessage(
                { event: 'stream-dock-manager-request-vault-location', context: shell.context },
                document.referrer ? new URL(document.referrer).origin : '*',
            );
        },
        message: locationMessage,
        // Refresh saved-location choices after shared settings arrive
        globals(value) {
            weatherGlobals = value;
            if (shell.module?.location) renderSavedLocations();
        },
        // Collect shared locations or resolve the action’s selected location
        collect(next) {
            if (!shell.module?.location) return;
            if (shell.globalScope) next.saved_locations = weatherLocations();
            else {
                if (next.use_vault_location === true) return;
                const selected = weatherLocations().find((entry) => entry.id === next.location_id);
                if (selected)
                    Object.assign(next, {
                        latitude: selected.latitude,
                        longitude: selected.longitude,
                        location: structuredClone(selected),
                    });
            }
        },
        // Clear weather discovery and drafts when the inspector context changes
        reset() {
            weatherGlobals = {};
            savedLocationDraft = {};
            vaultLocation = undefined;
        },
    };
}

// Adapters register each controller once. Priorities are explicit because pending
// notices and uncorrelated errors intentionally have different legacy routing
// Register unique response owners and explicit fallback priorities
export function createFeatureRegistry(adapters) {
    const operations = new Map();
    for (const adapter of adapters) {
        if (!adapter.operation) continue;
        if (operations.has(adapter.operation)) throw Error('Duplicate feature operation');
        operations.set(adapter.operation, adapter);
    }
    // Dispatch a lifecycle hook to each adapter in registration order
    const notify = (hook, ...args) => {
        for (const adapter of adapters) adapter[hook]?.(...args);
    };
    // Order only adapters that opt into the requested fallback priority
    const priority = (key) =>
        adapters.filter((a) => a[key] !== undefined).sort((a, b) => a[key] - b[key]);
    const pending = priority('pendingPriority');
    const errors = priority('errorPriority');
    return {
        // Notify all registered features of draft collection
        collect: (value) => notify('collect', value),
        // Notify all registered features of rendering
        render: () => notify('render'),
        // Notify all registered features of context reset
        reset: () => notify('reset'),
        // Notify all registered features of shared settings
        globals: (value) => notify('globals', value),
        // Notify all registered features of transport readiness
        ready: () => notify('ready'),
        // Notify all registered features of cancellation
        cancel: () => notify('cancel'),
        // Notify all registered features of save preparation
        save: () => notify('save'),
        // Notify busy controllers when the transport becomes unavailable
        status(text) {
            if (/disconnected|unavailable/i.test(text)) {
                for (const adapter of adapters) if (adapter.busy?.()) adapter.disconnect(text);
            }
        },
        // Route pending notices to the highest-priority busy owner
        pending(text) {
            const owner = pending.find((adapter) => adapter.busy());
            owner?.message(text);
            return !!owner;
        },
        // Route correlated replies before considering uncorrelated error priorities
        receive(payload) {
            const owner = operations.get(payload?.op);
            if (owner) {
                owner.receive(payload);
                return true;
            }
            if (payload?.error) {
                const active = errors.find((adapter) => adapter.busy());
                if (active) {
                    active.error(payload.error);
                    return true;
                }
            }
            return false;
        },
    };
}
