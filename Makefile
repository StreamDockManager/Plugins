# Standalone GNU Make entry points for this repository
PROJECT := $(shell makefile='$(subst ','"'"',$(MAKEFILE_LIST))'; makefile=$$(printf '%s' "$$makefile" | sed 's/^ //'); cd "$$(dirname "$$makefile")" && pwd)
PYTHON ?= python3
export WORKSPACE_CONFIG
export WORKSPACE_JOBS
export PLUGIN
export PLUGIN_HOST
TARGETS := help deps build package unified cross check test catalog assets
.PHONY: $(TARGETS)
$(TARGETS):
	@$(PYTHON) "$(PROJECT)/scripts/build.py" "$@"

.PHONY: sdk-pin-check sdk-pin-update
sdk-pin-check:
	@$(PYTHON) "$(PROJECT)/scripts/sdk_pin.py" check $(if $(SDK_VERSION),--version "$(SDK_VERSION)",)
sdk-pin-update:
	@test -n "$(SDK_VERSION)" || { echo 'Set SDK_VERSION=vX.Y.Z to a published SDK release'; exit 1; }
	@$(PYTHON) "$(PROJECT)/scripts/sdk_pin.py" update "$(SDK_VERSION)"
