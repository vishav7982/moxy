# Proposed Changes for Review

This note is meant to help the repository author review the work in this branch quickly. It focuses on what changed, why the change was made, what value it adds for Go developers, and where there may still be follow-up work.

## Summary

Before these changes, moxy was mainly a Go test helper built around `httptest.Server`. It already handled HTTP, HTTPS, mTLS, request matching, sequential responses, delays, timeouts, and unmatched request tracking.

The work in this branch keeps that model intact, but extends it so moxy can also act like a small standalone mock server with file-based configuration and runtime control.

The main additions are:

- a standalone `moxy` binary
- native JSON mappings
- runtime admin endpoints under `/__moxy`
- a full request journal
- richer request matching
- dynamic response templates
- a few public API cleanups
- stronger test coverage around the new surfaces

## 1. Standalone Binary

What changed:

- Added `cmd/moxy/main.go`
- Added `cmd/moxy/main_test.go`
- Added CLI flags:
  - `--host`
  - `--port`
  - `--https`
  - `--mappings`
  - `--verbose`

Why this was added:

- Earlier, moxy could only be started from Go code.
- That worked well in tests, but made it harder to use from CI scripts, local tooling, or non-Go callers.

How this helps:

- A Go team can keep using `NewMockServer()` in tests.
- The same team can also run a real mock server process during local development or CI.
- The standalone binary reuses the same core engine as the library, so behavior stays aligned.

Reviewer notes:

- `main.go` was refactored slightly so the CLI flow can be tested directly.
- The new `run(...)` and `serve(...)` helpers exist mainly to keep the CLI testable and avoid depending on `log.Fatal` in tests.

## 2. Native JSON Mappings

What changed:

- Added `mappings.go`
- Added mapping types:
  - `Mapping`
  - `MappingRequest`
  - `MappingResponse`
  - `BasicAuth`
- Added:
  - `LoadMappings(path string) error`
  - `AddMapping(mapping Mapping) error`
  - `Mapping.ToExpectation()`

Supported request fields:

- `method`
- `path`
- `pathPattern`
- `headers`
- `headerMatches`
- `query`
- `queryMatches`
- `cookies`
- `basicAuth`
- `body`
- `jsonBody`
- `bodyContains`
- `jsonFields`

Supported response fields:

- `status`
- `headers`
- `headerTemplates`
- `body`
- `bodyFile`
- `bodyTemplate`
- `delay`
- `timeout`

Also supported:

- `responses` for sequential behavior
- `times` for call limits
- `priority` for match order

Why this was added:

- Earlier, all expectations had to be defined in Go code.
- That made sharing mock behavior harder and limited the standalone server story.

How this helps:

- Mappings can now live in JSON fixtures.
- Teams can load the same mappings in tests, CI, or local development.
- Large response payloads can be stored in files instead of inline Go strings.

Reviewer notes:

- JSON mappings are intentionally moxy-native rather than trying to match another tool's schema.
- `bodyFile` is resolved relative to the mapping file directory when mappings are loaded from disk, which makes fixture folders easier to move around.

## 3. Admin API

What changed:

- Added runtime endpoints under `/__moxy`:
  - `GET /__moxy/health`
  - `GET /__moxy/mappings`
  - `POST /__moxy/mappings`
  - `DELETE /__moxy/mappings`
  - `GET /__moxy/requests`
  - `DELETE /__moxy/requests`
  - `POST /__moxy/reset`

Why this was added:

- A standalone mock server is much more useful if mappings and request history can be inspected or reset without restarting the process.

How this helps:

- Tests and setup scripts can control moxy over HTTP.
- Local debugging becomes easier because the server can reveal what it received.
- Health checks make it simpler to use moxy in automation.

Reviewer notes:

- The admin API is intentionally small and moxy-specific.
- It is not trying to be a compatibility layer for another admin API.

## 4. Request Journal

What changed:

- Added:
  - `RequestRecord`
  - `RequestFilter`
  - `GetRequests()`
  - `ClearRequests()`
  - `FindRequests(...)`
  - `AllRequests()`

Why this was added:

- Earlier, moxy only tracked unmatched requests.
- That was useful for debugging failures, but not for asserting on successful traffic.

How this helps:

- Tests can verify how many times an endpoint was called.
- Headers, body, and query params can be inspected after the fact.
- Retry and polling behavior are easier to validate.

Reviewer notes:

