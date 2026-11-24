## Hetzner client libraries

Versions checked: hcloud CLI v1.70.2 and terraform-provider-hcloud 1.70.2 both pin hcloud-go v2.53.0 (go.mod of each tag). hcloud-python 2.27.0 is the pinned version. Not looked up: the date of the latest release of each (maintenance status unconfirmed, all three are active in the pinned tags).

### hcloud-go v2.53.0 (hcloud CLI, OpenTofu provider)

Source: https://raw.githubusercontent.com/hetznercloud/hcloud-go/v2.53.0/hcloud/client_handler_retry.go and client.go

- The retry decision depends on the API error code in the JSON body, not on the HTTP status. Retried:
  - API error body with code `conflict`, `rate_limit_exceeded`, `bad_gateway` or `timeout`, at any HTTP status.
  - A response with HTTP status 502 or 504 and no usable JSON error body (`ErrStatusCode`).
  - Network errors that are timeouts.
- Not retried: `locked`, `resource_locked`, `service_error`, `server_error`, `unknown_error`, `maintenance`, `resource_unavailable`, `invalid_input`, `forbidden`. A 500 or 503 is never retried, with or without a JSON body. Any other 4xx fails at once. `locked` is not retried by the client.
- Backoff: `ExponentialBackoffWithOpts{Base: 1s, Multiplier: 2, Cap: 1min, Jitter: true}`. The wait is uniform between 1s and min(60s, 2^n s). The delay for retry n (n=0..4) is at most 1, 2, 4, 8, 16 s.
- `retryMaxRetries` is 5: 1 initial attempt plus 5 retries, 6 requests in all. The longest total wait is about 31 s. After that the last error is returned. The retry loop sits per request, so every request of a command retries on its own.
- `Retry-After` is not read. The `RateLimit-Limit`, `RateLimit-Remaining` and `RateLimit-Reset` headers are only parsed into `Response.Meta.Ratelimit` for information. They do not control any wait.
- Error parsing (client_handler_error.go): a 4xx/5xx with a JSON body that has `error.code` or `error.message` becomes `hcloud.Error` with that code. Otherwise it becomes `ErrStatusCode` (matched only on 502/504 for retry).
- hlab consequence: for a 429 with `{"error":{"code":"rate_limit_exceeded",...}}`, hcloud retries for about 5 to 31 s (6 requests) and then fails with `rate limit exceeded (rate_limit_exceeded)`. If the hlab rate limiter counts the retried requests, they also count against the 10/hour.

### hcloud CLI v1.70.2

Source: internal/state (client construction), internal/cmd/server/create.go, list.go, base/list.go in https://github.com/hetznercloud/cli/tree/v1.70.2

- It sets no retry option, so the hcloud-go defaults above apply (5 retries, exponential backoff with jitter).
- Action polling: constant 500 ms for the first 10 polls, then exponential x1.5 up to a cap of 2.5 s (`customPollBackoffFunc`). `--poll-interval` or the config option switches to a constant interval. The wait comes before the first poll.
- Poll request: one `GET /actions?id=<id>&id=<id>&sort=status&sort=id` per poll for up to 25 running actions (hcloud-go `ActionClient.WaitForFunc`, `AllWithOpts`). A poll fewer actions than requested gives an "actions not found" error.
- `hcloud server create --name x --type T --image I` (no location, ssh key or network flags):
  1. `GET /server_types?name=T`
  2. `GET /images?name=I&architecture=...` (more than one request is possible, the internals of `GetForArchitecture` were not read)
  3. `POST /servers` (the only server write; also the only request affected by a "server write" fault)
  4. Polling `GET /actions?...` for the create action and the `start_server` action, as many times as the mock actions need. Rough count: duration/0.5 s for the first 5 s, then one per 1 to 2.5 s.
  5. `GET /servers/{id}`
  So about 4 or 5 requests plus polls. With an action that finishes at once, 1 to 2 polls.
