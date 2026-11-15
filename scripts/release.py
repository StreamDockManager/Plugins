#!/usr/bin/env python3
"""Validate explicit plugin versions. Publication is handled by deploy_catalog.py."""
import argparse
import catalog


# Validate plugin and minimum-Manager versions from source definitions
def versions():
    for plugin in catalog.source_records():
        catalog.semver(plugin['version'])
        catalog.semver(plugin['minimum_manager'])


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['version'])
    parser.parse_args()
    versions()
