#!/bin/sh
# Legacy automatic installers are deliberately gated in SUB6.
printf "%s\n" "Build this reviewed source using deploy/compose.sub6.yml; see deploy/README.md." >&2
exit 1