- `hcloud server list` (no flags): one `GET /servers?per_page=50` per page. `AllWithOpts` pages on `meta.pagination`, so a response with `last_page` 1 or no next page ends it after one request. One extra `GET /networks` is possible only if a listed server has private networks (the `private_net` column; not confirmed in the source read). No action polling.
- Checks for the success criteria:
  - Criterion 2 (api_error fault of 50% on server writes): fault code `conflict`, `rate_limit_exceeded`, `bad_gateway` or `timeout` gets retried by hcloud. Success then shows only as extra POST requests in the hlab log, and the chance that all 6 attempts fail is 0.5^6, about 1.6%. A code that the client does not retry (`service_error`, `server_error`, `unknown_error`, `locked`, `resource_unavailable`; for example HTTP 500 `service_error`) makes the failure visible on every injected fault with one invocation, so about half of all `hcloud server create` runs fail. Use a non-retried code in the guide's example if the example must show a failure; use a retried code (`conflict`) to show that hcloud recovers.
  - Criterion 3 (rate_limit of 10/hour, eleventh call gets `rate_limit_exceeded`): `hcloud server list` makes one request per call, so the eleventh call can be checked with ten `server list` runs and an eleventh. That eleventh run takes up to about 31 s (retries) before it fails. For a quick check use `hcloud api GET /servers`? (not confirmed that `hcloud api` shares the retry client; it uses the same state client, so assume the same retries) or plain curl, where the 429 is returned at once. `hcloud server create` is a bad probe because it makes about 5 or more requests per run, and the limit has to say which requests it counts (all requests, writes only, per token).
  - A 429 with code `rate_limit_exceeded` is the right choice for a `rate_limit` fault. The HTTP status for it is 429 on the real API.

### hcloud-python 2.27.0 (Ansible collection hetzner.hcloud 7.2.0)

Source: https://raw.githubusercontent.com/hetznercloud/hcloud-python/v2.27.0/hcloud/_client.py, actions/client.py

- Same retry policy as hcloud-go: retried on API error codes `conflict`, `rate_limit_exceeded`, `bad_gateway`, `timeout`, on HTTP 502 and 504, and on `requests` timeouts. Backoff `exponential_backoff_function(base=1.0, multiplier=2, cap=60.0, jitter=True)`; max 5 retries (`_retry_max_retries`, not configurable through the `Client` constructor). `Retry-After` and `RateLimit-*` headers are not read (only `X-Correlation-Id`).
- Other 4xx/5xx raise `APIException` with the code from the body at once.
- Action polling: `Action.wait_until_finished` reloads the action (one `GET /actions/{id}` per poll, not confirmed from `reload()` source, which was not read), default `poll_interval` 1.0 s constant, `poll_max_retries` 120 (about 2 min), then `ActionTimeoutException`. A failed action raises `ActionFailedException`. Default request timeout is none (no timeout).
- Ansible hetzner.hcloud 7.2.0 modules call the library and turn `APIException` into a module failure (not read in detail; unconfirmed).

### OpenTofu provider hetznercloud/hcloud 1.70.2

Sources: https://raw.githubusercontent.com/hetznercloud/terraform-provider-hcloud/v1.70.2/hcloud/provider.go and docs/index.md

- It builds the client with token, application name, endpoint, poll options and optional debug writer; no retry option, so it uses the hcloud-go v2.53.0 defaults: 5 retries, exponential backoff with jitter, same codes and statuses (`conflict`, `rate_limit_exceeded`, `bad_gateway`, `timeout`, 502, 504).
- Provider arguments: `endpoint` (default `https://api.hetzner.cloud/v1`; env var not confirmed here), `poll_interval` (default `500ms`; docs say to raise it on rate limit errors), `poll_function` (`constant` or `exponential`, default `exponential` with base 2 times the interval). Action polling is therefore 0.5, 1, 2, 4, ... s by default, so far fewer polls than the CLI per long action. Poll request: one `GET /actions` per poll (hcloud-go waiter).
- Terraform/OpenTofu `apply` wraps the final error in a resource diagnostic with the API message; a non-retried 5xx fails the resource, and the next `apply` retries it. A tofu resource create is `POST` plus waits plus reads (`GET` after create). Count of requests per resource was not confirmed.

### Not confirmed

- Request count for the image lookup in `server create` (internals of `GetForArchitecture`) and whether `hcloud` v1.70.2 sends extra requests (for example deprecation lookups).
- Whether `hcloud server list` makes extra `GET /networks` calls.
- Python `reload()` request type and the Ansible module error mapping.
- Release dates / maintenance status of the three projects (not queried).
## MCP go-sdk

Basis: source reading of the module cache copies of go-sdk v1.8.0 and jsonschema-go v0.4.3 (the versions in go.mod). No Bash tool was available, so none of this was run. It is derived from the code paths, not observed. Verify with a small in-memory client/server test before relying on the edge cases marked "derived".

Versions: go.mod already pins go-sdk v1.8.0 and jsonschema-go v0.4.3. I did not check whether newer releases exist, so "current stable" is unconfirmed.

