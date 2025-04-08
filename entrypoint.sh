#!/bin/sh

# Base command
CMD="/app/imageproxy -addr 0.0.0.0:8080"

# Add -signatureKey if SIGNATURE_KEY is defined
if [ -n "$SIGNATURE_KEY" ]; then
  CMD="$CMD -signatureKey $SIGNATURE_KEY"
fi

# Log the startup command explicitly to stdout
echo "Starting imageproxy with command: $CMD" >&1

# Execute the command
exec $CMD