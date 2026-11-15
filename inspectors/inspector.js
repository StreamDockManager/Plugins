import {
    createFeatureRegistry,
    createMicrophoneFeature,
    createSpeedFeature,
    createWeatherFeature,
} from './inspector-features.js';

let context,
    action,
    module,
    globalScope = false,
    settings = {},
    fields = [];
let targetKey = '',
    targetChoices = [],
    targetConnected = false;
const { DiscoveryController } = window.StreamDockManagerWorkflows;
const targetDiscovery = new DiscoveryController({
    // Send target discovery through the shared inspector transport
    send: (payload) => inspector.send(payload),
    cancelOp: 'cancel_discovery',
    timeout: 20000,
    // Refresh target availability and explain discovery failures
    changed(phase) {
        targetLoad.disabled = !targetConnected || targetDiscovery.busy;
        if (phase === 'loading') targetMessage('Looking for choices…');
        if (phase === 'timeout')
            targetMessage('Discovery timed out. Try again or enter an identifier below.');
        if (phase === 'error')
            targetMessage('Discovery is unavailable. Try again or enter an identifier below.');
    },
});
const targetPicker = document.querySelector('#target-picker');
const targetChoice = document.querySelector('#target-choice');
const targetLoad = document.querySelector('#target-load');
const form = document.querySelector('#form');
const status = document.querySelector('#status');
const preview = document.querySelector('#preview');
const advanced = document.querySelector('#advanced');
// Collect feature drafts and validate the supported refresh policy
function collect() {
    const next = window.StreamDockManagerFields.collect(renderedFields(), settings, {
        scope: context,
    });
    features.collect(next);
    if (!globalScope && fields.some((field) => field.key === 'update_interval_ms')) {
        const policy = module.refresh?.[action];
        const interval = next.update_interval_ms;
        if (
            !policy ||
            !Number.isSafeInteger(interval) ||
            (interval !== 0 && (interval < policy.minimum_ms || interval > policy.maximum_ms))
        )
            throw Error('Choose 0 to load once, or a supported automatic read interval.');
        // The shared SDK merges collected fields before serializing. Explicitly
        // clear the retired key so that merge cannot resurrect it in the wire JSON
        if (Object.hasOwn(settings, 'interval')) next.interval = undefined;
    }
    return window.StreamDockManagerFields.validate(fields, next);
}
// Exclude fields rendered by dedicated feature pickers
function renderedFields() {
    if (module?.microphones && !globalScope)
        return fields.filter((field) => field.key !== 'device');
    return module?.speedtest && globalScope
        ? fields.filter((field) => !['server_mode', 'server'].includes(field.key))
        : fields;
}
// Reject events outside the inspector plugin-message channel
function send(event, payload) {
    if (event === 'sendToPlugin') return inspector.send(payload);
    throw Error('Unsupported inspector event');
}
// Render ordinary fields and feature-specific draft controls
function render(reset = false) {
    window.StreamDockManagerFields.render(
        document.querySelector('#fields'),
        renderedFields(),
        settings,
        {
            scope: context,
            reset,
        },
    );
    renderPathPickers();
    renderTargetPicker();
    advanced.value = window.StreamDockManagerWorkflows.advanced(settings, fields);
    document.querySelector('#hiscores-help').hidden = !module?.hiscores || globalScope;
    features.render();
}
// Feature controllers own their draft/discovery state. The shell is the only
// owner of settings and the shared transport, so scope and generation checks stay central
const shell = {
    // Expose current module metadata to feature controllers
    get module() {
        return module;
    },
    // Expose whether the inspector edits plugin-wide settings
    get globalScope() {
        return globalScope;
    },
    // Expose the active action context for scoped messages
    get context() {
        return context;
    },
    // Read the shell-owned settings draft
    get settings() {
        return settings;
    },
    // Replace the shell-owned draft without persisting it
    set settings(value) {
        settings = value;
    },
    // Expose the shared inspector transport
    get inspector() {
        return inspector;
    },
    collect,
    render,
};
const microphoneFeature = createMicrophoneFeature(shell);
const speedFeature = createSpeedFeature(shell);
const weatherFeature = createWeatherFeature(shell);
const features = createFeatureRegistry([
    {
        operation: 'location_results',
        pendingPriority: 0,
        errorPriority: 1,
        // Report whether weather discovery is pending
        busy: () => weatherFeature.discovery.busy,
        // Collect the weather draft fields
        collect: (next) => weatherFeature.collect(next),
        // Refresh both weather search and saved-location controls
        render: () => {
            weatherFeature.render();
            weatherFeature.renderSaved();
        },
        // Reset weather state for the new context
        reset: () => weatherFeature.reset(),
        // Forward shared settings to the weather controller
        globals: (value) => weatherFeature.globals(value),
        // Cancel pending weather discovery
        cancel: () => weatherFeature.cancel(),
        // Finish weather discovery with the disconnect reason
        disconnect: (text) => weatherFeature.finish(text),
        // Finish weather discovery with the error message
        error: (text) => weatherFeature.finish(text),
        // Display weather feedback
        message: (text) => weatherFeature.message(text),
        // Deliver a correlated reply to the weather controller
        receive: (payload) => weatherFeature.receive(payload),
    },
    {
        operation: 'speed_servers',
        // Report whether speed discovery is pending
        busy: () => speedFeature.discovery.busy,
        // Collect the speed draft fields
        collect: (next) => speedFeature.collect(next),
        // Refresh speed controls
        render: () => speedFeature.render(),
        // Reset speed state for the new context
        reset: () => speedFeature.reset(),
        // Cancel pending speed discovery
        cancel: () => speedFeature.cancel(),
        // Stop speed discovery before saving
        save: () => speedFeature.cancel(),
        // Stop speed discovery and show the disconnect reason
        disconnect: (text) => {
            speedFeature.cancel();
            speedFeature.message(text);
        },
        // Deliver a correlated reply to the speed controller
        receive: (payload) => speedFeature.receive(payload),
    },
    {
        operation: 'microphones',
        pendingPriority: 1,
        errorPriority: 0,
        // Report whether microphone discovery is pending
        busy: () => microphoneFeature.discovery.busy,
        // Collect the microphone draft fields
        collect: (next) => microphoneFeature.collect(next),
        // Refresh microphone controls
        render: () => microphoneFeature.render(),
        // Reset microphone state for the new context
        reset: () => microphoneFeature.reset(),
        // Discover microphones once the action inspector is ready
        ready: () => {
            if (module?.microphones && !globalScope) microphoneFeature.request();
        },
        // Cancel pending microphone discovery
        cancel: () => microphoneFeature.cancel(),
        // Stop microphone discovery before saving
        save: () => microphoneFeature.cancel(),
        // Stop microphone discovery and show the disconnect reason
        disconnect: (text) => {
            microphoneFeature.cancel();
            microphoneFeature.message(text);
        },
        // Stop microphone discovery and display the plugin error
        error: (text) => {
            microphoneFeature.cancel();
            microphoneFeature.message(String(text));
        },
        // Display microphone feedback
        message: (text) => microphoneFeature.message(text),
        // Deliver a correlated reply to the microphone controller
        receive: (payload) => microphoneFeature.receive(payload),
    },
]);
const inspector = window.StreamDockManagerInspector({
    // Fetch module metadata without mutating the active view
    async initialize(value, host, details) {
        const nextModule = await fetch('settings-module.json').then((response) => response.json());
        // The shared transport owns the connection generation. Returning metadata
        // prevents a stale fetch from mutating the active product view
        return { nextModule, details, host };
    },
    // Reset feature state after the transport accepts the new context
    initialized({ nextModule, details, host }) {
        window.StreamDockManagerWorkflows.clearChoices(fields, { scope: context });
        module = nextModule;
        targetDiscovery.cancel();
        features.reset();
        context = details.context;
        action = details.action;
        globalScope = details.payload?.scope === 'plugin';
        fields = (globalScope ? module.plugin : module.actions[action]) || [];
        targetKey = fields.find((field) => ['app_id', 'service'].includes(field.key))?.key || '';
        targetChoices = [];
        targetMessage('');
        document.querySelector('#title').textContent =
            `${module.name} · ${globalScope ? 'Plugin' : 'Action'} settings`;
        const runtime = host.streamDockManager;
        document.querySelector('#runtime').textContent = runtime?.setup_runtime
            ? 'Setup tools run while this panel is open. Save your settings when you’re ready.'
            : runtime?.enabled === false
              ? 'Enable this plugin in Plugins to use search, discovery, and testing. You can edit and save settings now.'
              : 'Enabled plugins start automatically when configuring them. No active binding is required.';
    },
    // Render the received settings as a fresh draft
    render(value) {
        settings = value;
        render(true);
    },
    // Forward shared settings to feature controllers
    globals(value) {
        features.globals(value);
    },
    // Notify features that the inspector transport is ready
    ready() {
        features.ready();
    },
    // Keep discovery availability synchronized with connection state
    connected(connected) {
        targetConnected = connected;
        targetLoad.disabled = !connected || targetDiscovery.busy;
    },
    // Collect the draft and refresh its advanced JSON representation
    collect() {
        settings = collect();
        advanced.value = window.StreamDockManagerWorkflows.advanced(settings, fields);
        return settings;
    },
    // Display transport status and stop disconnected discovery
    status(text) {
        status.textContent = text;
        features.status(text);
        if (targetDiscovery.busy && /disconnected|unavailable/i.test(text)) {
            targetDiscovery.cancel();
            targetMessage(text);
        }
    },
    // Route validated picker selections and correlated plugin replies
    message(event) {
        if (event.event === 'settingsFieldSelected') {
            const { key, value } = event.payload;
            if (
                typeof value !== 'string' ||
                !fields.some((field) => field.key === key && field.type === 'path')
            )
                return;
            document.getElementById(`field-${key}`).value = value;
            settings = { ...settings, [key]: value };
            advanced.value = window.StreamDockManagerWorkflows.advanced(settings, fields);
            return;
        }
        if (event.event !== 'sendToPropertyInspector') return;
        if (event.payload?.pending) {
            if (features.pending(String(event.payload.message || 'Starting plugin…'))) return;
            if (targetDiscovery.busy) targetMessage(event.payload.message || 'Starting plugin…');
            else status.textContent = String(event.payload.message || 'Starting plugin…');
            return;
        }
        if (features.receive(event.payload)) return;
        if (Array.isArray(event.payload)) {
            targetMessage('Unsupported discovery response. Update the plugin package.');
            return;
        }
        if (
            targetKey &&
            (event.payload?.devices ||
                ['local_services', 'steam_games'].includes(event.payload?.op)) &&
            !targetDiscovery.accepts(event.payload)
        )
            return;
        preview.textContent = JSON.stringify(event.payload, null, 2);
        const devices = event.payload?.devices || [];
        window.StreamDockManagerWorkflows.choices(fields, devices, { scope: context });
        if (
            targetKey &&
            (devices.some((device) => device.kind === targetKey) ||
                ['local_services', 'steam_games'].includes(event.payload?.op))
        ) {
            if (!targetDiscovery.receive(event.payload, devices)) return;
            targetChoices = targetDiscovery.choices.filter(
                (device) => device.kind === targetKey && typeof device.id === 'string',
            );
            targetMessage(
                event.payload?.error ||
                    (targetChoices.length
                        ? ''
                        : 'No matches found. You can enter an identifier below.'),
            );
            renderTargetPicker();
        } else if (targetDiscovery.busy && event.payload?.error) {
            targetDiscovery.receive(event.payload);
            targetMessage(String(event.payload.error));
        }
    },
});
// Stop feature discovery before submitting settings through the SDK
form.onsubmit = (event) => {
    event.preventDefault();
    features.save();
    inspector.save();
};
// Cancel feature discovery and discard the inspector draft
document.querySelector('#cancel').onclick = () => {
    features.cancel();
    inspector.cancel();
};
// Validate advanced JSON before replacing the editable draft
document.querySelector('#apply-json').onclick = () => {
    try {
        const value = window.StreamDockManagerWorkflows.applyJSON(
            advanced.value,
            fields,
            collect(),
        );
        features.cancel();
        settings = value;
        inspector.replaceSettings(value);
        render();
    } catch (error) {
        status.textContent = error.message;
    }
};
for (const [id, op] of [
    ['discover', 'discover'],
    ['test', 'test'],
])
    // Use specialized discovery when available and otherwise request the plugin operation
    document.getElementById(id).onclick = () => {
        if (op === 'discover') {
            if (module?.microphones && !globalScope) return microphoneFeature.request();
            if (targetKey) return loadTargets();
        }
        return window.StreamDockManagerWorkflows.request(
            inspector,
            op,
            collect(),
            globalScope ? 'plugin' : 'action',
        );
    };