### Inferred input schema (jsonschema-go v0.4.3 `forType`, called from `mcp.AddTool` via `setSchema`)
Source: `jsonschema/infer.go`, `mcp/server.go` (`toolForErr`, `setSchema`).
- The struct becomes `type: object` with `additionalProperties: false`. Unknown keys are rejected, at the top level and inside nested structs.
- Every field without `omitempty` or `omitzero` is in `required`. Optional fields need `json:"x,omitempty"`.
- Nested struct `target Target` gives an object schema with its own `properties`, `required` and `additionalProperties: false`. It is required unless the field has `omitempty`.
- `map[string]any` (`params`) gives `{type: object, additionalProperties: {}}`, so any keys and values are allowed. A non-pointer map does not allow `null`.
- `*uint64` (`seed`) gives `{type: ["null","integer"], minimum: 0}`. There is no maximum. A uint64 has no upper bound in the schema.
- A plain `string` gives `{type: string}`. `*string` gives `{type: ["null","string"]}`.
- A slice gives `type: ["null","array"]`. This is the default in 0.4.3.
- The `jsonschema:"text"` tag becomes the description. The tag must not start with `WORD=`.
- Pointers allow `null`. A non-pointer field does not.

### Validation and rejection (`mcp/tool.go` `applySchema`)
- The SDK validates the arguments before the handler runs.
- A validation failure is not a protocol error. The call returns a `CallToolResult` with `IsError=true` and the text `validating "arguments": ...`.
- Arguments are first decoded into `map[string]any`. Numbers therefore become `float64`.
- Validation uses `jsonType`. A float64 with no fraction counts as `integer`, so seed values above 2^53 and up to 2^64 pass validation. `minimum: 0` rejects `-1`. `1.5` is rejected as a number, not an integer.
- Because defaults are applied to a map, the input is always re-marshalled with `encoding/json` from the float64 values before it is unmarshalled into the struct. This is derived from the code and not run.
  - Seeds up to 2^53 are exact.
  - Seeds above 2^53 that are not exactly representable as float64 are silently rounded. For example `9007199254740993` becomes `9007199254740992`.
  - `18446744073709551615` becomes the float 1.8446744073709552e19. It is re-marshalled as `18446744073709552000`, which overflows uint64. The call then returns an `IsError` result from the unmarshal, not the handler.
  - The same float64 round trip applies to large integers inside `params`.
- Recommendation: do not take a full 64-bit seed as a JSON number. Either declare `seed` as a string and parse it, or accept values only up to 2^53-1 and document the limit. Alternatively register the tool with the low-level `Server.AddTool`, which gets raw `Arguments` and does no validation, and decode with `json.Decoder.UseNumber`.
- `null` handling (derived from the schema):
  - `seed: null` is accepted. `null` for a `*string` is accepted.
  - `null` for `params` (non-pointer map), `target` (non-pointer struct) or a plain `string` is rejected with `type: null has type "null", want ...`.
  - An omitted optional field is accepted.
- No default values are inferred. Defaults would only come from a hand-written schema.

### Omitted versus zero
- Pointer fields (`*uint64`, `*string`) are `nil` when omitted and non-nil when the client sends `0` or `""`. The two can be told apart.
- An explicit `null` also gives `nil`, so `null` and omitted look the same.
- Non-pointer fields (`string`, `map`) cannot tell omitted from zero. A map is `nil` when omitted.
- Unmarshalling into the struct uses the SDK's internal segmentio-based decoder. It matches field names case-sensitively and has a nesting depth limit of 1000.

### Pitfalls
- Output: the `Out` type gets an output schema, and the handler's output is validated against it. A typed `nil` pointer is replaced by the zero value.
- `AddTool` panics if schema inference fails, for example with a cycle in the types, a non-string map key or an `int` map key.
- The `In` type must infer to `type: object`. Use a struct or a map.
- Maintenance: the repository is active. I did not confirm the last release date.

Sources (files read, local module cache):
- github.com/modelcontextprotocol/go-sdk@v1.8.0/mcp/server.go
- github.com/modelcontextprotocol/go-sdk@v1.8.0/mcp/tool.go
- github.com/modelcontextprotocol/go-sdk@v1.8.0/internal/json/json.go
- github.com/google/jsonschema-go@v0.4.3/jsonschema/infer.go
- github.com/google/jsonschema-go@v0.4.3/jsonschema/validate.go
- github.com/google/jsonschema-go@v0.4.3/jsonschema/util.go
- Upstream repositories: https://github.com/modelcontextprotocol/go-sdk and https://github.com/google/jsonschema-go
