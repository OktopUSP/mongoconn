# mongoconn

Shared MongoDB connection module for the Oktopus services: one place that
decides which environment variables configure the client, how credentials are
applied, how a service waits for the database at boot and how it watches the
connection afterwards.

```go
import "github.com/OktopUSP/mongoconn"

cfg := mongoconn.ConfigFromEnv(ctx, "CONTROLLER", mongoconn.StandardDefaults)
client, err := mongoconn.Connect(cfg) // blocks, with backoff, until MongoDB answers an authenticated ping
if err != nil {
	// Connect only fails when ctx ends, i.e. the service is shutting down.
}
go mongoconn.Monitor(ctx, client, slog.Default())
```

## Environment

| Variable | Default | Notes |
|---|---|---|
| `MONGO_URI` | `mongodb://localhost:27017` | Connection string. Credentials embedded in it (`user:password@`) take precedence over the variables below. |
| `MONGO_PASSWORD` | *(empty)* | The switch: while empty the client connects **without authentication**. |
| `MONGO_USER` | `oktopus` | Only used when `MONGO_PASSWORD` is set. |
| `MONGO_AUTH_SOURCE` | `admin` | Database the user was created in. Only used when `MONGO_PASSWORD` is set. |
| `<PREFIX>_MONGO_MAX_POOL_SIZE` | per service (`StandardDefaults`: 50) | |
| `<PREFIX>_MONGO_MIN_POOL_SIZE` | 0 | |
| `<PREFIX>_MONGO_MAX_CONNECTING` | 2 | |
| `<PREFIX>_MONGO_CONNECT_TIMEOUT_SECONDS` | 10 | |
| `<PREFIX>_MONGO_SERVER_SELECTION_TIMEOUT_SECONDS` | 10 | |

`<PREFIX>` is the argument passed to `ConfigFromEnv`, so each service keeps its
own pool knobs while the URI and the credentials are shared.

## Why the ping is not `client.Ping`

A `mongod` started with `--auth` answers the `ping` command even to an
anonymous client, so a service missing its password would log "connected" and
fail on its first real query. `mongoconn.Ping` runs `listDatabases` with
`authorizedDatabases: true` instead: it works anonymously on a server without
`--auth`, works for any authenticated user whatever its roles, and is refused
when the server requires authentication.

## Behaviour

- `Connect` retries with exponential backoff and jitter (2 s → 30 s) until the
  context is cancelled; it never gives up on its own.
- `Monitor` checks the connection every 10 s and logs `MongoDB connection lost`
  / `MongoDB connection restored`.
- The "Connected to MongoDB" log line reports `mongo_auth=disabled` or
  `enabled (user=…, authSource=…, source=env|uri)` plus the pool sizes. The
  password is never logged.
- No default password exists in the code.

## Releasing

Tag the commit with a semantic version (`git tag v1.0.1 && git push --tags`);
consumers bump the version in their `go.mod`. A breaking change needs a new
major version and the `/v2` suffix in the module path.

## License

Apache-2.0, see [LICENSE](LICENSE).
