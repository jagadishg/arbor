# Workspace format

Arbor workspace files use strict YAML. Unknown fields are errors, and every file has a schema version. Paths shown below are relative to the directory containing `arbor.yaml`.

Every resource accepts an optional `description` field. It is never sent over the wire; it exists so both people and coding agents can understand a workspace without reverse-engineering it. Descriptions appear in `arbor describe`, `arbor list`, and the interactive describe view.

## Workspace

`arbor.yaml`:

```yaml
version: 1
name: Payments API
description: Internal payments service API.
defaultEnvironment: local

variables:
  api_version: v1

http:
  timeout: 30s
  followRedirects: true
  insecureTLS: false
```

`insecureTLS` disables certificate verification and should only be used for local development.

## Requests

Request files may be nested anywhere under `collections/` and must use `kind: request`.

```yaml
version: 1
kind: request
id: payments.create
name: Create payment
method: POST
url: "{{base_url}}/payments"
timeout: 10s

headers:
  Authorization: "Bearer {{token}}"

query:
  expand: customer

body:
  amount: 1200
  currency: USD
  reference: "{{reference}}"

assert:
  - status == 201
  - body.id != null

extract:
  payment_id: body.id
```

String bodies are sent as `text/plain`; mappings and lists are encoded as JSON. An explicit `Content-Type` header overrides Arbor's inferred value.

`id` is the stable reference used by commands and scenarios. When omitted, `name` is used, but explicit IDs are strongly recommended.

## Collections

A collection is the folder a request lives in under `collections/`. A request in
`collections/payments/create.yaml` belongs to the `payments` collection; requests placed
directly in `collections/` belong to `default`. Collections are how the interactive browser
groups requests — `:collections` lists them and `Enter` drills into a collection's requests.

Add an optional `collection.yaml` marker to give a folder a description:

```yaml
version: 1
kind: collection
name: payments
description: Create, capture, and refund payments.
```

`arbor new collection <name>` scaffolds one. The marker's `name` should match its folder.

## File uploads

A request can send `multipart/form-data` instead of a JSON/text `body`, using `form` for text
fields and `files` for file parts (field name → path):

```yaml
version: 1
kind: request
id: avatars.upload
name: Upload avatar
method: POST
url: "{{base_url}}/users/{{user_id}}/avatar"

form:
  caption: "Profile photo"

files:
  avatar: ./files/me.png
```

- File paths are relative to the request's own `.yaml` file (absolute paths are used as-is);
  `{{variables}}` work in both `form` values and `files` paths.
- `form` on its own (no `files`) is sent as `application/x-www-form-urlencoded`.
- `form`/`files` and `body` are mutually exclusive.
- In the TUI, `:attach <field>=<path>` adds a file entry to the selected request (it rewrites
  the request file, normalising formatting).

## Environments

Environment files live under `environments/` and use `kind: environment`.

```yaml
version: 1
kind: environment
name: production

variables:
  base_url: https://api.example.com

secrets:
  token: keychain://arbor-example/production-token
  client_id: env://EXAMPLE_CLIENT_ID
```

Supported secret references:

- `env://NAME` reads an environment variable at execution time.
- `keychain://service/account` reads the native macOS Keychain, Windows Credential Manager, or Linux Secret Service.

## Variables

Variables use `{{name}}` syntax in URLs, headers, query values, string bodies, and nested JSON body strings.

Precedence from lowest to highest is:

1. workspace variables
2. environment variables and secrets
3. scenario variables
4. values extracted by previous scenario steps
5. command-line `--var key=value` values

An unresolved variable stops execution and reports its name.

## Assertions

Assertions have the form `<selector> <operator> <literal>`.

Selectors:

- `status` or `statusCode`
- `statusText`
- `duration` or `durationMs`
- `size`
- `headers.<name>`
- `body`
- `body.<json.path>` including array indexes such as `body.users[0].id`

Operators:

- `==`, `!=`
- `>`, `<`, `>=`, `<=` for numeric values
- `contains` for textual containment

Examples:

```yaml
assert:
  - status == 200
  - durationMs < 500
  - headers.Content-Type contains "json"
  - body.active == true
  - body.id == "{{expected_id}}"
```

Right-hand values accept JSON literals, quoted strings, plain strings, and variables.

## Extraction

Extraction uses the same response selectors:

```yaml
extract:
  token: body.access_token
  request_id: headers.X-Request-Id
```

Extracted values are strings and become available to later scenario steps.

## Scenarios

Scenario files live under `scenarios/` and use `kind: scenario`.

```yaml
version: 1
kind: scenario
id: checkout.happy-path
name: Checkout happy path
continueOnFailure: false

variables:
  quantity: "2"

steps:
  - name: Authenticate
    request: auth.login
    extract:
      token: body.access_token

  - name: Create order
    request: orders.create
    assert:
      - status == 201
    extract:
      order_id: body.id

  - name: Fetch order
    request: orders.get
    assert:
      - body.id == "{{order_id}}"
```

Request-level assertions and extraction run together with step-level definitions. Step-level extraction wins when the same variable name is defined in both places.
