package mongoconn

import (
	"context"
	"log/slog"
	"math/rand"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

const (
	initialReconnectDelay   = 2 * time.Second
	maxReconnectDelay       = 30 * time.Second
	connectionCheckInterval = 10 * time.Second
	disconnectTimeout       = 5 * time.Second
)

// PingFunc checks that a client can talk to the server.
type PingFunc func(context.Context, *mongo.Client) error

// Connect opens a client and waits, with exponential backoff and jitter, until
// MongoDB answers an authenticated ping. It only gives up when cfg.Ctx is
// cancelled, in which case the client is closed and the context error is
// returned: a service that cannot reach its database keeps trying instead of
// crash-looping, and logs every attempt.
func Connect(cfg Config) (*mongo.Client, error) {
	return connect(cfg, Ping, slog.Default())
}

func connect(cfg Config, ping PingFunc, logger *slog.Logger) (*mongo.Client, error) {
	ctx := cfg.context()
	opts := ClientOptions(cfg)

	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		logger.Error("Failed to create MongoDB client", "error", err)
	}

	delay := initialReconnectDelay
	for attempt := 1; ; attempt++ {
		if client != nil {
			// Ping on every attempt. A failed Ping leaves err non-nil, so using
			// err as a guard here would prevent all subsequent recovery checks.
			err = ping(ctx, client)
			if err == nil {
				logger.Info("Connected to MongoDB",
					"mongo_auth", cfg.Auth.Describe(opts),
					"max_pool_size", cfg.MaxPoolSize,
					"min_pool_size", cfg.MinPoolSize,
					"max_connecting", cfg.MaxConnecting,
				)
				return client, nil
			}
		}

		logger.Error("Failed to connect to MongoDB, retrying",
			"error", err,
			"attempt", attempt,
			"delay", delay.String(),
		)

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			if client != nil {
				disconnectCtx, cancel := context.WithTimeout(context.Background(), disconnectTimeout)
				_ = client.Disconnect(disconnectCtx)
				cancel()
			}
			return nil, ctx.Err()
		case <-timer.C:
		}

		if client == nil {
			client, err = mongo.Connect(ctx, opts)
		}

		delay = nextReconnectDelay(delay)
	}
}

func nextReconnectDelay(delay time.Duration) time.Duration {
	next := delay * 2
	if next > maxReconnectDelay {
		next = maxReconnectDelay
	}

	jitter := time.Duration(rand.Int63n(int64(next) / 2))
	return next/2 + jitter
}

// Ping checks that the server is reachable AND that the connection is allowed
// to run commands. The driver's client.Ping succeeds on a server started with
// --auth even when the client sent no credentials (the ping command is open),
// so a service would log "Connected to MongoDB" and then fail on its first real
// query. listDatabases with authorizedDatabases, instead, works for any
// authenticated user whatever its roles, works anonymously on a server
// without --auth, and is refused when the server requires authentication.
func Ping(ctx context.Context, client *mongo.Client) error {
	if client == nil {
		return mongo.ErrClientDisconnected
	}
	cmd := bson.D{
		{Key: "listDatabases", Value: 1},
		{Key: "nameOnly", Value: true},
		{Key: "authorizedDatabases", Value: true},
	}
	return client.Database("admin").RunCommand(ctx, cmd).Err()
}

// IsConnected reports whether the client can currently reach MongoDB.
func IsConnected(ctx context.Context, client *mongo.Client) bool {
	return Ping(ctx, client) == nil
}

// Monitor checks the connection every 10 seconds until ctx is cancelled and
// logs when it is lost and when it comes back. Run it in its own goroutine.
func Monitor(ctx context.Context, client *mongo.Client, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(connectionCheckInterval)
	defer ticker.Stop()

	monitor(ctx, ticker.C, func(ctx context.Context) error {
		return Ping(ctx, client)
	}, logger)
}

func monitor(ctx context.Context, ticks <-chan time.Time, ping func(context.Context) error, logger *slog.Logger) {
	disconnected := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			err := ping(ctx)
			if ctx.Err() != nil {
				return
			}

			if err != nil {
				if !disconnected {
					logger.Error("MongoDB connection lost", "error", err)
					disconnected = true
				} else {
					logger.Warn("MongoDB connection still unavailable", "error", err)
				}
				continue
			}

			if disconnected {
				logger.Info("MongoDB connection restored")
				disconnected = false
			}
		}
	}
}
