// Package mongoconn is the one place where the Oktopus services agree on how
// to reach MongoDB: which environment variables configure the client, how the
// credentials are applied, how a service waits for the database at boot, and
// how it watches the connection afterwards.
//
// Import path: github.com/OktopUSP/mongoconn
//
// A service does three things with it:
//
//	cfg := mongoconn.ConfigFromEnv(ctx, "CONTROLLER", mongoconn.StandardDefaults)
//	client, err := mongoconn.Connect(cfg)   // blocks, with backoff, until MongoDB answers
//	go mongoconn.Monitor(ctx, client, slog.Default())
//
// Environment:
//
//   - MONGO_URI, MONGO_USER, MONGO_PASSWORD, MONGO_AUTH_SOURCE are shared by
//     every service (see Credentials).
//   - <PREFIX>_MONGO_MAX_POOL_SIZE, <PREFIX>_MONGO_MIN_POOL_SIZE,
//     <PREFIX>_MONGO_MAX_CONNECTING, <PREFIX>_MONGO_CONNECT_TIMEOUT_SECONDS and
//     <PREFIX>_MONGO_SERVER_SELECTION_TIMEOUT_SECONDS tune one service's pool
//     and keep the names each service already used.
package mongoconn

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/mongo/options"
)

// Config is everything needed to open a MongoDB client.
type Config struct {
	Uri  string
	Auth Credentials
	// Ctx bounds the connection attempts and the monitor; cancelling it is
	// how a service shuts the connection down.
	Ctx                    context.Context
	MaxPoolSize            uint64
	MinPoolSize            uint64
	MaxConnecting          uint64
	ConnectTimeout         time.Duration
	ServerSelectionTimeout time.Duration
}

// Defaults are the pool settings used when the environment does not override
// them.
type Defaults struct {
	MaxPoolSize            uint64
	MinPoolSize            uint64
	MaxConnecting          uint64
	ConnectTimeout         time.Duration
	ServerSelectionTimeout time.Duration
}

// StandardDefaults is what most services run with.
var StandardDefaults = Defaults{
	MaxPoolSize:            50,
	MinPoolSize:            0,
	MaxConnecting:          2,
	ConnectTimeout:         10 * time.Second,
	ServerSelectionTimeout: 10 * time.Second,
}

// ConfigFromEnv builds a Config from the process environment. prefix is the
// service's own prefix for the pool variables ("CONTROLLER" reads
// CONTROLLER_MONGO_MAX_POOL_SIZE and so on); the connection string and the
// credentials have no prefix.
func ConfigFromEnv(ctx context.Context, prefix string, d Defaults) Config {
	return configFromLookup(ctx, prefix, d, os.LookupEnv)
}

func configFromLookup(ctx context.Context, prefix string, d Defaults, lookup func(string) (string, bool)) Config {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "_")
	name := func(suffix string) string {
		if prefix == "" {
			return "MONGO_" + suffix
		}
		return prefix + "_MONGO_" + suffix
	}
	str := func(key, def string) string {
		if v, ok := lookup(key); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return def
	}
	u64 := func(key string, def uint64) uint64 {
		if v, ok := lookup(key); ok {
			if n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64); err == nil {
				return n
			}
		}
		return def
	}
	seconds := func(key string, def time.Duration) time.Duration {
		if v, ok := lookup(key); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				return time.Duration(n) * time.Second
			}
		}
		return def
	}

	return Config{
		Uri:                    str(EnvURI, DefaultURI),
		Auth:                   credentialsFromLookup(lookup),
		Ctx:                    ctx,
		MaxPoolSize:            u64(name("MAX_POOL_SIZE"), d.MaxPoolSize),
		MinPoolSize:            u64(name("MIN_POOL_SIZE"), d.MinPoolSize),
		MaxConnecting:          u64(name("MAX_CONNECTING"), d.MaxConnecting),
		ConnectTimeout:         seconds(name("CONNECT_TIMEOUT_SECONDS"), d.ConnectTimeout),
		ServerSelectionTimeout: seconds(name("SERVER_SELECTION_TIMEOUT_SECONDS"), d.ServerSelectionTimeout),
	}
}

// ClientOptions turns the Config into driver options, credentials included.
func ClientOptions(c Config) *options.ClientOptions {
	opts := options.Client().
		ApplyURI(c.Uri).
		SetMaxPoolSize(c.MaxPoolSize).
		SetMinPoolSize(c.MinPoolSize).
		SetMaxConnecting(c.MaxConnecting).
		SetConnectTimeout(c.ConnectTimeout).
		SetServerSelectionTimeout(c.ServerSelectionTimeout)
	return c.Auth.Apply(opts)
}

func (c Config) context() context.Context {
	if c.Ctx == nil {
		return context.Background()
	}
	return c.Ctx
}
