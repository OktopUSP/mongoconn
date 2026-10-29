package mongoconn

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
}

/* ------------------------------ credentials ------------------------------ */

func TestCredentialsWithoutPasswordAreDisabled(t *testing.T) {
	cases := map[string]map[string]string{
		"nothing set":       {},
		"only user":         {EnvUsername: "someone"},
		"only auth source":  {EnvAuthSource: "other"},
		"blank password":    {EnvPassword: "   "},
		"user without pass": {EnvUsername: "someone", EnvAuthSource: "other"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			c := credentialsFromLookup(lookupFrom(env))
			if c.Enabled() || c != (Credentials{}) {
				t.Fatalf("expected disabled zero credentials, got %+v", c)
			}
			if c.String() != "disabled" {
				t.Fatalf("unexpected String(): %q", c.String())
			}
		})
	}
}

func TestCredentialsDefaults(t *testing.T) {
	c := credentialsFromLookup(lookupFrom(map[string]string{EnvPassword: "s3cret"}))
	if !c.Enabled() || c.Username != DefaultUsername || c.AuthSource != DefaultAuthSource || c.Password != "s3cret" {
		t.Fatalf("unexpected credentials %+v", c)
	}
	c = credentialsFromLookup(lookupFrom(map[string]string{EnvUsername: " app ", EnvPassword: "s3cret", EnvAuthSource: " oktopus "}))
	if c.Username != "app" || c.AuthSource != "oktopus" {
		t.Fatalf("values must be trimmed, got %+v", c)
	}
}

