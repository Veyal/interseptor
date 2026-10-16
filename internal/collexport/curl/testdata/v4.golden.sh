#!/bin/sh
# Collection: Example API
# Exported by Interseptor. {{variables}} are placeholders to substitute before running.
# Credentials were scrubbed; replace REDACTED with real values.

# --- /Users ---

# /Users/Get user
curl \
  -k \
  --path-as-is \
  '{{base_url}}/users/1?expand=roles' \
  -H 'Authorization: Bearer {{api_token}}' \
  -H 'Authorization: Bearer {{chain_1}}'

# /Users/Upload
curl \
  -k \
  --path-as-is \
  'https://example.com/upload' \
  -F 'title=x' \
  -F 'file=@FILE_PATH'

# /Users/GQL
curl \
  -k \
  --path-as-is \
  'https://example.com/graphql' \
  -H 'X-T: {% faker '\''name'\'' %} {{ _.a | upper }}' \
  -H 'Content-Type: application/json' \
  --data-raw '{"query":"{ me { id } }","variables":{"a":1}}'

# /Login
curl \
  -k \
  --path-as-is \
  '{{base_url}}/login' \
  -u 'alice:REDACTED' \
  -H 'Content-Type: application/json' \
  -H 'X-Request-Id: {{$guid}}' \
  --data-raw '{"user":"alice","pass":"{{password}}"}'
