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
        savedLocationDraft = {};
    const locationDiscovery = new DiscoveryController({
        // Send location searches through the shell-owned transport
        send: (payload) => shell.inspector.send(payload),
        cancelOp: 'cancel_location_search',
        timeout: 25000,
        // Refresh search controls and explain discovery failures
        changed(phase) {
            document.querySelector('#location-submit').disabled = locationDiscovery.busy;
            document.querySelector('#location-cancel').hidden = !locationDiscovery.busy;
            if (phase === 'timeout') locationMessage('Location search timed out. Try again.');
            if (phase === 'error') locationMessage('Location search is unavailable. Try again.');
        },
    });
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
        locationOptions(
            document.querySelector('#weather-location-choice'),
            'Custom location',
            shell.settings.location_id,
        );
        const linked = weatherLocations().find((entry) => entry.id === shell.settings.location_id);
        const missing = !shell.globalScope && shell.settings.location_id && !linked;
        const warning = document.querySelector('#weather-location-warning');
        warning.hidden = !missing;
        warning.textContent = missing
            ? 'Saved location unavailable. This action keeps its saved coordinates; choose a location to repair the link.'
            : '';
        if (!shell.globalScope) {
            for (const key of ['latitude', 'longitude']) {
                const field = document.querySelector('#field-' + key);
                if (field) {
                    field.disabled = Boolean(linked);
                    if (linked) field.value = linked[key];
                }
            }
        }
        document.querySelector('#saved-location-remove').disabled = !editor.value;
        if (shell.globalScope)
            document.querySelector('#saved-weather-locations details').open = true;
    }
    // Apply a linked location or return the action to custom coordinates
    document.querySelector('#weather-location-choice').onchange = (event) => {
        shell.settings = shell.collect();
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
    // Stop pending location discovery before showing its final status
    function finishLocation(message) {
        if (locationDiscovery.busy) locationDiscovery.cancel();
        locationMessage(message);
    }
    // Cancel search and clear obsolete results
    function cancelLocation() {
        locationDiscovery.cancel();
        finishLocation('');
        document.querySelector('#location-results').replaceChildren();
    }
    // Show selected-location details only while their coordinates match the draft
    function renderLocation() {
        const enabled = shell.module?.location === true;
        document.querySelector('#location-search').hidden = !enabled;
        if (!enabled) return;
        const saved = shell.settings.location;
        const matches =
            !shell.globalScope &&
            saved &&
            saved.latitude === Number(document.querySelector('#field-latitude').value) &&
            saved.longitude === Number(document.querySelector('#field-longitude').value);
        const selected = document.querySelector('#location-selection');
        selected.textContent = matches ? `Selected: ${saved.label}` : '';
        selected.hidden = !matches;
        document.querySelector('#location-attribution').hidden = !(
            matches && saved.source === 'nominatim'
        );
    }
    // Validate coordinates before applying a search or device location to the draft
    function useLocation(value, source) {
        if (
            !shell.module?.location ||
            typeof value.latitude !== 'number' ||
            typeof value.longitude !== 'number' ||
            !Number.isFinite(value.latitude) ||
            !Number.isFinite(value.longitude) ||
            Math.abs(value.latitude) > 90 ||
            Math.abs(value.longitude) > 180
        )
            return;
        savedLocationDraft = {
            ...value,
            source,
            label:
                source === 'device'
                    ? 'Current device location'
                    : String(value.label || '').slice(0, 2048),
        };
        document.querySelector('#saved-location-latitude').value = String(value.latitude);
        document.querySelector('#saved-location-longitude').value = String(value.longitude);
        if (!document.querySelector('#saved-location-name').value)
            document.querySelector('#saved-location-name').value = savedLocationDraft.label.slice(
                0,
                128,
            );
        if (shell.globalScope) {
            document.querySelector('#saved-weather-locations details').open = true;
            cancelLocation();
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
        cancelLocation();
        shell.settings.latitude = value.latitude;
        shell.settings.longitude = value.longitude;
        shell.settings.location = {
            latitude: value.latitude,
            longitude: value.longitude,
            source,
            label:
                source === 'device'
                    ? 'Current device location'
                    : String(value.label).slice(0, 2048),
            ...(source === 'nominatim'
                ? { attribution: '© OpenStreetMap contributors (ODbL)' }
                : {}),
        };
        shell.render();
        locationMessage('Location added to draft. Apply settings, then save the binding.');
    }
    // Render a bounded set of correlated search results as selectable locations
    function receiveLocations(payload) {
        if (!locationDiscovery.receive(payload)) return;
        finishLocation(String(payload.message || ''));
        const list = document.querySelector('#location-results');
        list.replaceChildren();
        for (const result of Array.isArray(payload.results) ? payload.results.slice(0, 5) : []) {
            const row = document.createElement('li');
            const label = document.createElement('span');
            label.textContent = `${result.label} (${result.latitude}, ${result.longitude})`;
            const button = document.createElement('button');
            button.type = 'button';
            button.textContent = 'Use location';
            button.setAttribute('aria-label', `Use location: ${result.label}`);
            button.onclick = () => useLocation(result, 'nominatim');
            row.append(label, button);
            list.append(row);
        }
        document.querySelector('#location-attribution').hidden = list.childElementCount === 0;
    }
    // Validate search inputs before starting a scoped location lookup
    function searchLocation() {
        if (!shell.module?.location) return;
        cancelLocation();
        const query = document.querySelector('#location-query').value.trim();
        const country = document.querySelector('#location-country').value;
        const mode = document.querySelector('#location-mode').value;
        if (!query || (mode === 'postal' && !country)) {
            locationMessage('Enter a location and choose its country.');
            return;
        }
        locationMessage('Searching…');
        locationDiscovery.start({ op: 'search_location', search: { mode, query, country } });
    }
    document.querySelector('#location-submit').onclick = searchLocation;
    document.querySelector('#location-cancel').onclick = cancelLocation;
    // Run location search on Enter without submitting the settings form
    document.querySelector('#location-query').onkeydown = (event) => {
        if (event.key === 'Enter') {
            event.preventDefault();
            searchLocation();
        }
    };
    for (const id of ['location-query', 'location-country', 'location-mode'])
        // Cancel stale searches when search parameters change
        document.getElementById(id).addEventListener('input', () => {
            cancelLocation();
            document.querySelector('#location-query-label').textContent =
                document.querySelector('#location-mode').value === 'postal'
                    ? 'ZIP / postal code'
                    : 'Address or place';
        });
    // Detach saved-location metadata when coordinates are edited directly
    document.querySelector('#fields').addEventListener('input', (event) => {
        if (!['field-latitude', 'field-longitude'].includes(event.target.id)) return;
        delete shell.settings.location;
        delete shell.settings.location_id;
        renderLocation();
        renderSavedLocations();
    });
    const countries =
        'AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW'.split(
            ' ',
        );
    const names = new Intl.DisplayNames(['en'], { type: 'region' });
    const countrySelect = document.querySelector('#location-country');
    countrySelect.add(new Option('Choose country', ''));
    for (const code of countries.sort((a, b) => names.of(a).localeCompare(names.of(b))))
        countrySelect.add(new Option(names.of(code), code.toLowerCase()));
    // Accept device-location messages only from the matching parent and context
    window.addEventListener('message', (event) => {
        if (
            event.source !== window.parent ||
            event.data?.event !== 'stream-dock-manager-location-selected' ||
            event.data?.context !== shell.context
        )
            return;
        if (document.referrer && event.origin !== new URL(document.referrer).origin) return;
        useLocation(event.data.location, 'device');
    });

    return {
        discovery: locationDiscovery,
        render: renderLocation,
        renderSaved: renderSavedLocations,
        message: locationMessage,
        finish: finishLocation,
        cancel: cancelLocation,
        receive: receiveLocations,
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
            locationDiscovery.cancel();
            weatherGlobals = {};
            savedLocationDraft = {};
        },
    };
}

// Adapters register each controller once. Priorities are explicit because pending
// notices and uncorrelated errors intentionally have different legacy routing
// Register unique response owners and explicit fallback priorities
export function createFeatureRegistry(adapters) {
    const operations = new Map();
    for (const adapter of adapters) {
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
                for (const adapter of adapters) if (adapter.busy()) adapter.disconnect(text);
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