func TestApply(t *testing.T) {
	opts := options.Client().ApplyURI("mongodb://localhost:27017")
	if got := (Credentials{}).Apply(opts); got != opts || opts.Auth != nil {
		t.Fatal("disabled credentials must leave the options untouched")
	}

	c := Credentials{Username: "oktopus", Password: "s3cret", AuthSource: "admin"}
	c.Apply(opts)
	if opts.Auth == nil || opts.Auth.Username != "oktopus" || opts.Auth.Password != "s3cret" || opts.Auth.AuthSource != "admin" || opts.Auth.AuthMechanism != "" {
		t.Fatalf("unexpected auth %+v", opts.Auth)
	}

	fromURI := options.Client().ApplyURI("mongodb://fromuri:uripass@mongo:27017/?authSource=uridb")
	c.Apply(fromURI)
	if fromURI.Auth.Username != "fromuri" || fromURI.Auth.Password != "uripass" || fromURI.Auth.AuthSource != "uridb" {
		t.Fatalf("URI credentials must win, got %+v", *fromURI.Auth)
	}

	if got := c.Apply(nil); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestDescribe(t *testing.T) {
	env := Credentials{Username: "oktopus", Password: "s3cret", AuthSource: "admin"}

	if got := env.Describe(nil); got != "disabled" {
		t.Fatalf("nil opts: %q", got)
	}
	if got := env.Describe(options.Client().ApplyURI("mongodb://mongo:27017")); got != "disabled" {
		t.Fatalf("no auth: %q", got)
	}
	fromEnv := env.Apply(options.Client().ApplyURI("mongodb://mongo:27017"))
	if got := env.Describe(fromEnv); got != "enabled (user=oktopus, authSource=admin, source=env)" {
		t.Fatalf("env: %q", got)
	}
	fromURI := env.Apply(options.Client().ApplyURI("mongodb://other:pw@mongo:27017/?authSource=db1"))
	if got := env.Describe(fromURI); got != "enabled (user=other, authSource=db1, source=uri)" {
		t.Fatalf("uri: %q", got)
	}
	sameUser := env.Apply(options.Client().ApplyURI("mongodb://oktopus:otherpw@mongo:27017/?authSource=admin"))
	if got := env.Describe(sameUser); got != "enabled (user=oktopus, authSource=admin, source=uri)" {
		t.Fatalf("same user, uri wins: %q", got)
	}
	for _, s := range []string{env.Describe(fromEnv), env.Describe(fromURI), env.String()} {
		if strings.Contains(s, "s3cret") || strings.Contains(s, "pw") {
			t.Fatalf("password leaked: %q", s)
		}
	}
}

/* -------------------------------- config --------------------------------- */

func TestConfigFromEnvDefaults(t *testing.T) {
	cfg := configFromLookup(context.Background(), "CONTROLLER", StandardDefaults, lookupFrom(nil))
	if cfg.Uri != DefaultURI || cfg.Auth.Enabled() {
		t.Fatalf("unexpected uri/auth %+v", cfg)
	}
	if cfg.MaxPoolSize != 50 || cfg.MinPoolSize != 0 || cfg.MaxConnecting != 2 ||
		cfg.ConnectTimeout != 10*time.Second || cfg.ServerSelectionTimeout != 10*time.Second {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestConfigFromEnvOverrides(t *testing.T) {
	env := map[string]string{
		EnvURI:                                              "mongodb://mongo:27017",
		EnvPassword:                                         "s3cret",
		"CONTROLLER_MONGO_MAX_POOL_SIZE":                    "7",
		"CONTROLLER_MONGO_MIN_POOL_SIZE":                    "1",
		"CONTROLLER_MONGO_MAX_CONNECTING":                   "3",
		"CONTROLLER_MONGO_CONNECT_TIMEOUT_SECONDS":          "4",
		"CONTROLLER_MONGO_SERVER_SELECTION_TIMEOUT_SECONDS": "5",
		"ADAPTER_MONGO_MAX_POOL_SIZE":                       "99", // another service's knob
		"CONTROLLER_MONGO_BOGUS":                            "x",
	}
	cfg := configFromLookup(context.Background(), "CONTROLLER_", StandardDefaults, lookupFrom(env))
	if cfg.Uri != "mongodb://mongo:27017" || !cfg.Auth.Enabled() || cfg.Auth.Username != DefaultUsername {
		t.Fatalf("unexpected uri/auth %+v", cfg)
	}
	if cfg.MaxPoolSize != 7 || cfg.MinPoolSize != 1 || cfg.MaxConnecting != 3 ||
		cfg.ConnectTimeout != 4*time.Second || cfg.ServerSelectionTimeout != 5*time.Second {
		t.Fatalf("overrides not applied: %+v", cfg)
	}

	// A value that does not parse falls back to the default instead of 0.
	cfg = configFromLookup(context.Background(), "CONTROLLER", StandardDefaults,
		lookupFrom(map[string]string{"CONTROLLER_MONGO_MAX_POOL_SIZE": "many"}))
	if cfg.MaxPoolSize != 50 {
		t.Fatalf("unparsable value must keep the default, got %d", cfg.MaxPoolSize)
	}
}

func TestClientOptionsCarryPoolAndAuth(t *testing.T) {
	cfg := Config{
		Uri:                    "mongodb://mongo:27017",
		Auth:                   Credentials{Username: "oktopus", Password: "s3cret", AuthSource: "admin"},
		MaxPoolSize:            12,
		MinPoolSize:            3,
		MaxConnecting:          4,
		ConnectTimeout:         6 * time.Second,
		ServerSelectionTimeout: 7 * time.Second,
	}
	opts := ClientOptions(cfg)
	if opts.MaxPoolSize == nil || *opts.MaxPoolSize != 12 || opts.MinPoolSize == nil || *opts.MinPoolSize != 3 ||
		opts.MaxConnecting == nil || *opts.MaxConnecting != 4 ||
		opts.ConnectTimeout == nil || *opts.ConnectTimeout != 6*time.Second ||
		opts.ServerSelectionTimeout == nil || *opts.ServerSelectionTimeout != 7*time.Second {
		t.Fatalf("pool options not applied: %+v", opts)
	}
	if opts.Auth == nil || opts.Auth.Username != "oktopus" {
		t.Fatalf("credentials not applied: %+v", opts.Auth)
	}
}

/* ------------------------------- connecting ------------------------------- */

func TestNextReconnectDelay(t *testing.T) {
	tests := []struct {
		name    string
		delay   time.Duration
		wantMin time.Duration
		wantMax time.Duration // exclusive
	}{
		{"doubles from initial", 2 * time.Second, 2 * time.Second, 4 * time.Second},
		{"keeps doubling", 4 * time.Second, 4 * time.Second, 8 * time.Second},
		{"keeps doubling further", 8 * time.Second, 8 * time.Second, 16 * time.Second},
		{"caps once doubling would exceed max", 16 * time.Second, 15 * time.Second, 30 * time.Second},
		{"stays capped once at max", 30 * time.Second, 15 * time.Second, 30 * time.Second},
		{"stays capped well past max", time.Minute, 15 * time.Second, 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// jitter is randomized, so sample repeatedly to exercise the range.
			for i := 0; i < 200; i++ {
				got := nextReconnectDelay(tt.delay)
				if got < tt.wantMin || got >= tt.wantMax {
					t.Fatalf("nextReconnectDelay(%v) = %v, want in [%v, %v)", tt.delay, got, tt.wantMin, tt.wantMax)
				}
			}
		})
	}
}

func TestConnectRetriesPingAfterFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := Config{
		Uri:                    "mongodb://127.0.0.1:1",
		Ctx:                    ctx,
		MaxPoolSize:            StandardDefaults.MaxPoolSize,
		MaxConnecting:          StandardDefaults.MaxConnecting,
		ConnectTimeout:         10 * time.Millisecond,
		ServerSelectionTimeout: 10 * time.Millisecond,
	}

	pingAttempts := 0
	ping := func(context.Context, *mongo.Client) error {
		pingAttempts++
		if pingAttempts == 1 {
			return errors.New("MongoDB unavailable")
		}
		return nil
	}

	var logs bytes.Buffer
	client, err := connect(cfg, ping, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil || client == nil {
		t.Fatalf("connect() = %v, %v", client, err)
	}
	if pingAttempts != 2 {
		t.Fatalf("ping attempts = %d, want 2", pingAttempts)
	}
	if !strings.Contains(logs.String(), "Failed to connect to MongoDB") || !strings.Contains(logs.String(), `"mongo_auth":"disabled"`) {
		t.Fatalf("unexpected logs: %s", logs.String())
	}

	disconnectCtx, cancelDisconnect := context.WithTimeout(context.Background(), time.Second)
	defer cancelDisconnect()
	if err := client.Disconnect(disconnectCtx); err != nil {
		t.Fatalf("disconnect MongoDB client: %v", err)
	}
}

func TestConnectGivesUpWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cfg := Config{
		Uri:                    "mongodb://127.0.0.1:1",
		Ctx:                    ctx,
		ConnectTimeout:         10 * time.Millisecond,
		ServerSelectionTimeout: 10 * time.Millisecond,
	}
	failing := func(context.Context, *mongo.Client) error { return errors.New("down") }

	start := time.Now()
	client, err := connect(cfg, failing, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	elapsed := time.Since(start)
	if client != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("connect() = %v, %v; want nil, context.DeadlineExceeded", client, err)
	}
	if elapsed < initialReconnectDelay {
		t.Errorf("connect() returned after %v, want at least the first backoff delay (%v)", elapsed, initialReconnectDelay)
	}
	if elapsed > 4*time.Second {
		t.Errorf("connect() took %v to give up, want close to the 3s context timeout", elapsed)
	}
}

func TestPingNilClient(t *testing.T) {
	if err := Ping(context.Background(), nil); err == nil {
		t.Fatal("expected an error for a nil client")
	}
	if IsConnected(context.Background(), nil) {
		t.Fatal("a nil client is not connected")
	}
}

/* -------------------------------- monitor -------------------------------- */

func TestMonitorLogsOutageAndReminders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	done := make(chan struct{})

	outcomes := []error{
		errors.New("MongoDB unavailable"),
		errors.New("MongoDB still unavailable"),
		nil,
		nil,
	}
	pingCalls := 0
	ping := func(context.Context) error {
		if pingCalls == len(outcomes) {
			cancel()
			return context.Canceled
		}
		err := outcomes[pingCalls]
		pingCalls++
		return err
	}

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	go func() {
		monitor(ctx, ticks, ping, logger)
		close(done)
	}()

	// The fifth tick synchronizes completion of the fourth check, then
	// cancels the monitor without producing another state transition.
	for i := 0; i < len(outcomes)+1; i++ {
		ticks <- time.Now()
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("monitor did not stop after context cancellation")
	}

	out := logs.String()
	for _, want := range []string{"MongoDB connection lost", "MongoDB connection still unavailable", "MongoDB connection restored"} {
		if strings.Count(out, want) != 1 {
			t.Fatalf("expected exactly one %q in logs, got:\n%s", want, out)
		}
	}
}

func TestMonitorStopsWithoutLoggingOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var logs bytes.Buffer
	done := make(chan struct{})
	go func() {
		monitor(ctx, make(chan time.Time), func(context.Context) error { return nil }, slog.New(slog.NewJSONHandler(&logs, nil)))
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("monitor did not stop after context cancellation")
	}
	if logs.Len() != 0 {
		t.Fatalf("expected no logs, got %s", logs.String())
	}
}