// Add host-backed path buttons only inside a supported inline inspector
function renderPathPickers() {
    if (!inspector.state().host?.streamDockManager?.inline_path_pickers || window.parent === window)
        return;
    const labels = { file: 'Choose file…', folder: 'Choose folder…', save: 'Choose destination…' };
    for (const field of fields) {
        if (field.type !== 'path' || !Object.hasOwn(labels, field.path_picker)) continue;
        const label = document.getElementById(`field-${field.key}`).parentElement;
        const group = document.createElement('div');
        group.className = 'path-field';
        const button = document.createElement('button');
        button.type = 'button';
        button.textContent = labels[field.path_picker];
        button.setAttribute('aria-label', `${labels[field.path_picker]} ${field.label}`);
        // Request a scoped path selection from the embedding host
        button.onclick = () => {
            const origin = document.referrer && new URL(document.referrer).origin;
            window.parent.postMessage(
                { event: 'stream-dock-manager-choose-settings-path', context, key: field.key },
                origin && origin !== 'null' ? origin : '*',
            );
        };
        label.before(group);
        group.append(label, button);
    }
}

// Render discovered targets while retaining manually entered identifiers
function renderTargetPicker() {
    targetPicker.hidden = !targetKey;
    if (!targetKey) return;
    const steam = targetKey === 'app_id';
    document.querySelector('#target-label').textContent = steam
        ? 'Installed Steam game'
        : 'Local service';
    targetLoad.textContent = steam ? 'Load installed games' : 'Load local services';
    targetChoice.replaceChildren(new Option(steam ? 'Choose a game' : 'Choose a service', ''));
    targetChoice.options[0].disabled = true;
    for (const { id, name } of targetChoices) targetChoice.add(new Option(name || id, id));
    targetChoice.disabled = !targetChoices.length;
    const input = document.getElementById(`field-${targetKey}`);
    // Select a discovered choice only when it matches the identifier field
    const syncChoice = () => {
        targetChoice.value = targetChoices.some(({ id }) => id === input.value) ? input.value : '';
    };
    input.oninput = syncChoice;
    syncChoice();
}

// Show or hide target discovery feedback
function targetMessage(text) {
    const status = document.querySelector('#target-status');
    status.textContent = text;
    status.hidden = !text;
}

// Start one scoped target discovery request at a time
function loadTargets() {
    if (targetDiscovery.busy) return;
    targetDiscovery.start({ op: 'discover', scope: 'action', settings: {} });
}
targetLoad.onclick = loadTargets;
// Copy the selected target into its ordinary settings field
targetChoice.onchange = () => {
    document.getElementById(`field-${targetKey}`).value = targetChoice.value;
};
