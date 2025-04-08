#!/bin/sh

# Ensure ADDR is set to 0.0.0.0:8080
export ADDR="0.0.0.0:8080"

# Base command
CMD="/app/imageproxy -addr $ADDR"

# Add -signatureKey if SIGNATURE_KEY is defined
if [ -n "$SIGNATURE_KEY" ]; then
  CMD="$CMD -signatureKey $SIGNATURE_KEY"
fi

# Log the startup command explicitly to stdout
echo "Starting imageproxy with command: $CMD"

# Execute the command
exec $CMD