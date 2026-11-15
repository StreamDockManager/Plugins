"""Validate suggested profiles in official artifacts without executing their payloads."""
import json
from pathlib import Path
HARDWARE = Path(__file__).resolve().parents[1] / 'internal/catalog/profile-hardware.v1.json'

# Validate the suggested-profile workspace envelope and version
def validate_workspace_envelope(document):
    if not isinstance(document, dict):
        raise ValueError('Suggested workspace must be an object')
    for field in ('schema_version', 'configuration_revision', 'saved_macros', 'rules', 'history', 'active_profile', 'profiles'):
        if field not in document:
            raise ValueError('Suggested workspace missing required field ' + field)
    if type(document['schema_version']) is not int or document['schema_version'] != 1:
        raise ValueError('Invalid suggested profile schema')
    revision = document['configuration_revision']
    if type(revision) is not int or not 0 <= revision <= 2**64 - 1:
        raise ValueError('Invalid suggested workspace configuration_revision')
    for field in ('saved_macros', 'rules', 'profiles'):
        if not isinstance(document[field], list):
            raise ValueError('Suggested workspace ' + field + ' must be an array')
    if not isinstance(document['active_profile'], str):
        raise ValueError('Suggested workspace active_profile must be a string')
    history = document['history']
    if not isinstance(history, dict) or set(history) != {'undo', 'redo'} or any(not isinstance(history[field], list) for field in ('undo', 'redo')):
        raise ValueError('Suggested workspace history needs undo and redo arrays')

# Validate suggested profiles against action and hardware constraints
def validate_profiles(archive, prefix, definition):
    models = json.loads(HARDWARE.read_text())['models']
    capabilities = definition['capabilities']['actions']
    for model in models:
        document = json.loads(archive.read(prefix + 'profiles/' + model['model_id'] + '.json'))
        validate_workspace_envelope(document)
        if len(document['profiles']) != 1:
            raise ValueError('Invalid suggested profile schema')
        profile = document['profiles'][0]
        controls = {control['id']: control for control in model['controls']}
        if profile['model_id'] != model['model_id']:
            raise ValueError('Suggested hardware mismatch')
        if document['active_profile'] != profile['id']:
            raise ValueError('Invalid suggested active profile')
        pages = {page['id'] for page in profile['pages']}
        if not pages or len(pages) != len(profile['pages']) or profile['active_page'] not in pages:
            raise ValueError('Invalid suggested pages')
        found = set()
        # Check profile references and placement constraints before packaging
        def check(action, control, depth=0):
            if not isinstance(action, dict):
                raise ValueError('Invalid generated profile action')
            if action.get('kind') == 'page':
                if action.get('page_id') not in pages:
                    raise ValueError('Missing suggested navigation target')
            elif action.get('kind') == 'plugin':
                capability = capabilities.get(action.get('action_id'))
                if action.get('plugin_id') != definition['id'] or not capability or control['kind'] not in capability['control_types']:
                    raise ValueError('Invalid suggested plugin action/controller')
                found.add(action['action_id'])
            elif action.get('kind') == 'dial':
                if depth != 0 or control['kind'] != 'encoder' or not isinstance(action.get('actions'), list) or len(action['actions']) != 3:
                    raise ValueError('Suggested dial needs an encoder and three explicit actions')
                for child in action['actions']:
                    check(child, control, depth + 1)
            else:
                raise ValueError('Unexpected generated profile action')
        for page in profile['pages']:
            for control_id, binding in page['bindings'].items():
                if control_id not in controls:
                    raise ValueError('Unknown suggested hardware control')
                action = binding.get('action')
                if not isinstance(action, dict):
                    raise ValueError('Invalid generated profile action')
                if controls[control_id]['kind'] == 'encoder' and action.get('kind') != 'dial':
                    raise ValueError('Suggested encoder needs explicit left, right and press actions')
                check(action, controls[control_id])
        expected = {identity for identity, capability in capabilities.items() if any(control['kind'] in capability['control_types'] for control in controls.values())}
        if found != expected or set(document.get('unavailable_actions', [])) != set(capabilities) - expected:
            raise ValueError('Suggested profile action coverage mismatch')
        configuration = document.get('plugin_configuration', {})
        if set(configuration) != {'settings', 'action_defaults'} or set(configuration['action_defaults']) - set(capabilities):
            raise ValueError('Invalid suggested configuration ownership')