- Unmatched request tracking is still there.
- The broader request journal is additive, but it does change internal bookkeeping.

## 5. Better Request Matching

What changed:

- Added DSL methods:
  - `WithQueryParamMatching(...)`
  - `WithHeaderMatching(...)`
  - `WithCookie(...)`
  - `WithCookieMatching(...)`
  - `WithBasicAuth(...)`
  - `WithRequestJSONField(...)`
  - `WithPriority(...)`
- Added `ValueMatcher`

Why this was added:

- The earlier matcher handled the common basics well, but dynamic values still pushed users toward custom matchers.

How this helps:

- Regex-based matching is useful for auth tokens, trace IDs, and generated values.
- Cookie and basic auth matching make session/auth flows easier to test.
- Simple dot-path JSON matching gives field-level assertions without requiring the whole body to match exactly.
- Priority gives predictable behavior when expectations overlap.

Reviewer notes:

- This is one area worth reviewing for backward-compatibility assumptions.
- If any existing users depend on pure insertion-order matching for overlapping expectations, `Priority` may change that behavior.

## 6. Dynamic Responses

What changed:

- Added:
  - `AndRespondWithTemplate(...)`
  - `WithResponseHeaderTemplate(...)`
  - `AndRespondWithFunc(...)`
- Added Go `text/template` rendering for response bodies and selected headers

Template data includes:

- method
- path
- path variables
- query values
- headers
- cookies
- raw body
- parsed JSON body

Why this was added:

- Earlier, responses were mostly static.
- Sequential responses helped with state-like flows, but could not easily adapt their body to the request.

How this helps:

- A single mapping can return different IDs or trace values based on the incoming request.
- Response headers can echo request-derived values when needed.
- Go users still have the custom function hook for more advanced behavior.

Reviewer notes:

- Template parse/render failures currently log and return a server error, which is covered by tests.
- Invalid header templates are skipped and logged rather than breaking the whole response path.

## 7. Public API Cleanup

What changed:

- Added:
  - `Client()`
  - `MTLSClient(...)`
  - `ExpectationCallCount(...)`
  - `NewMockServerEngine(...)`
  - `ServerTLSConfig()`
  - `Handler()`

Why this was added:

- Some docs and examples were drifting from the actual API.
- The standalone binary also needed a way to reuse the server engine without always starting `httptest.Server`.

How this helps:

- The public API now matches common usage more cleanly.
- mTLS helpers are usable from outside the package.
- Advanced users can embed or host the moxy handler themselves.

Reviewer notes:

- This is mostly additive.
- The only thing worth a quick compatibility look is whether any behavior tied to `ClearExpectations()` or match ordering should be called out more explicitly before release.

## 8. Testing and Coverage

What changed:

- Expanded `mappings_test.go`
- Added `cmd/moxy/main_test.go`

Newly covered areas include:

- mapping validation and error cases
- admin API branches
- request journal helpers
- template success and failure paths
- function responders
- CLI flag parsing
- CLI mapping load failures
- CLI HTTP and HTTPS serve paths

Verification used:

```bash
go test ./... -race -coverprofile=coverage.out -v
go build -o /tmp/moxy-test-build ./cmd/moxy
```

Current result:

- library package coverage: `89.2%`
- CLI package coverage: `86.1%`
- total statement coverage: `89.0%`

Reviewer notes:

- The test output includes some expected noisy logs.
- Those logs come from tests that intentionally exercise unmatched requests, TLS handshake failures, bad templates, and invalid CLI flags.
- `--help` still follows standard Go `flag` behavior and reports `flag: help requested`.

## Backward-Compatibility Notes

Most earlier functionality is still supported:

- `NewMockServer()` and `NewMockServerWithConfig(...)`
- HTTP, HTTPS, and mTLS support
- the existing expectation DSL
- path, query, header, and body matching
- sequential responses
- delays and timeouts
- unmatched request tracking
- `DefaultClient()`

The main areas an author may want to review carefully are:

- expectation ordering now that `Priority` exists
- whether `ClearExpectations()` clearing loaded mappings is the right long-term behavior
- whether the standalone admin API surface is the right minimum shape for a first release

## Deferred Work

These are intentionally not included in this branch:

- proxy mode
- record/playback
- scenario/state-machine support
- full JSONPath support
- XML/XPath support
- multipart matching
- webhooks
- advanced near-miss diagnostics

The branch is aimed at improving moxy for practical Go service testing without making the project much heavier all at once.
