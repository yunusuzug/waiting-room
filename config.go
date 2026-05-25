package waitingroom

import (
	"fmt"
	"time"
)

// DatabaseConfig holds the PostgreSQL connection configuration.
type DatabaseConfig struct {
	// Host is the PostgreSQL host address.
	// Default: "localhost"
	Host string

	// Port is the PostgreSQL port.
	// Default: "5432"
	Port string

	// Name is the name of the database to connect to.
	// Required.
	Name string

	// User is the username for database authentication.
	// Required.
	User string

	// Password is the password for database authentication.
	// Required.
	Password string

	// SSLMode is the SSL mode for the connection.
	// Default: "disable"
	SSLMode string
}

// setDefaults applies default values for unspecified database options.
func (d *DatabaseConfig) setDefaults() {
	if d.Host == "" {
		d.Host = "localhost"
	}
	if d.Port == "" {
		d.Port = "5432"
	}
	if d.SSLMode == "" {
		d.SSLMode = "disable"
	}
}

// Validate checks that all required database fields are set.
func (d *DatabaseConfig) Validate() error {
	if d.Name == "" {
		return fmt.Errorf("database name is required")
	}
	if d.User == "" {
		return fmt.Errorf("database user is required")
	}
	return nil
}

// ConnectionString builds the PostgreSQL connection string from the config.
func (d *DatabaseConfig) ConnectionString() (string, error) {
	d.setDefaults()

	if err := d.Validate(); err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s",
		d.User,
		d.Password,
		d.Host,
		d.Port,
		d.Name,
		d.SSLMode,
	), nil
}

// Config holds the configuration for the waiting-room library.
// Applications pass this when initializing the TaskManager.
type Config struct {
	// Database contains the PostgreSQL connection configuration.
	// Required.
	Database DatabaseConfig

	// ApplicationID identifies which application owns the tasks.
	// This is used to distinguish tasks from different applications in the same database.
	ApplicationID string

	// WorkerInterval is the polling interval for the scheduler to check
	// for pending and scheduled tasks. Default is 1 minute.
	WorkerInterval time.Duration

	// LockTimeout is the duration after which a distributed lock is considered
	// expired and can be acquired by another instance. Default is 5 minutes.
	LockTimeout time.Duration

	// MaxConcurrentTasks is the maximum number of tasks that can run concurrently.
	// Default is 10.
	MaxConcurrentTasks int

	// instanceID is internally generated to uniquely identify this running instance
	// for distributed locking. It is auto-generated on each startup.
	instanceID string
}

// setDefaults applies default values for unspecified configuration options.
func (c *Config) setDefaults() {
	if c.WorkerInterval <= 0 {
		c.WorkerInterval = time.Minute
	}
	if c.LockTimeout <= 0 {
		c.LockTimeout = 5 * time.Minute
	}
	if c.MaxConcurrentTasks <= 0 {
		c.MaxConcurrentTasks = 10
	}
}
