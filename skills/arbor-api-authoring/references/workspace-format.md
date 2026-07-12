# Arbor workspace authoring reference

Arbor uses strict, versioned YAML. Unknown fields are errors. This reference is self-contained so an installed skill does not depend on an Arbor source checkout.

```text
.arbor/arbor.yaml
.arbor/collections/<collection>/<request>.yaml
.arbor/environments/<environment>.yaml
.arbor/scenarios/<scenario>.yaml
```

Every document uses `version: 1` and the appropriate `kind`: `request`, `collection`, `environment`, or `scenario`.

Arbor recommends storing requests under `.arbor/collections/<collection>/`; requests directly under `.arbor/collections/` belong to `default`. Use stable IDs such as `users.get`. Collections may have a `collection.yaml` marker whose name matches its directory.

```yaml
version: 1
kind: request
id: payments.create
name: Create payment
description: Create a payment.
method: POST
url: "{{base_url}}/payments"
headers:
  Authorization: "Bearer {{token}}"
body:
  amount: 1200
  currency: USD
assert:
  - status == 201
extract:
  payment_id: body.id
```

Mappings and lists are sent as JSON; string bodies are sent as `text/plain` unless `Content-Type` overrides it. `body` cannot be combined with `form` or `files`. Use `form` and `files` for uploads; paths are relative to the request file and may contain variables.

Environment example:

```yaml
version: 1
kind: environment
name: staging
variables:
  base_url: https://staging.example.com
secrets:
  token: env://API_TOKEN
  client_id: keychain://arbor-acme/staging-client-id
```

Secret values must never be written to files. Variables use `{{name}}` and precedence is workspace, environment, scenario, extracted values, then `--var`. Unresolved variables stop execution.

Assertions use selectors such as `status`, `durationMs`, `headers.Content-Type`, and `body.id`, with operators `==`, `!=`, comparisons, and `contains`. Extraction maps names to selectors, for example `token: body.access_token`.

Scenario files live under `scenarios/`; each step requires a request reference and may add `assert` and `extract`. Step-level extraction wins when names overlap.
