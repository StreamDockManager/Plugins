import assert from 'node:assert/strict';
import { test } from 'node:test';
globalThis.window = { StreamDockManagerWorkflows: {} };
const { createFeatureRegistry } = await import('../../inspectors/inspector-features.js');
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
