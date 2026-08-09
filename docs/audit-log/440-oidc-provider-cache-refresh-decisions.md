# Slice 440 — OIDC provider cache refresh decisions

**Type:** JUDGMENT · **Date:** 2026-08-04

## D1 — Foreground TTL refresh, no background rediscovery loop

**Decision:** Cache discovered OIDC providers per issuer for 30 seconds, then
re-run discovery on the next login or callback that needs that issuer.

Slice 335's chaos design already named a 30 second OIDC discovery refresh
interval as the expected recovery bound. The existing code had no such bound:
the first successful `coreos.NewProvider` result lived for the process
lifetime. A foreground TTL makes the login path observe both IdP outage and
recovery without adding a package-level goroutine, stop channel, or scheduler
lifecycle to the authenticator.

The refresh keeps the existing concurrency discipline: the mutex protects only
the map lookup/update/delete and is released before `coreos.NewProvider` makes
the network call.

## D2 — Discovery failure invalidates the stale entry

**Decision:** When an expired entry is rediscovered and discovery fails, delete
the issuer's cache entry before returning the discovery error.

Serving the previous provider after a failed refresh would preserve the exact
failure mode this slice closes: a warm cache could hide IdP unavailability
indefinitely. Deleting the entry means subsequent logins keep attempting
discovery and resume as soon as the IdP is reachable, without requiring an
atlas restart.

## D3 — No public configuration knob yet

**Decision:** Ship `ProviderCacheTTL = 30 * time.Second` as a package constant
and do not add operator configuration in this slice.

The issue asks for the refresh behavior to exist and be bounded, not for a new
runtime config surface. Thirty seconds matches the existing chaos expectation
and is short enough to make outage/recovery observable while avoiding a network
round-trip on every login.
