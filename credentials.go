package mongoconn

import (
	"fmt"
	"os"
	"strings"

	"go.mongodb.org/mongo-driver/mongo/options"
)

// Environment variables that carry the MongoDB credentials. They are shared by
// every service (no per-service prefix): one secret per environment is enough.
const (
	// EnvURI names the environment variable with the MongoDB connection string.
	EnvURI = "MONGO_URI"
	// EnvUsername names the environment variable with the MongoDB user name.
	EnvUsername = "MONGO_USER"
	// EnvPassword names the environment variable with the MongoDB password.
	EnvPassword = "MONGO_PASSWORD"
	// EnvAuthSource names the environment variable with the authentication
	// database (the database the user was created in).
	EnvAuthSource = "MONGO_AUTH_SOURCE"

	// DefaultURI is used when MONGO_URI is not set.
	DefaultURI = "mongodb://localhost:27017"
	// DefaultUsername is used when MONGO_PASSWORD is set but MONGO_USER is not.
	DefaultUsername = "oktopus"
	// DefaultAuthSource is used when MONGO_AUTH_SOURCE is not set.
	DefaultAuthSource = "admin"
)

// Credentials is what a service needs to authenticate against MongoDB.
// The zero value means "no authentication".
//
// MONGO_PASSWORD is the switch: while it is empty the service connects without
// authentication, exactly as it did before credentials existed, so a new image
// can be rolled out ahead of the MongoDB change and the credentials turned on
// later, per environment. No password ever has a default: a default password
// compiled into the binary would be the same for every customer and could not
// be rotated without a new release.
type Credentials struct {
	Username   string
	Password   string
	AuthSource string
}

// CredentialsFromEnv reads the credentials from the process environment.
func CredentialsFromEnv() Credentials {
	return credentialsFromLookup(os.LookupEnv)
}

// credentialsFromLookup builds the credentials from any variable source, so
// the rules can be tested without touching the process environment. Values
// are trimmed, so a stray space at the end of a line in an .env file does not
// turn into a wrong password.
func credentialsFromLookup(lookup func(string) (string, bool)) Credentials {
	get := func(name string) string {
		v, _ := lookup(name)
		return strings.TrimSpace(v)
	}

	c := Credentials{
		Username:   get(EnvUsername),
		Password:   get(EnvPassword),
		AuthSource: get(EnvAuthSource),
	}
	if !c.Enabled() {
		// Keep the zero value: a lone MONGO_USER must not switch auth on.
		return Credentials{}
	}
	if c.Username == "" {
		c.Username = DefaultUsername
	}
	if c.AuthSource == "" {
		c.AuthSource = DefaultAuthSource
	}
	return c
}

// Enabled reports whether the service should authenticate. The password is
// the switch: without one, the connection stays anonymous.
func (c Credentials) Enabled() bool {
	return c.Password != ""
}

// Apply sets the credentials on opts when they are enabled and the URI did
// not already carry its own (user:password@host). It returns opts so it can
// be chained with the other options.Client() setters.
func (c Credentials) Apply(opts *options.ClientOptions) *options.ClientOptions {
	if opts == nil || !c.Enabled() || opts.Auth != nil {
		return opts
	}
	return opts.SetAuth(options.Credential{
		AuthSource: c.AuthSource,
		Username:   c.Username,
		Password:   c.Password,
	})
}

// Describe reports the credentials the client will actually use, for the
// "Connected to MongoDB" log line: the ones from the URI when it carries any,
// otherwise the ones from the environment. It never includes the password.
func (c Credentials) Describe(opts *options.ClientOptions) string {
	if opts == nil || opts.Auth == nil {
		return "disabled"
	}
	source := "uri"
	if c.Enabled() && opts.Auth.Username == c.Username &&
		opts.Auth.Password == c.Password && opts.Auth.AuthSource == c.AuthSource {
		source = "env"
	}
	return fmt.Sprintf("enabled (user=%s, authSource=%s, source=%s)",
		opts.Auth.Username, opts.Auth.AuthSource, source)
}

// String describes the authentication mode for logs. It never includes the
// password.
func (c Credentials) String() string {
	if !c.Enabled() {
		return "disabled"
	}
	return fmt.Sprintf("enabled (user=%s, authSource=%s)", c.Username, c.AuthSource)
}
