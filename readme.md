# Traefik Plugin Bulk Redirects

Traefik middleware plugin for Cloudflare-style bulk redirects.

This plugin allows defining multiple redirects in a single Traefik Middleware configuration. It supports exact path redirects, subpath redirects, query string preservation, and configurable redirect status codes.

## Features

- Exact redirects by host and path
- Subpath/prefix redirects
- Query string preservation
- Configurable redirect status codes: `301`, `302`, `303`, `307`, `308`
- Host normalization when the request host contains a port
- Pass-through behavior when no redirect matches
- Cloudflare-style `enabled` / `disabled` options

## Configuration

### Static Traefik configuration

Enable the plugin in the Traefik static configuration.

```yaml
experimental:
  plugins:
    bulkRedirects:
      moduleName: github.com/DoodleScheduling/traefik-plugin-bulk-redirects
      version: v0.1.0
```

For local development:

```yaml
experimental:
  localPlugins:
    bulkRedirects:
      moduleName: github.com/DoodleScheduling/traefik-plugin-bulk-redirects
```

## Middleware example

```yaml
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: bulk-redirects
  namespace: traefik
spec:
  plugin:
    bulkRedirects:
      redirects:
        - sourceHost: example.com
          sourcePath: /premium/coupon
          targetURL: https://example.com/en/premium/
          statusCode: 302
          preserveQueryString: enabled
          subpathMatching: disabled

        - sourceHost: example.com
          sourcePath: /docs
          targetURL: https://example.com/en/resources
          statusCode: 301
          preserveQueryString: enabled
          subpathMatching: enabled
```

## HTTPRoute example

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: web
  namespace: web
spec:
  parentRefs:
    - name: traefik-external
      namespace: traefik
      sectionName: websecure
  hostnames:
    - example.com
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      filters:
        - type: ExtensionRef
          extensionRef:
            group: traefik.io
            kind: Middleware
            name: bulk-redirects
      backendRefs:
        - name: web-frontend
          port: 80
```

## IngressRoute example

```yaml
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata:
  name: web
  namespace: web
spec:
  entryPoints:
    - websecure
  routes:
    - match: Host(`example.com`) && PathPrefix(`/`)
      kind: Rule
      middlewares:
        - name: bulk-redirects
          namespace: traefik
      services:
        - name: web-frontend
          port: 80
  tls: {}
```

## Redirect fields

| Field | Required | Description |
|---|---:|---|
| `sourceHost` | yes | Host to match, for example `example.com`. |
| `sourcePath` | yes | Path to match. Must start with `/`. |
| `targetURL` | yes | Redirect destination URL. |
| `statusCode` | no | Redirect status code. Defaults to `301`. Supported: `301`, `302`, `303`, `307`, `308`. |
| `preserveQueryString` | no | Use `enabled` to append the original query string to the target URL. |
| `subpathMatching` | no | Use `enabled` to match the source path and all subpaths below it. |

## Exact redirects

When `subpathMatching` is `disabled`, the plugin only redirects exact path matches.

Example:

```yaml
- sourceHost: example.com
  sourcePath: /premium/coupon
  targetURL: https://example.com/en/premium/
  statusCode: 302
  preserveQueryString: enabled
  subpathMatching: disabled
```

Result:

```text
https://example.com/premium/coupon?utm=test
->
https://example.com/en/premium/?utm=test
```

This does not match:

```text
/premium/coupon/
/premium/coupon/foo
```

## Subpath redirects

When `subpathMatching` is `enabled`, the plugin redirects the source path and all child paths.

Example:

```yaml
- sourceHost: example.com
  sourcePath: /docs
  targetURL: https://example.com/en/resources
  statusCode: 301
  preserveQueryString: enabled
  subpathMatching: enabled
```

Results:

```text
/docs
->
https://example.com/en/resources

/docs/api
->
https://example.com/en/resources/api

/docs/api/v1?utm=test
->
https://example.com/en/resources/api/v1?utm=test
```

The plugin avoids matching similar but unrelated paths:

```text
/docs-other
```

does not match:

```text
/docs
```

## Query string preservation

When `preserveQueryString` is `enabled`, the original query string is appended to the target URL.

Example:

```text
/premium/coupon?utm_source=google
->
https://example.com/en/premium/?utm_source=google
```

If the target URL already contains a query string, the plugin appends the original query string using `&`.

Example:

```text
targetURL: https://example.com/en/premium/?plan=pro

/premium/coupon?utm_source=google
->
https://example.com/en/premium/?plan=pro&utm_source=google
```

## Local tests

Run:

```bash
go test -v -cover ./...
```

## Development

The plugin exposes the standard Traefik plugin functions:

```go
func CreateConfig() *Config

func New(ctx context.Context, next http.Handler, config *Config, name string) (http.Handler, error)
```

If no redirect matches the incoming request, the plugin passes the request to the next handler.

## Release

Create and push a tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Then reference the tag in the Traefik static configuration:

```yaml
experimental:
  plugins:
    bulkRedirects:
      moduleName: github.com/DoodleScheduling/traefik-plugin-bulk-redirects
      version: v0.1.0
```
