import assert from 'node:assert/strict';
import { test } from 'node:test';
globalThis.window = { StreamDockManagerWorkflows: {} };
const { createFeatureRegistry, createWeatherFeature } =
    await import('../../inspectors/inspector-features.js');
// Create traceable adapters with independently controlled busy states
function fixture() {
    const trace = [];
    const states = [true, true, true];
    // Build an adapter that records lifecycle and response dispatch
    const adapters = ['weather', 'speed', 'microphone'].map((name, index) => {
        // Expose mutable busy state without recreating the adapter
        const entry = { operation: name, busy: () => states[index] };
        for (const hook of [
            'collect',
            'render',
            'reset',
            'globals',
            'ready',
            'cancel',
            'save',
            'disconnect',
            'message',
            'receive',
            'error',
        ]) {
            entry[hook] = (value) => trace.push([name, hook, value]);
        }
        return entry;
    });
    adapters[0].pendingPriority = 0;
    adapters[0].errorPriority = 1;
    adapters[2].pendingPriority = 1;
    adapters[2].errorPriority = 0;
    return { trace, states, registry: createFeatureRegistry(adapters) };
}
for (const hook of ['collect', 'render', 'reset', 'globals', 'ready', 'cancel', 'save']) {
    // Verify lifecycle hooks preserve registration order
    test(`registry dispatches ${hook} in registration order`, () => {
        const { trace, registry } = fixture();
        registry[hook]({ setting: 'untouched' });
        assert.deepEqual(
            trace.map(([name]) => name),
            ['weather', 'speed', 'microphone'],
        );
        assert.ok(trace.every((entry) => entry[1] === hook));
    });
}
// Verify correlated responses bypass fallback busy priorities
test('matching operations route to one owner, regardless of busy priority', () => {
    const { trace, registry } = fixture();
    for (const name of ['weather', 'speed', 'microphone']) {
        const payload = { op: name, error: 'correlated error' };
        assert.equal(registry.receive(payload), true);
        assert.deepEqual(trace.pop(), [name, 'receive', payload]);
    }
    assert.equal(registry.receive({ op: 'unrelated' }), false);
    assert.equal(registry.receive(null), false);
    assert.equal(trace.length, 0);
});
// Verify pending and error priorities differ and omit unregistered fallback owners
test('pending and uncorrelated errors retain distinct priorities and speed omission', () => {
    const { trace, states, registry } = fixture();
    registry.pending('pending');
    assert.deepEqual(trace.pop(), ['weather', 'message', 'pending']);
    registry.receive({ error: 'failed' });
    assert.deepEqual(trace.pop(), ['microphone', 'error', 'failed']);
    states[0] = states[2] = false;
    assert.equal(registry.pending('pending'), false);
    assert.equal(registry.receive({ error: 'failed' }), false);
    assert.equal(trace.length, 0);
});
// Verify ordinary status leaves controllers running and disconnect affects only busy owners
test('disconnect affects busy controllers only; ordinary status does not cancel', () => {
    const { trace, states, registry } = fixture();
    registry.status('ready');
    assert.equal(trace.length, 0);
    states[1] = false;
    registry.status('Disconnected');
    assert.deepEqual(
        trace.map(([name, hook]) => [name, hook]),
        [
            ['weather', 'disconnect'],
            ['microphone', 'disconnect'],
        ],
    );
});
// Reject duplicate operation ownership before dispatch begins
test('duplicate response ownership is rejected', () => {
    assert.throws(
        () => createFeatureRegistry([{ operation: 'same' }, { operation: 'same' }]),
        /Duplicate/,
    );
});

// Exercise the actual location controller with a minimal dropdown and trusted parent
test('Weather lists only the host-shared Vault label and keeps fallback coordinates', () => {
    const nodes = new Map();
    const requests = [];
    let receive;
    globalThis.Option = class {
        dataset = {};
        constructor(text, value) {
            Object.assign(this, { text, value });
        }
    };
    globalThis.document = {
        referrer: 'https://manager.invalid/index.html',
        querySelector(key) {
            if (key === '#field-use_vault_location') return null;
            if (!nodes.has(key))
                nodes.set(key, {
                    value: '',
                    options: [],
                    add(option) {
                        this.options.push(option);
                    },
                    replaceChildren(...children) {
                        this.options = children;
                    },
                    get selectedOptions() {
                        return this.options.filter((option) => option.value === this.value);
                    },
                    addEventListener() {},
                });
            return nodes.get(key);
        },
        querySelectorAll() {
            return [];
        },
    };
    const parent = { postMessage: (...message) => requests.push(message) };
    globalThis.window = { parent, addEventListener: (_, callback) => (receive = callback) };
    const shell = {
        context: 'settings-draft:session',
        action: 'weather',
        module: { location: true, actions: { weather: [{ key: 'use_vault_location' }] } },
        settings: { latitude: 1, longitude: 2, location_id: '@vault-location' },
        collect() {
            return structuredClone(this.settings);
        },
        render() {
            feature.renderSaved();
        },
    };
    const feature = createWeatherFeature(shell);
    const choices = document.querySelector('#weather-location-choice');
    feature.globals({
        saved_locations: [{ id: '@vault-location', name: 'Local', latitude: 1, longitude: 2 }],
    });
    feature.requestVaultLocation();
    assert.deepEqual(requests, [
        [
            { event: 'stream-dock-manager-request-vault-location', context: shell.context },
            'https://manager.invalid',
        ],
    ]);
    const response = {
        source: parent,
        origin: 'https://manager.invalid',
        data: {
            event: 'stream-dock-manager-vault-location',
            context: shell.context,
            location: { label: 'Home', latitude: 98, longitude: 99 },
        },
    };
    receive({ ...response, source: {} });
    receive({ ...response, origin: 'https://other.invalid' });
    receive({ ...response, data: { ...response.data, context: 'settings-draft:old' } });
    assert.equal(choices.options.length, 2);
    receive(response);
    const option = choices.options.find((item) => item.dataset.vaultLocation);
    assert.equal(option.text, 'Vault · Home');
    assert.notEqual(option.value, '@vault-location');
    choices.value = option.value;
    choices.onchange({ target: choices });
    assert.deepEqual(shell.settings, {
        latitude: 1,
        longitude: 2,
        location_id: '@vault-location',
        use_vault_location: true,
    });
    assert.equal(document.querySelector('#field-latitude').disabled, true);
    receive({ ...response, data: { ...response.data, location: null } });
    assert.equal(choices.selectedOptions[0].text, 'Vault location unavailable');
    assert.equal(shell.settings.use_vault_location, true);
    choices.value = '';
    choices.onchange({ target: choices });
    assert.deepEqual(shell.settings, { latitude: 1, longitude: 2, use_vault_location: false });
    assert.equal(document.querySelector('#field-latitude').disabled, false);

    document.referrer = '';
    feature.reset();
    feature.renderSaved();
    feature.requestVaultLocation();
    assert.deepEqual(requests.at(-1), [
        { event: 'stream-dock-manager-request-vault-location', context: shell.context },
        '*',
    ]);
    assert.equal(choices.options.length, 1);
    receive({ ...response, origin: 'null', source: {} });
    receive({
        ...response,
        origin: 'null',
        data: { ...response.data, context: 'settings-draft:old' },
    });
    assert.equal(choices.options.length, 1);
    receive({ ...response, origin: 'null' });
    assert.equal(choices.options.find((item) => item.dataset.vaultLocation)?.text, 'Vault · Home');
});
